package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/cmd"

	rollbackcontroller "github.com/gocardless/theatre/v5/internal/controller/deploy"
	"github.com/gocardless/theatre/v5/pkg/signals"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	scheme               = runtime.NewScheme()
	app                  = kingpin.New("rollback-manager", "Manages deploy.crd.gocardless.com resources").Version(cmd.VersionStanza())
	rollbackHistoryLimit = app.Flag("rollback-history-limit", "Maximum number of rollbacks to keep per target name. All rollbacks older than this will be deleted by the reconciler.").
				Default("10").
				Envar("ROLLBACK_MANAGER_HISTORY_LIMIT").
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
		LeaderElectionID: "rollback.deploy.crds.gocardless.com",
		Scheme:           scheme,
		Metrics: metricsserver.Options{
			BindAddress: fmt.Sprintf("%s:%d", commonOptions.MetricAddress, commonOptions.MetricPort),
		},
		WebhookServer: webhookServer,
	})

	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	err = (&rollbackcontroller.RollbackReconciler{
		Client:               manager.GetClient(),
		Scheme:               scheme,
		Log:                  logger,
		RollbackHistoryLimit: *rollbackHistoryLimit,
	}).SetupWithManager(ctx, manager)

	if err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	if err := manager.Start(ctx); err != nil {
		app.Fatalf("failed to start manager: %v", err)
	}
}
