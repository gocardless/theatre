package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	rbacv1alpha1 "github.com/gocardless/theatre/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/cmd"
	consolecontroller "github.com/gocardless/theatre/controllers/workloads/console"
	"github.com/gocardless/theatre/pkg/signals"
)

var (
	scheme = runtime.NewScheme()

	app = kingpin.New("workloads-manager", "Manages workloads.crd.gocardless.com resources").Version(cmd.VersionStanza())

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
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("console"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(ctx, mgr); err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	// console authenticator webhook
	mgr.GetWebhookServer().Register("/mutate-consoles", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthenticatorWebhook(
			logger.WithName("webhooks").WithName("console-authenticator"),
		),
	})

	// console authorisation webhook
	mgr.GetWebhookServer().Register("/validate-consoleauthorisations", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthorisationWebhook(
			mgr.GetClient(),
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

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}
