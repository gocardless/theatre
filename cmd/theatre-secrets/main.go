package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	execpkg "os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/go-logr/logr"
	"github.com/hashicorp/vault/api"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/gocardless/theatre/v4/cmd"
	"github.com/gocardless/theatre/v4/pkg/signals"
)

var logger logr.Logger

var (
	app = kingpin.New("theatre-secrets", "Kubernetes container vault support using secrets").Version(cmd.VersionStanza())

	commonOpts = cmd.NewCommonOptions(app)

	defaultInstallPath           = "/var/theatre-vault"
	defaultTheatreSecretsPath, _ = os.Executable()

	install                     = app.Command("install", "Install binaries into path")
	installPath                 = install.Flag("path", "Path to install theatre binaries").Default(defaultInstallPath).String()
	installTheatreSecretsBinary = install.Flag("theatre-secrets-binary", "Path to theatre-secrets binary").Default(defaultTheatreSecretsPath).String()

	exec                        = app.Command("exec", "Authenticate with vault and exec secrets")
	execVaultOptions            = newVaultOptions(exec)
	execConfigFile              = exec.Flag("config-file", "App config file").String()
	execServiceAccountTokenFile = exec.Flag("service-account-token-file", "Path to Kubernetes service account token file").String()
	execCommand                 = exec.Arg("command", "Command to execute").Required().Strings()
)

type environment map[string]string
type vaultFile struct {
	vaultKey       string
	filesystemPath string
}

