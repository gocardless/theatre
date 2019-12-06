package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	execpkg "os/exec"
	"path"
	"strings"
	"syscall"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"gopkg.in/yaml.v2"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/hashicorp/vault/api"
	"github.com/pkg/errors"

	"github.com/gocardless/theatre/cmd"
	"github.com/gocardless/theatre/pkg/signals"
)

var logger kitlog.Logger

var (
	app = kingpin.New("theatre-envconsul", "Kubernetes container vault support using envconsul").Version(Version)

	commonOpts                     = cmd.NewCommonOptions(app)
	defaultInstallPath             = "/var/theatre-vault"
	defaultTheatreEnvconsulPath, _ = os.Executable()

	install                       = app.Command("install", "Install binaries into path")
	installPath                   = install.Flag("path", "Path to install theatre binaries").Default(defaultInstallPath).String()
	installEnvconsulBinary        = install.Flag("envconsul-binary", "Path to envconsul binary").Default("/usr/local/bin/envconsul").String()
	installTheatreEnvconsulBinary = install.Flag("theatre-envconsul-binary", "Path to theatre-envconsul binary").Default(defaultTheatreEnvconsulPath).String()

	exec                        = app.Command("exec", "Authenticate with vault and exec envconsul")
	execVaultOptions            = newVaultOptions(exec)
	execConfigFile              = exec.Flag("config-file", "app config file").String()
	execInstallPath             = exec.Flag("install-path", "Path containing installed binaries").Default(defaultInstallPath).String()
	execTheatreEnvconsulBinary  = exec.Flag("theatre-envconsul-binary", "Path to theatre-envconsul binary").Default(defaultTheatreEnvconsulPath).String()
	execServiceAccountTokenFile = exec.Flag("service-account-token-file", "Path to Kubernetes service account token file").String()
	execCommand                 = exec.Arg("command", "Command to execute").Required().Strings()

	base64Exec        = app.Command("base64-exec", "Decode base64 encoded args and exec them").Hidden()
	base64ExecCommand = base64Exec.Arg("base64-command", "Command to execute").Required().Strings()

	// Version is set at compile time
	Version = "dev"
)

func main() {
	command := kingpin.MustParse(app.Parse(os.Args[1:]))
	logger = commonOpts.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	if err := mainError(ctx, command); err != nil {
		logger.Log("error", err, "msg", "exiting with error")
		os.Exit(1)
	}
}

