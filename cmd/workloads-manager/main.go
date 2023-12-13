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
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	rbacv1alpha1 "github.com/gocardless/theatre/v4/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/v4/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/v4/cmd"
	consolecontroller "github.com/gocardless/theatre/v4/controllers/workloads/console"
	"github.com/gocardless/theatre/v4/pkg/signals"
	"github.com/gocardless/theatre/v4/pkg/workloads/console/events"
)

var (
	scheme = runtime.NewScheme()

	app                        = kingpin.New("workloads-manager", "Manages workloads.crd.gocardless.com resources").Version(cmd.VersionStanza())
	contextName                = app.Flag("context-name", "Distinct name for the context this controller runs within. Usually the user-facing name of the kubernetes context for the cluster").Envar("CONTEXT_NAME").String()
	pubsubProjectId            = app.Flag("pubsub-project-id", "ID for the project containing the Pub/Sub topic for console event publishing").Envar("PUBSUB_PROJECT_ID").String()
	pubsubTopicId              = app.Flag("pubsub-topic-id", "ID of the topic to publish lifecycle event messages").Envar("PUBSUB_TOPIC_ID").String()
	enableDirectoryRoleBinding = app.Flag("directory-role-binding", "Use DirectoryRoleBinding for provisioning RBAC against console objects").Envar("ENABLE_DIRECTORY_ROLE_BINDING").Default("true").Bool()
	enableSessionRecording     = app.Flag("session-recording", "Enable session recording features").Envar("ENABLE_SESSION_RECORDING").Default("false").Bool()
	sessionSidecarImage        = app.Flag("session-sidecar-image", "Container image to use for the session recording sidecar container").Envar("SESSION_SIDECAR_IMAGE").Default("").String()
	sessionPubsubProjectId     = app.Flag("session-pubsub-project-id", "ID for the project containing the Pub/Sub topic for session recording").Envar("SESSION_PUBSUB_PROJECT_ID").Default("").String()
	sessionPubsubTopicId       = app.Flag("session-pubsub-topic-id", "ID of the topic to publish session recording data to").Envar("SESSION_PUBSUB_TOPIC_ID").Default("").String()

	commonOpts = cmd.NewCommonOptions(app).WithMetrics(app)
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = workloadsv1alpha1.AddToScheme(scheme)
	_ = rbacv1alpha1.AddToScheme(scheme)

	// Register custom metrics with the global controller runtime prometheus registry
	metrics.Registry.MustRegister(cmd.BuildInfo)
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	// Check flag validity for session recording
	if *enableSessionRecording {
		if len(*sessionSidecarImage) == 0 {
			app.Fatalf("Session recording sidecar image parameter must be set")
		}
		if len(*sessionPubsubProjectId) == 0 {
			app.Fatalf("Session recording Google project ID must be set")
		}
		if len(*sessionPubsubTopicId) == 0 {
			app.Fatalf("Session recording Google pubsub ID must be set")
		}
	}

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
	idBuilder := workloadsv1alpha1.NewConsoleIdBuilder(*contextName)
	lifecycleRecorder := workloadsv1alpha1.NewLifecycleEventRecorder(*contextName, logger, publisher, idBuilder)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Metrics:          metricsserver.Options{BindAddress: fmt.Sprintf("%s:%d", commonOpts.MetricAddress, commonOpts.MetricPort)},
		LeaderElection:   commonOpts.ManagerLeaderElection,
		LeaderElectionID: "workloads.crds.gocardless.com",
		Scheme:           scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: 443,
		}),
	})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	// controller
	if err = (&consolecontroller.ConsoleReconciler{
		Client:                     mgr.GetClient(),
		LifecycleRecorder:          lifecycleRecorder,
		ConsoleIdBuilder:           idBuilder,
		Log:                        ctrl.Log.WithName("controllers").WithName("console"),
		Scheme:                     mgr.GetScheme(),
		EnableDirectoryRoleBinding: *enableDirectoryRoleBinding,
		EnableSessionRecording:     *enableSessionRecording,
		SessionSidecarImage:        *sessionSidecarImage,
		SessionPubsubProjectId:     *sessionPubsubProjectId,
		SessionPubsubTopicId:       *sessionPubsubTopicId,
	}).SetupWithManager(ctx, mgr); err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	// NOTE: We may want to simplify the implementation of webhooks, like this:
	// https://book.kubebuilder.io/cronjob-tutorial/webhook-implementation
	// Currently there's a lot of boilerplate/wiring up, which isn't really necessary.

	// console authenticator webhook
	mgr.GetWebhookServer().Register("/mutate-consoles", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthenticatorWebhook(
			lifecycleRecorder,
			logger.WithName("webhooks").WithName("console-authenticator"),
			mgr.GetScheme(),
		),
	})

	// console authorisation webhook
	mgr.GetWebhookServer().Register("/validate-consoleauthorisations", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthorisationWebhook(
			mgr.GetClient(),
			lifecycleRecorder,
			logger.WithName("webhooks").WithName("console-authorisation"),
			mgr.GetScheme(),
		),
	})

	// console template webhook
	mgr.GetWebhookServer().Register("/validate-consoletemplates", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleTemplateValidationWebhook(
			logger.WithName("webhooks").WithName("console-template"),
			mgr.GetScheme(),
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
			mgr.GetScheme(),
		),
	})

	if err := mgr.Start(ctx); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}