func main() {
	command := kingpin.MustParse(app.Parse(os.Args[1:]))
	logger = commonOpts.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	if err := mainError(ctx, command); err != nil {
		logger.Error(err, "exiting with error")
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
			*installTheatreSecretsBinary: "theatre-secrets",
		}

		logger.Info("copying files into install path", "file_path", *installPath)
		for src, dstName := range files {
			if err := copyExecutable(src, path.Join(*installPath, dstName)); err != nil {
				return errors.Wrap(err, "error copying file")
			}
		}

	// Run the authentication dance against Vault, exchanging our Kubernetes service account
	// token for a Vault token that can read secrets. Then parse the available environment
	// variables and config file, if supplied, to determine a list of Vault paths to query
	// for secret data. Once in possession of this secret data, set the environment
	// variables and provision secret data to the filesystem as required.
	case exec.FullCommand():
		if execVaultOptions.Token == "" {
			serviceAccountToken, err := getKubernetesToken(*execServiceAccountTokenFile)
			if err != nil {
				return errors.Wrap(err, "failed to authenticate within kubernetes")
			}

			execVaultOptions.Decorate(logger).Info("logging into vault", "event", "vault.login")

			vaultToken, err := execVaultOptions.Login(serviceAccountToken)
			if err != nil {
				return errors.Wrap(err, "failed to login to vault")
			}

			execVaultOptions.Token = vaultToken
		}

		client, err := execVaultOptions.Client()
		if err != nil {
			return err
		}

		env := environment{}

		// Load all the environment variables we currently know from our process
		for _, element := range os.Environ() {
			nameValue := strings.SplitN(element, "=", 2)
			env[nameValue[0]] = nameValue[1]
		}

		if *execConfigFile != "" {
			logger.Info(
				fmt.Sprintf("loading config from %s", *execConfigFile),
				"event", "config.load",
				"file_path", *execConfigFile,
			)

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

		var (
			// Use a set to describe the keys that we need to pull from Vault,
			// ensuring that API requests aren't repeated if environment variables or
			// secret files use the same Vault key.
			keysToFetch  = map[string]bool{}
			envPlain     = environment{}
			envFromVault = environment{}
			vaultFiles   = map[string]vaultFile{}
		)

		for key, value := range env {
			switch {
			// For all the environment values that look like they should be vault
			// references, store the envvar -> vault path mapping, and add the vault
			// path to our list to pull.
			case strings.HasPrefix(value, "vault:"):
				vaultKey := strings.TrimPrefix(value, "vault:")

				keysToFetch[vaultKey] = true
				envFromVault[key] = vaultKey

			// Support 'vault-file:' prefixed env vars.
			//
			// For reference, the expected formats are
			// 'vault-file:tls-key/2021010100' and
			// 'vault-file:ssh-key/2021010100:/home/user/.ssh/id_rsa'
			case strings.HasPrefix(value, "vault-file:"):
				trimmed := strings.TrimSpace(
					strings.TrimPrefix(value, "vault-file:"),
				)
				if len(trimmed) == 0 {
					return fmt.Errorf("empty vault-file env var: %v", value)
				}

				split := strings.SplitN(trimmed, ":", 2)
				vaultKey := split[0]
				keysToFetch[vaultKey] = true

				// determine if we define a path at which to place the file. For SplitN,
				// N=2 so we only have two cases
				switch len(split) {
				case 2: // path and key
					vaultFiles[key] = vaultFile{
						filesystemPath: split[1],
						vaultKey:       vaultKey,
					}
				case 1: // just key
					vaultFiles[key] = vaultFile{
						filesystemPath: "",
						vaultKey:       vaultKey,
					}
				}
			// For all environment variables that don't have a known prefix, store
			// them in our map of plain envvars so that we can ensure that they're
			// set before exec'ing the wrapped process, even if they've been defined
			// in the configuration file rather than the process environment.
			default:
				envPlain[key] = value
			}
		}

		secretEnv := environment{}

		for key := range keysToFetch {
			path := path.Join(execVaultOptions.PathPrefix, key)

			start := time.Now()
			resp, err := client.Logical().Read(path)

			// Use verbosity 1, which is equal to debug level
			logger.V(1).Info(
				"vault request finished",
				"event", "vault_kv_request.finished",
				"path", path,
				"duration", time.Since(start).Seconds(),
				"outcome", outcome(err),
			)

			if err != nil {
				return errors.Wrap(err, "failed to retrieve secret value from Vault")
			}

			if resp == nil {
				return errors.Errorf("no secret data found at Vault KV path: %s", path)
			}

			value := resp.Data["data"].(map[string]interface{})["data"].(string)
			secretEnv[key] = value
		}

		// Set all our environment variables which will proxy through to our exec'd process
		for key, value := range envPlain {
			os.Setenv(key, value)
		}

		for key, value := range envFromVault {
			os.Setenv(key, secretEnv[value])
		}

		// For every 'vault file' defined in our configuration or environment variables, write
		// the value out to the specified location on the filesystem, or a random path if not
		// specified.
		for key, file := range vaultFiles {
			path := file.filesystemPath
			if path == "" {
				// generate file path prefixed by key
				tempFilePath, err := os.CreateTemp("", fmt.Sprintf("%s-*", key))
				if err != nil {
					return errors.Wrap(err, fmt.Sprintf("failed to write temporary file for key %s", key))
				}

				file.filesystemPath = tempFilePath.Name()
				path = file.filesystemPath
			}
			// ensure the path structure is available
			err := os.MkdirAll(filepath.Dir(path), 0600)
			if err != nil {
				return errors.Wrap(err, "failed to ensure path structure is available")
			}

			logger.Info(
				"creating vault secret file",
				"event", "secret_file.create",
				"path", path,
			)

			// write file with value of envMap[key]
			if err := os.WriteFile(path, []byte(secretEnv[file.vaultKey]), 0600); err != nil {
				return errors.Wrap(err,
					fmt.Sprintf("failed to write file with key %s to path %s", key, path))
			}

			// update the env with the location of the file we've written
			os.Setenv(key, path)
		}

		command := (*execCommand)[0]
		binary, err := execpkg.LookPath(command)
		if err != nil {
			return fmt.Errorf("failed to find application %s in path: %w", command, err)
		}

		logger.Info(
			"executing wrapped application",
			"event", "theatre_secrets.exec",
			"binary", binary,
		)

		args := []string{command}
		args = append(args, (*execCommand)[1:]...)

		// Run the command directly
		if err := syscall.Exec(binary, args, os.Environ()); err != nil {
			return errors.Wrap(err, "failed to execute wrapped program")
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
		tokenBytes, err := os.ReadFile(tokenFileOverride)

		return string(tokenBytes), errors.Wrap(err, "failed to read kubernetes token file")
	}

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		clusterConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		).ClientConfig()

		if err != nil {
			return "", errors.Wrap(err, "failed to construct kubernetes client")
		}
	}

	return clusterConfig.BearerToken, nil
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
	PathPrefix            string
	Timeout               time.Duration
}