func mainError(ctx context.Context, command string) (err error) {
	switch command {
	// Install theatre binaries into the target installation directory. This is used to
	// prime any target containers with the tools they will need to authenticate with Vault
	// and pull secrets.
	case install.FullCommand():
		files := map[string]string{
			*installEnvconsulBinary:        "envconsul",
			*installTheatreEnvconsulBinary: "theatre-envconsul",
		}

		logger.Log("msg", "copying files into install path", "file_path", *installPath)
		for src, dstName := range files {
			if err := copyExecutable(src, path.Join(*installPath, dstName)); err != nil {
				return errors.Wrap(err, "error copying file")
			}
		}

	// Run the authentication dance against Vault, exchanging our Kubernetes service account
	// token for a Vault token that can read secrets. Then prepare a Hashicorp envconsul
	// configuration file and exec into envconsul with the Vault token, leaving envconsul to
	// do all the secret fetching and lease management.
	case exec.FullCommand():
		var vaultToken string
		if execVaultOptions.Token == "" {
			serviceAccountToken, err := getKubernetesToken(*execServiceAccountTokenFile)
			if err != nil {
				return errors.Wrap(err, "failed to authenticate within kubernetes")
			}

			execVaultOptions.Decorate(logger).Log("event", "vault.login")
			vaultToken, err = execVaultOptions.Login(serviceAccountToken)
			if err != nil {
				return errors.Wrap(err, "failed to login to vault")
			}
		}

		var env = environment{}

		// Load all the environment variables we currently know from our process
		for _, element := range os.Environ() {
			nameValue := strings.SplitN(element, "=", 2)
			env[nameValue[0]] = nameValue[1]
		}

		if *execConfigFile != "" {
			logger.Log("event", "config.load", "file_path", *execConfigFile)
			config, err := loadConfigFromFile(*execConfigFile)
			if err != nil {
				return err
			}

			// Load all the values from our config, which will now override what is set in the
			// environment variables of the current process
			for key, value := range config.Environment {
				env[key] = value
			}
		}

		var secretEnv = environment{}

		// For all the environment values that look like they should be vault references, we
		// can place them in secretEnv so we can render an envconsul configuration file for
		// them.
		for key, value := range env {
			if strings.HasPrefix(value, "vault:") {
				secretEnv[key] = strings.TrimPrefix(value, "vault:")
			}
		}

		if len(secretEnv) == 0 {
			return errors.New("no 'vault:' prefix found in config or environment")
		}

		envconsulConfig := execVaultOptions.EnvconsulConfig(secretEnv, vaultToken, *execTheatreEnvconsulBinary, *execCommand)
		configJSONContents, err := json.Marshal(envconsulConfig)
		if err != nil {
			return err
		}

		tempConfigFile, err := ioutil.TempFile("", "envconsul-config-*.json")
		if err != nil {
			return errors.Wrap(err, "failed to create temporary file for envconsul")
		}

		logger.Log("event", "envconsul_config_file.create", "path", tempConfigFile.Name())
		if err := ioutil.WriteFile(tempConfigFile.Name(), configJSONContents, 0444); err != nil {
			return errors.Wrap(err, "failed to write temporary file for envconsul")
		}

		// Set all our environment variables which will proxy through to our exec'd process
		for key, value := range env {
			os.Setenv(key, value)
		}

		envconsulBinaryPath := path.Join(*execInstallPath, "envconsul")
		envconsulArgs := []string{envconsulBinaryPath, "-config", tempConfigFile.Name()}

		logger.Log("event", "envconsul.exec", "binary", envconsulBinaryPath, "path", tempConfigFile.Name())
		if err := syscall.Exec(envconsulBinaryPath, envconsulArgs, os.Environ()); err != nil {
			return errors.Wrap(err, "failed to execute envconsul")
		}

	// Hidden command that allows us to exec a command using base64 encoded arguments. As
	// envconsul, the Hashicorp tool, only allows us to specify a command string, we have to
	// ensure we preserve the original commands shellword split.
	//
	// We use the exec command to generate an envconsul config with base64 encoded
	// arguments, passed to this base64-exec command, that we know will be split correctly.
	// This command then does the final execution, ensuring the split remains consistent.
	case base64Exec.FullCommand():
		args := []string{}
		for _, base64arg := range *base64ExecCommand {
			arg, err := base64.StdEncoding.DecodeString(base64arg)
			if err != nil {
				app.Fatalf("failed to decode base64 argument: %s", arg)
			}

			args = append(args, string(arg))
		}

		var err error
		args[0], err = execpkg.LookPath(args[0])
		if err != nil {
			app.Fatalf("could not resolve binary for command: %v", err)
		}

		if err := syscall.Exec(args[0], args, os.Environ()); err != nil {
			return errors.Wrap(err, "failed to execute decoded arguments")
		}

	default:
		panic("unrecognised command")
	}

	return nil
}

// getKubernetesToken attempts to construct a Kubernetes client configuration, preferring
// in cluster auth but falling back to other detection methods if that fails.
func getKubernetesToken(tokenFileOverride string) (string, error) {
	if tokenFileOverride != "" {
		tokenBytes, err := ioutil.ReadFile(tokenFileOverride)
		return string(tokenBytes), err
	}

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		clusterConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		).ClientConfig()

		if err != nil {
			return "", err
		}
	}

	return clusterConfig.BearerToken, err
}

// copyExecutable is designed to load an executable binary from our current environment
// into a volume that will later be passed to a application container.
func copyExecutable(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return errors.Wrapf(err, "error copying %s -> %s", src, dst)
	}

	// We don't know if we're running as the same user as our container will be, so we need
	// to mark this file as executable by all users.
	if err := os.Chmod(dst, 0555); err != nil {
		return errors.Wrapf(err, "failed to make executable: %s", dst)
	}

	return nil
}

type vaultOptions struct {
	Address               string
	UseTLS                bool
	InsecureSkipVerify    bool
	Token                 string
	AuthBackendMountPoint string
	AuthBackendRole       string
}

func newVaultOptions(cmd *kingpin.CmdClause) *vaultOptions {
	opt := &vaultOptions{}

	cmd.Flag("auth-backend-mount-path", "Vault auth backend mount path").Default("kubernetes").StringVar(&opt.AuthBackendMountPoint)
	cmd.Flag("auth-backend-role", "Vault auth backend role").Default("default").StringVar(&opt.AuthBackendRole)
	cmd.Flag("vault-address", "Address of vault (format: scheme://host:port)").Required().StringVar(&opt.Address)
	cmd.Flag("vault-token", "Vault token to use, instead of Kubernetes auth").OverrideDefaultFromEnvar("VAULT_TOKEN").StringVar(&opt.Token)
	cmd.Flag("vault-use-tls", "Use TLS when connecting to Vault").Default("true").BoolVar(&opt.UseTLS)
	cmd.Flag("vault-insecure-skip-verify", "Skip TLS certificate verification when connecting to Vault").Default("false").BoolVar(&opt.InsecureSkipVerify)

	return opt
}

