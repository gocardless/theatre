package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/cmd"
	releasecontroller "github.com/gocardless/theatre/v5/internal/controller/deploy"
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
	scheme        = runtime.NewScheme()
	app           = kingpin.New("release-manager", "Manages deploy.crd.gocardless.com resources").Version(cmd.VersionStanza())
	scheme               = runtime.NewScheme()
	app                  = kingpin.New("release-manager", "Manages deploy.crd.gocardless.com resources").Version(cmd.VersionStanza())
	maxReleasesPerTarget = app.Flag("max-releases-per-target", "Maximum number of releases to keep per target. All releases older than this will be deleted by the reconciler.").
				Default("10").
				Envar("RELEASE_MANAGER_MAX_RELEASES_PER_TARGET").
				Int()
	maxHistoryLimit = app.Flag("max-history-limit", "Maximum number of status.history entries to keep per release. All history entries older than this will be deleted by the reconciler.").
			Default("50").
			Envar("RELEASE_MANAGER_MAX_HISTORY_LIMIT").
			Int()
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

	// Webhook configuration
	manager.GetWebhookServer().Register("/mutate-releases", &admission.Webhook{
		Handler: releasewebhook.NewReleaseNamerWebhook(logger, manager.GetScheme()),
	})

	manager.GetWebhookServer().Register("/validate-releases", &admission.Webhook{
		Handler: releasewebhook.NewReleaseValidateWebhook(logger, manager.GetScheme()),
	})

	if err = (&releasecontroller.ReleaseReconciler{
		Client:               manager.GetClient(),
		Scheme:               scheme,
		Log:                  logger,
		MaxReleasesPerTarget: *maxReleasesPerTarget,
		MaxHistoryLimit:      *maxHistoryLimit,
	}).SetupWithManager(ctx, manager); err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	if err := manager.Start(ctx); err != nil {
		app.Fatalf("failed to start manager: %v", err)
	}
}