func newVaultOptions(cmd *kingpin.CmdClause) *vaultOptions {
	opt := &vaultOptions{}

	cmd.Flag("auth-backend-mount-path", "Vault auth backend mount path").Default("kubernetes").StringVar(&opt.AuthBackendMountPoint)
	cmd.Flag("auth-backend-role", "Vault auth backend role").Default("default").StringVar(&opt.AuthBackendRole)
	cmd.Flag("vault-address", "Address of vault (format: scheme://host:port)").Required().StringVar(&opt.Address)
	cmd.Flag("vault-token", "Vault token to use, instead of Kubernetes auth").OverrideDefaultFromEnvar("VAULT_TOKEN").StringVar(&opt.Token)
	cmd.Flag("vault-use-tls", "Use TLS when connecting to Vault").Default("true").BoolVar(&opt.UseTLS)
	cmd.Flag("vault-insecure-skip-verify", "Skip TLS certificate verification when connecting to Vault").Default("false").BoolVar(&opt.InsecureSkipVerify)
	cmd.Flag("vault-path-prefix", "Path prefix to read Vault secret from").Default("").StringVar(&opt.PathPrefix)
	cmd.Flag("vault-http-timeout", "Timeout in seconds when making requests to vault").Default("2s").DurationVar(&opt.Timeout)

	return opt
}

func (o *vaultOptions) Client() (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = o.Address

	// By default the Vault library uses a retryable HTTP client, but we probably
	// don't want to use the default timeout of 60 seconds as this will
	// significantly slow the effective container start time if Vault is actually
	// responding consistently slowly.
	cfg.Timeout = o.Timeout

	transport := cfg.HttpClient.Transport.(*http.Transport)
	if o.InsecureSkipVerify {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	if !o.UseTLS {
		transport.TLSClientConfig = nil
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create vault client")
	}

	if o.Token != "" {
		client.SetToken(o.Token)
	}

	return client, nil
}

func (o *vaultOptions) Decorate(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		"address", o.Address,
		"backend", o.AuthBackendMountPoint,
		"role", o.AuthBackendRole,
	)
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

	start := time.Now()
	resp, err := client.RawRequest(req)

	// Use verbosity 1, which is equal to debug level
	logger.V(1).Info(
		"Vault login finished",
		"event", "vault_login_request.finished",
		"duration", time.Since(start).Seconds(),
		"outcome", outcome(err),
	)

	if err != nil {
		return "", errors.Wrap(err, "failed to perform login POST request against Vault auth backend mount")
	}

	if err := resp.Error(); err != nil {
		return "", errors.Wrap(err, "received error response from login POST request against Vault auth backend mount")
	}

	var secret api.Secret
	if err := resp.DecodeJSON(&secret); err != nil {
		return "", errors.Wrap(err, "failed to decode vault login response")
	}

	return secret.Auth.ClientToken, nil
}

// Config is the configuration file format that the exec command will use to parse the
// Vault references that define where to pull secret material from. We expect application
// developers to include this file within their applications.
type Config struct {
	Environment environment `yaml:"environment"`
}

func loadConfigFromFile(configFile string) (Config, error) {
	var cfg Config

	yamlContent, err := os.ReadFile(configFile)
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

// Helper for setting the `outcome` field in a log entry.
func outcome(err error) string {
	if err != nil {
		return "failure"
	}

	return "success"
}