func (o *vaultOptions) Client() (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = o.Address

	transport := cfg.HttpClient.Transport.(*http.Transport)
	if o.InsecureSkipVerify {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	if !o.UseTLS {
		transport.TLSClientConfig = nil
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	if o.Token != "" {
		client.SetToken(o.Token)
	}

	return client, err
}

func (o *vaultOptions) Decorate(logger kitlog.Logger) kitlog.Logger {
	return kitlog.With(logger, "address", o.Address, "backend", o.AuthBackendMountPoint, "role", o.AuthBackendRole)
}

// Login uses the kubernetes service account token to authenticate against the Vault
// server. The Vault server is configured with a specific authentication backend that can
// validate the service account token we provide is valid. We are asking Vault to assign
// us the specified role.
func (o *vaultOptions) Login(jwt string) (string, error) {
	client, err := o.Client()
	if err != nil {
		return "", err
	}

	req := client.NewRequest("POST", fmt.Sprintf("/v1/auth/%s/login", o.AuthBackendMountPoint))
	req.SetJSONBody(map[string]string{
		"jwt":  jwt,
		"role": o.AuthBackendRole,
	})

	resp, err := client.RawRequest(req)
	if err != nil {
		return "", err
	}

	if err := resp.Error(); err != nil {
		return "", err
	}

	var secret api.Secret
	if err := resp.DecodeJSON(&secret); err != nil {
		return "", errors.Wrap(err, "failed to decode vault login response")
	}

	return secret.Auth.ClientToken, nil
}

// Config is the configuration file format that the exec command will use to parse the
// Vault references that it will pass onto the envconsul command. We expect application
// developers to include this file within their applications.
type Config struct {
	Environment environment `yaml:"environment"`
}

type environment map[string]string

func loadConfigFromFile(configFile string) (Config, error) {
	var cfg Config

	yamlContent, err := ioutil.ReadFile(configFile)
	if err != nil {
		return cfg, errors.Wrap(err, "failed to open config file")
	}

	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		return cfg, errors.Wrap(err, "failed to parse config")
	}

	if cfg.Environment == nil {
		return cfg, fmt.Errorf("missing 'environment' key in configuration file")
	}

	return cfg, nil
}

// EnvconsulConfig generates a configuration file that envconsul (hashicorp) can read, and
// will use to resolve secret values into environment variables.
//
// This will only work if your vault secrets have exactly one key. The format specifier we
// pass to envconsul uses no interpolation, so multiple keys in a vault secret would be
// assigned the same environment variable. This is undefined behaviour, resulting in
// subsequent executions setting different values for the same env var.
func (o *vaultOptions) EnvconsulConfig(env environment, token string, theatreEnvconsulPath string, args []string) *EnvconsulConfig {
	base64args := []string{}
	for _, arg := range args {
		base64args = append(base64args, base64.StdEncoding.EncodeToString([]byte(arg)))
	}

	cfg := &EnvconsulConfig{
		Vault: envconsulVault{
			Address: o.Address,
			Token:   token,
			Retry: envconsulRetry{
				Enabled: false,
			},
			SSL: envconsulSSL{
				Enabled: o.UseTLS,
				Verify:  !o.InsecureSkipVerify,
			},
		},
		Exec: envconsulExec{
			// Base64 encode the command and pass it to theatre-envconsul base64-exec. This
			// ensures we preserve command splitting, instead of relying on envconsul's shell
			// splitting to do the right thing.
			Command: fmt.Sprintf("%s %s %s", theatreEnvconsulPath, base64Exec.FullCommand(), strings.Join(base64args, " ")),
		},
		Secret: []envconsulSecret{},
	}

	for key, value := range env {
		cfg.Secret = append(cfg.Secret, envconsulSecret{Format: key, Path: value})
	}

	return cfg
}

// EnvconsulConfig defines the subset of the configuration we use for envconsul:
// https://github.com/hashicorp/envconsul/blob/master/config.go
type EnvconsulConfig struct {
	Vault  envconsulVault    `json:"vault"`
	Exec   envconsulExec     `json:"exec"`
	Secret []envconsulSecret `json:"secret"`
}

type envconsulVault struct {
	Address string         `json:"address"`
	Token   string         `json:"token"`
	Retry   envconsulRetry `json:"retry"`
	SSL     envconsulSSL   `json:"ssl"`
}

type envconsulRetry struct {
	Enabled bool `json:"enabled"`
}

type envconsulSSL struct {
	Enabled bool `json:"enabled"`
	Verify  bool `json:"verify"`
}

type envconsulExec struct {
	Command string `json:"command"`
}

type envconsulSecret struct {
	Format string `json:"format"`
	Path   string `json:"path"`
}
