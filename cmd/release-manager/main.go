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
	scheme                          = runtime.NewScheme()
	app                             = kingpin.New("release-manager", "Manages release.deploy.crd.gocardless.com resources").Version(cmd.VersionStanza())
	enableReleaseUniquenessWebhooks = app.Flag(
		"enable-release-uniqueness-webhooks",
		"Enable release uniqueness webhooks - when enabled, the release name will be set by the controller based on the"+
			" release.config object. Kubernetes will then handle the uniqueness of Release resources in the namespace.",
	).Default("true").Bool()
	maxReleasesPerTarget = app.Flag("max-releases-per-target-name", "Maximum number of releases to keep per target name."+
		" All releases older than this will be deleted by the reconciler. If set to -1, no releases will be deleted.").
		Default("10").
		Envar("RELEASE_MANAGER_MAX_RELEASES_PER_TARGET_NAME").
		Int()
	cullingStrategyFlag = app.Flag("culling-strategy", "Strategy to use when trimming releases. Options are"+
		" 'deployment-end-time' or 'signature'. 'deployment-end-time' will delete the oldest releases sorted by"+
		" status.deploymentEndTime and default to metadata.creationTimestamp if status.deploymentEndTime is not set,"+
		" while 'signature' will delete releases with the same signature first, before it deletes the oldest releases sorted by deploymentEndTime.").
		Default("deployment-end-time").
		Envar("RELEASE_MANAGER_CULLING_STRATEGY").
		String()
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
		LeaderElectionID: "release.deploy.crds.gocardless.com",
		Scheme:           scheme,
		Metrics: metricsserver.Options{
			BindAddress: fmt.Sprintf("%s:%d", commonOptions.MetricAddress, commonOptions.MetricPort),
		},
		WebhookServer: webhookServer,
	})

	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	cullingStrategy := releasecontroller.CullingStrategy(*cullingStrategyFlag)
	if cullingStrategy != releasecontroller.CullingStrategySignature {
		cullingStrategy = releasecontroller.CullingStrategyDeploymentEndTime
	}

	if err = (&releasecontroller.ReleaseReconciler{
		Client:               manager.GetClient(),
		Scheme:               scheme,
		Log:                  logger,
		MaxReleasesPerTarget: *maxReleasesPerTarget,
		CullingStrategy:      cullingStrategy,
	}).SetupWithManager(ctx, manager); err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	manager.GetWebhookServer().Register("/validate-releases", &admission.Webhook{
		Handler: releasewebhook.NewReleaseValidateWebhook(logger, manager.GetScheme()),
	})

	if *enableReleaseUniquenessWebhooks {
		// Webhook configuration
		manager.GetWebhookServer().Register("/mutate-releases", &admission.Webhook{
			Handler: releasewebhook.NewReleaseNamerWebhook(logger, manager.GetScheme()),
		})
	}

	if err := manager.Start(ctx); err != nil {
		app.Fatalf("failed to start manager: %v", err)
	}
}
