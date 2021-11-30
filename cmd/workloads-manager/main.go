package main

import (
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kingpin"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	rbacv1alpha1 "github.com/gocardless/theatre/v3/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/v3/cmd"
	consolecontroller "github.com/gocardless/theatre/v3/controllers/workloads/console"
	"github.com/gocardless/theatre/v3/pkg/signals"
	"github.com/gocardless/theatre/v3/pkg/workloads/console/events"
)

var (
	scheme = runtime.NewScheme()

	app             = kingpin.New("workloads-manager", "Manages workloads.crd.gocardless.com resources").Version(cmd.VersionStanza())
	contextName     = app.Flag("context-name", "Distinct name for the context this controller runs within. Usually the user-facing name of the kubernetes context for the cluster").Envar("CONTEXT_NAME").String()
	pubsubProjectId = app.Flag("pubsub-project-id", "ID for the project containing the Pub/Sub topic for console event publishing").Envar("PUBSUB_PROJECT_ID").String()
	pubsubTopicId   = app.Flag("pubsub-topic-id", "ID of the topic to publish lifecycle event messages").Envar("PUBSUB_TOPIC_ID").String()

	commonOpts = cmd.NewCommonOptions(app).WithMetrics(app)
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = workloadsv1alpha1.AddToScheme(scheme)
	_ = rbacv1alpha1.AddToScheme(scheme)
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	// Create publisher sink for console lifecycle events
	var publisher events.Publisher
	var err error
	if len(*pubsubProjectId) > 0 && len(*pubsubTopicId) > 0 {
		publisher, err = events.NewGooglePubSubPublisher(ctx, *pubsubProjectId, *pubsubTopicId)
		if err != nil {
			app.Fatalf("failed to create publisher for %s/%s", *pubsubProjectId, *pubsubTopicId)
		}
		defer publisher.(*events.GooglePubSubPublisher).Stop()
	} else { // Default to a nop publisher
		publisher = events.NewNopPublisher()
	}
	lifecycleRecorder := workloadsv1alpha1.NewLifecycleEventRecorder(*contextName, logger, publisher)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		MetricsBindAddress: fmt.Sprintf("%s:%d", commonOpts.MetricAddress, commonOpts.MetricPort),
		Port:               443,
		LeaderElection:     commonOpts.ManagerLeaderElection,
		LeaderElectionID:   "workloads.crds.gocardless.com",
		Scheme:             scheme,
	})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	// controller
	if err = (&consolecontroller.ConsoleReconciler{
		Client:            mgr.GetClient(),
		LifecycleRecorder: lifecycleRecorder,
		Log:               ctrl.Log.WithName("controllers").WithName("console"),
		Scheme:            mgr.GetScheme(),
	}).SetupWithManager(ctx, mgr); err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	// console authenticator webhook
	mgr.GetWebhookServer().Register("/mutate-consoles", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthenticatorWebhook(
			lifecycleRecorder,
			logger.WithName("webhooks").WithName("console-authenticator"),
		),
	})

	// console authorisation webhook
	mgr.GetWebhookServer().Register("/validate-consoleauthorisations", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthorisationWebhook(
			mgr.GetClient(),
			lifecycleRecorder,
			logger.WithName("webhooks").WithName("console-authorisation"),
		),
	})

	// console template webhook
	mgr.GetWebhookServer().Register("/validate-consoletemplates", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleTemplateValidationWebhook(
			logger.WithName("webhooks").WithName("console-template"),
		),
	})

	// priority webhook
	mgr.GetWebhookServer().Register("/mutate-pods", &admission.Webhook{
		Handler: workloadsv1alpha1.NewPriorityInjector(
			mgr.GetClient(),
			logger.WithName("webhooks").WithName("priority-injector"),
		),
	})

	// console attach webhook
	mgr.GetWebhookServer().Register("/observe-console-attach", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAttachObserverWebhook(
			mgr.GetClient(),
			mgr.GetEventRecorderFor("console-attach-observer"),
			lifecycleRecorder,
			logger.WithName("webhooks").WithName("console-attach-observer"),
			10*time.Second,
		),
	})

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}
