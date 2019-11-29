package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"

	"k8s.io/apimachinery/pkg/types"
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

var (
	app                     = kingpin.New("vault-manager", "Manages vault.crd.gocardless.com resources").Version(Version)
	namespace               = app.Flag("namespace", "Kubernetes webhook service namespace").Default("theatre-system").String()
	serviceName             = app.Flag("service-name", "Webhook service name").Default("theatre-vault-manager").String()
	webhookName             = app.Flag("webhook-name", "Mutating webhook name").Default("theatre-vault").String()
	theatreImage            = app.Flag("theatre-image", "Set to the same image as current binary").Required().String()
	installPath             = app.Flag("install-path", "Location to install theatre binaries").Default("/var/run/theatre").String()
	vaultConfigMapName      = app.Flag("vault-configmap-name", "Vault configMap name containing vault configuration").Default("vault-config").String()
	vaultConfigMapNamespace = app.Flag("vault-configmap-namespace", "Namespace of vault configMap").Default("vault-system").String()

	commonOpts = cmd.NewCommonOptions(app)

	// Version is set at compile time
	Version = "dev"
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	if err := apis.AddToScheme(scheme.Scheme); err != nil {
		app.Fatalf("failed to add schemes: %v", err)
	}

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	opts := webhook.ServerOptions{
		CertDir: "/tmp/theatre-vault",
		BootstrapOptions: &webhook.BootstrapOptions{
			Secret: &types.NamespacedName{
				Namespace: *namespace,
				Name:      fmt.Sprintf("%s-ca", *serviceName),
			},
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
		Image:       *theatreImage,
		InstallPath: *installPath,
		VaultConfigMapKey: client.ObjectKey{
			Namespace: *vaultConfigMapNamespace,
			Name:      *vaultConfigMapName,
		},
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
