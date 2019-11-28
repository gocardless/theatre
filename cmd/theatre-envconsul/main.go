package main

import (
	"context"
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

	exec             = app.Command("exec", "Authenticate with vault and exec envconsul")
	execVaultOptions = newVaultOptions(exec)
	execConfigFile   = exec.Flag("config-file", "app config file").String()
	execInstallPath  = exec.Flag("install-path", "Path containing installed binaries").Default(defaultInstallPath).String()
	execCommand      = exec.Arg("command", "Command to execute").Required().Strings()

	configure             = app.Command("configure", "Configures Vault with a Kubernetes auth backend (for testing)")
	configureVaultOptions = newVaultOptions(configure)

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
		if execVaultOptions.Token == "" {
			clusterConfig, err := getClusterConfig()
			if err != nil {
				return errors.Wrap(err, "failed to authenticate within kubernetes")
			}

			execVaultOptions.Decorate(logger).Log("event", "vault.login")
			vaultToken, err = execVaultOptions.Login(clusterConfig.BearerToken)
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

		envconsulConfig := execVaultOptions.EnvconsulConfig(secretEnv, vaultToken, *execCommand)
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

	// This command should only be used for preparing a test environment. It is
	// used for configuring a Vault server in our acceptance tests to provide
	// Kubernetes authentication via service account.
	//
	// It does several things:
	//
	// - Mounts a kv2 secrets engine at secret/
	// - Creates a Kubernetes auth backend mounted at auth/kubernetes
	// - Configures the Kubernetes backend to authenticate against the currently
	// 	detected Kubernetes API server (the current cluster, if run from within)
	// - For all successful Kubernetes logins, the user is assigned a token that
	// 	maps to a cluster-reader policy, which permits reading of secrets from:
	//
	// 	secret/data/kubernetes/{namespace}/{service-account-name}/*
	case configure.FullCommand():
		client, err := configureVaultOptions.Client()
		if err != nil {
			return errors.Wrap(err, "failed to configure vault client")
		}

		mountPath := "secret"
		mountOptions := &api.MountInput{
			Type:        "kv",
			Description: "Generic Vault kv mount",
			Options: map[string]string{
				"version": "2",
			},
		}

		logger.Log("msg", "mounting secret engine", "path", mountPath, "options", mountOptions)
		client.Sys().Unmount(mountPath)
		if err := client.Sys().Mount(mountPath, mountOptions); err != nil {
			return err
		}

		backendPath := configureVaultOptions.AuthBackendMountPoint
		enableOptions := &api.EnableAuthOptions{
			Type:        "kubernetes",
			Description: "Permit authentication by Kubernetes service accounts",
		}

		logger.Log("msg", "enabling auth mount", "path", backendPath, "options", enableOptions)
		client.Sys().DisableAuth(backendPath)
		if err := client.Sys().EnableAuthWithOptions(backendPath, enableOptions); err != nil {
			return err
		}

		logger.Log("msg", "authenticating against kubernetes")
		clusterConfig, err := getClusterConfig()
		if err != nil {
			return errors.Wrap(err, "failed to authenticate against kubernetes")
		}

		var ca string

		if string(clusterConfig.CAData) == "" {
			caBytes, err := ioutil.ReadFile(clusterConfig.CAFile)
			ca = string(caBytes)
			if err != nil {
				return errors.Wrap(err, "could not parse certificate for kubernetes")
			}
		} else {
			ca = string(clusterConfig.CAData)
		}

		backendConfigPath := fmt.Sprintf("auth/%s/config", configureVaultOptions.AuthBackendMountPoint)
		backendConfig := map[string]interface{}{
			"kubernetes_host":    clusterConfig.Host,
			"kubernetes_ca_cert": string(ca),
		}

		logger.Log("msg", "writing auth backend config", "path", backendConfigPath, "config", backendConfig)
		if _, err := client.Logical().Write(backendConfigPath, backendConfig); err != nil {
			return err
		}

		backendRolePath := fmt.Sprintf("auth/%s/role/default", configureVaultOptions.AuthBackendMountPoint)
		backendRoleConfig := map[string]interface{}{
			// https://github.com/hashicorp/vault-plugin-auth-kubernetes/pull/66
			"bound_service_account_names": strings.Split(
				"a*,b*,c*,d*,e*,f*,h*,i*,j*,k*,l*,m*,n*,o*,p*,q*,r*,s*,t*,u*,v*,w*,x*,y*,z*,1*,2*,3*,4*,5*,6*,7*,8*,9*,0*", ",",
			),
			"bound_service_account_namespaces": []string{"*"},
			"token_policies":                   []string{"default", "cluster-reader"},
			"token_ttl":                        600,
		}

		logger.Log("msg", "creating default backend role", "path", backendRolePath)
		if _, err := client.Logical().Write(backendRolePath, backendRoleConfig); err != nil {
			return err
		}

		auths, err := client.Sys().ListAuth()
		if err != nil {
			return errors.Wrap(err, "could not list auth backends which prevents linking roles against a backend")
		}

		backend := auths[fmt.Sprintf("%s/", configureVaultOptions.AuthBackendMountPoint)]
		readerPathTemplate :=
			"{{identity.entity.aliases.%s.metadata.service_account_namespace}}/" +
				"{{identity.entity.aliases.%s.metadata.service_account_name}}/" +
				"*"

		policyRules := fmt.Sprintf(
			`path "secret/data/kubernetes/%s" { capabilities = ["read"] }`,
			fmt.Sprintf(readerPathTemplate, backend.Accessor, backend.Accessor),
		)

		logger.Log("msg", "creating cluster-reader policy to permit kubernetes service accounts to read secrets")
		if err := client.Sys().PutPolicy("cluster-reader", policyRules); err != nil {
			return err
		}

		secretPath := "secret/data/kubernetes/staging/secret-reader/jimmy"
		secretData := map[string]interface{}{"data": map[string]interface{}{"data": "eats-the-world"}}

		logger.Log("msg", "writing sentinel secret value", "path", secretPath, "data", secretData)
		if _, err := client.Logical().Write(secretPath, secretData); err != nil {
			return err
		}

	default:
		panic("unrecognised command")
	}

	return nil
}

// getClusterConfig attempts to construct a Kubernetes client configuration, preferring in
// cluster auth but falling back to other detection methods if that fails.
func getClusterConfig() (*rest.Config, error) {
	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		clusterConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		).ClientConfig()

		if err != nil {
			return nil, err
		}
	}

	return clusterConfig, err
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
func (o *vaultOptions) EnvconsulConfig(env environment, token string, command []string) *EnvconsulConfig {
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
			Command: strings.Join(command, " "),
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
