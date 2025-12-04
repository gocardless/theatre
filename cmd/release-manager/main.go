package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/cmd"

	releasewebhook "github.com/gocardless/theatre/v5/internal/webhook/deploy/v1alpha1/release"
	"github.com/gocardless/theatre/v5/pkg/signals"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	scheme                          = runtime.NewScheme()
	app                             = kingpin.New("release-manager", "Manages deploy.crd.gocardless.com resources").Version(cmd.VersionStanza())
	enableReleaseUniquenessWebhooks = app.Flag(
		"enable-release-uniqueness-webhooks",
		"Enable release uniqueness webhooks - when enabled, the release name will be set by the controller based on the"+
			" release.config field and validated for uniqueness across the namespace.",
	).Default("true").Bool()
	commonOptions = cmd.NewCommonOptions(app).WithMetrics(app)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(deployv1alpha1.AddToScheme(scheme))
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOptions.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	webhookOpts := webhook.Options{Port: 9443}
	webhookServer := webhook.NewServer(webhookOpts)

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		LeaderElection:   commonOptions.ManagerLeaderElection,
		LeaderElectionID: "deploy.crds.gocardless.com",
		Scheme:           scheme,
		Metrics: metricsserver.Options{
			BindAddress: fmt.Sprintf("%s:%d", commonOptions.MetricAddress, commonOptions.MetricPort),
		},
		WebhookServer: webhookServer,
	})

	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	if *enableReleaseUniquenessWebhooks {
		// Webhook configuration
		manager.GetWebhookServer().Register("/mutate-releases", &admission.Webhook{
			Handler: releasewebhook.NewReleaseNamerWebhook(logger, manager.GetScheme()),
		})

		manager.GetWebhookServer().Register("/validate-releases", &admission.Webhook{
			// Handler: releasewebhook.NewReleaseValidateWebhook(logger, manager.GetScheme()),
			Handler: nil,
		})
	}

	if err := manager.Start(ctx); err != nil {
		app.Fatalf("failed to start manager: %v", err)
	}
}
