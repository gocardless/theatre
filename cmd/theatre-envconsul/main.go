package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"syscall"

	"k8s.io/client-go/rest"

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

	commonOpts         = cmd.NewCommonOptions(app)
	defaultInstallPath = "/var/theatre-vault"

	install                       = app.Command("install", "Install binaries into path")
	installPath                   = install.Flag("path", "Path to install theatre binaries").Default(defaultInstallPath).String()
	installEnvconsulBinary        = install.Flag("envconsul-binary", "Path to envconsul binary").Default("/envconsul").String()
	installTheatreEnvconsulBinary = install.Flag("theatre-envconsul-binary", "Path to theatre-envconsul binary").Default(os.Args[0]).String()

	exec                     = app.Command("exec", "Authenticate with vault and exec envconsul")
	execConfigFile           = exec.Flag("config-file", "app config file").String()
	execAuthBackendMountPath = exec.Flag("auth-backend-mount-path", "Vault auth backend mount path").Default("kubernetes").String()
	execAuthBackendRole      = exec.Flag("auth-backend-role", "Vault auth backend role").Default("default").String()
	execCommand              = exec.Flag("command", "Command to execute").Required().String()
	execInstallPath          = exec.Flag("install-path", "Path containing installed binaries").Default(defaultInstallPath).String()
	execVaultAddress         = exec.Flag("vault-address", "Address of vault (format: scheme://host:port)").Required().String()
	execVaultToken           = exec.Flag("vault-token", "Vault token to use, instead of Kubernetes auth").OverrideDefaultFromEnvar("VAULT_TOKEN").String()

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

	case exec.FullCommand():
		var vaultToken string
		if *execVaultToken == "" {
			clusterConfig, err := rest.InClusterConfig()
			if err != nil {
				return errors.Wrap(err, "failed to authenticate within kubernetes")
			}

			logger.Log("event", "vault.login", "host", *execVaultAddress, "backend", *execAuthBackendMountPath, "role", *execAuthBackendRole)
			vaultToken, err = getVaultToken(clusterConfig.BearerToken, *execVaultAddress, *execAuthBackendMountPath, *execAuthBackendRole)
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

		envconsulConfig := buildEnvconsulConfig(secretEnv, *execVaultAddress, vaultToken, *execCommand)
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

	default:
		panic("unrecognised command")
	}

	return nil
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

// getVaultToken uses the kubernetes service account token to authenticate against the
// Vault server. The Vault server is configured with a specific authentication backend
// that can validate the service account token we provide is valid. We are asking Vault to
// assign us the specified role.
func getVaultToken(serviceAccountToken, address, backend, role string) (string, error) {
	vault, err := api.NewClient(
		&api.Config{
			Address: address,
			HttpClient: &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // TODO: Remove this
				},
			},
		},
	)

	req := vault.NewRequest("POST", fmt.Sprintf("/v1/auth/%s/login", backend))
	req.SetJSONBody(map[string]string{
		"role": role,
		"jwt":  serviceAccountToken,
	})

	resp, err := vault.RawRequest(req)
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

// buildEnvconsulConfig generates a configuration file that envconsul (hashicorp) can
// read, and will use to resolve secret values into environment variables.
//
// Whether this works depends entirely on if your vault secrets have more than one key: as
// we set the Format of each secret to be the name of the key, envconsul will generate an
// environment variable for the environment variable name for each value within the vault
// secret. If you have more than one, it is undefined behaviour as to which value you will
// receive in your child process. Don't do this.
func buildEnvconsulConfig(env environment, address, token, command string) *EnvconsulConfig {
	cfg := &EnvconsulConfig{
		Vault: envconsulVault{
			Address: address,
			Token:   token,
			SSL: envconsulSSL{
				Enabled: true,
				Verify:  false, // TODO: true,
			},
		},
		Exec: envconsulExec{
			Command: command,
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
	Address string       `json:"address"`
	Token   string       `json:"token"`
	SSL     envconsulSSL `json:"ssl"`
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
