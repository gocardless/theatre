package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	vaultv1alpha1 "github.com/gocardless/theatre/v3/apis/vault/v1alpha1"
	"github.com/gocardless/theatre/v3/cmd"
	"github.com/gocardless/theatre/v3/pkg/signals"
)

var (
	app = kingpin.New("vault-manager", "Manages vault.crd.gocardless.com resources").Version(cmd.VersionStanza())

	commonOpts = cmd.NewCommonOptions(app).WithMetrics(app)

	namespace               = app.Flag("namespace", "Kubernetes webhook service namespace").Default("theatre-system").String()
	serviceName             = app.Flag("service-name", "Name of service for webhook").Default("theatre-vault-manager").String()
	webhookName             = app.Flag("webhook-name", "Name of webhook").Default("theatre-vault").String()
	theatreImage            = app.Flag("theatre-image", "Set to the same image as current binary").Required().String()
	installPath             = app.Flag("install-path", "Location to install theatre binaries").Default("/var/run/theatre").String()
	namespaceLabel          = app.Flag("namespace-label", "Namespace label that enables webhook to operate on").Default("theatre-secrets-injector").String()
	vaultConfigMapName      = app.Flag("vault-configmap-name", "Vault configMap name containing vault configuration").Default("vault-config").String()
	vaultConfigMapNamespace = app.Flag("vault-configmap-namespace", "Namespace of vault configMap").Default("vault-system").String()

	theatreSecretsTimeout   = app.Flag("theatre-secrets-timeout", "Timeout that theatre-secrets should use when communicating with Vault").Default("10s").Duration()
	theatreSecretsDebugMode = app.Flag("theatre-secrets-debug", "Whether enable debug mode within theatre-secrets").Default("false").Bool()

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
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		MetricsBindAddress: fmt.Sprintf("%s:%d", commonOpts.MetricAddress, commonOpts.MetricPort),
		Port:               443,
		LeaderElection:     commonOpts.ManagerLeaderElection,
		LeaderElectionID:   "vault.crds.gocardless.com",
	})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	injectorOpts := vaultv1alpha1.SecretsInjectorOptions{
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
		Timeout:                     *theatreSecretsTimeout,
		Debug:                       *theatreSecretsDebugMode,
	}

	mgr.GetWebhookServer().Register("/mutate-pods", &admission.Webhook{
		Handler: vaultv1alpha1.NewSecretsInjector(
			mgr.GetClient(),
			logger.WithName("webhooks").WithName("secrets-injector"),
			injectorOpts,
		),
	})

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}
