package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/alecthomas/kingpin"

	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/gocardless/theatre/cmd"
	"github.com/gocardless/theatre/pkg/apis"
	"github.com/gocardless/theatre/pkg/signals"
	"github.com/gocardless/theatre/pkg/vault/envconsul"
)

// Set by goreleaser
var (
	// Version is set at compile time
	Version   = "dev"
	Commit    = "none"
	Date      = "unknown"
	BuiltBy   = "unknown"
	GoVersion = runtime.Version()
)

var (
	app                     = kingpin.New("vault-manager", "Manages vault.crd.gocardless.com resources").Version(versionStanza())
	namespace               = app.Flag("namespace", "Kubernetes webhook service namespace").Default("theatre-system").String()
	serviceName             = app.Flag("service-name", "Name of service for webhook").Default("theatre-vault-manager").String()
	webhookName             = app.Flag("webhook-name", "Name of webhook").Default("theatre-vault").String()
	theatreImage            = app.Flag("theatre-image", "Set to the same image as current binary").Required().String()
	installPath             = app.Flag("install-path", "Location to install theatre binaries").Default("/var/run/theatre").String()
	namespaceLabel          = app.Flag("namespace-label", "Namespace label that enables webhook to operate on").Default("theatre-envconsul-injector").String()
	vaultConfigMapName      = app.Flag("vault-configmap-name", "Vault configMap name containing vault configuration").Default("vault-config").String()
	vaultConfigMapNamespace = app.Flag("vault-configmap-namespace", "Namespace of vault configMap").Default("vault-system").String()

	// These configuration parameters alter how the injector mounts service account tokens.
	// We expect tokens to be sent to Vault, outside of the Kubernetes cluster, so we ensure
	// the tokens used are short-lived in case they are exposed.
	//
	// If an audience is set, this will prevent Kubernetes assigning the default cluster
	// audience, meaning the token won't be useable to authenticate against the API server.
	// Only set this value if Vault is configured with its own token to perform reviews,
	// otherwise the auth chain will be broken.
	serviceAccountTokenFile = app.Flag("service-account-token-file", "Mount path for the Kubernetes service account token").
				Default("/var/run/secrets/kubernetes.io/vault/token").String()
	serviceAccountTokenExpiry   = app.Flag("service-account-token-expiry", "Expiry for service account tokens").Default("15m").Duration()
	serviceAccountTokenAudience = app.Flag("service-account-token-audience", "Audience for the projected service account token").String()

	commonOpts = cmd.NewCommonOptions(app).WithMetrics(app)
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	if err := apis.AddToScheme(scheme.Scheme); err != nil {
		app.Fatalf("failed to add schemes: %v", err)
	}

	go func() {
		commonOpts.ListenAndServeMetrics(logger)
	}()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	opts := webhook.ServerOptions{
		CertDir: "/tmp/theatre-vault",
		BootstrapOptions: &webhook.BootstrapOptions{
			MutatingWebhookConfigName: *webhookName,
			Service: &webhook.Service{
				Namespace: *namespace,
				Name:      *serviceName,
				Selectors: map[string]string{
					"app":   "theatre",
					"group": "vault.crd.gocardless.com",
				},
			},
		},
	}

	svr, err := webhook.NewServer("vault", mgr, opts)
	if err != nil {
		app.Fatalf("failed to create admission server: %v", err)
	}

	injectorOpts := envconsul.InjectorOptions{
		Image:          *theatreImage,
		InstallPath:    *installPath,
		NamespaceLabel: *namespaceLabel,
		VaultConfigMapKey: client.ObjectKey{
			Namespace: *vaultConfigMapNamespace,
			Name:      *vaultConfigMapName,
		},
		ServiceAccountTokenFile:     *serviceAccountTokenFile,
		ServiceAccountTokenExpiry:   *serviceAccountTokenExpiry,
		ServiceAccountTokenAudience: *serviceAccountTokenAudience,
	}

	var wh *admission.Webhook
	if wh, err = envconsul.NewWebhook(logger, mgr, injectorOpts); err != nil {
		app.Fatalf(err.Error())
	}

	if err := svr.Register(wh); err != nil {
		app.Fatalf(err.Error())
	}

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}

func versionStanza() string {
	return fmt.Sprintf(
		"Version: %v\nGit SHA: %v\nGo Version: %v\nGo OS/Arch: %v/%v\nBuilt at: %v",
		Version, Commit, GoVersion, runtime.GOOS, runtime.GOARCH, Date,
	)
}
