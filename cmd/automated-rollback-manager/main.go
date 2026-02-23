package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/cmd"

	"github.com/gocardless/theatre/v5/pkg/signals"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	automatedrollbackcontroller "github.com/gocardless/theatre/v5/internal/controller/deploy"
)

var (
	scheme        = runtime.NewScheme()
	app           = kingpin.New("automated-rollback-manager", "Creates rollback resources based on release status and rollback policies").Version(cmd.VersionStanza())
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

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		LeaderElection:   commonOptions.ManagerLeaderElection,
		LeaderElectionID: "automated-rollback-policy.deploy.crd.gocardless.com",
		Scheme:           scheme,
		Metrics: metricsserver.Options{
			BindAddress: fmt.Sprintf("%s:%d", commonOptions.MetricAddress, commonOptions.MetricPort),
		},
	})

	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	if err = (&automatedrollbackcontroller.AutomatedRollbackReconciler{
		Client: manager.GetClient(),
		Scheme: scheme,
		Log:    logger,
	}).SetupWithManager(ctx, manager); err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	if err := manager.Start(ctx); err != nil {
		app.Fatalf("failed to start manager: %v", err)
	}
}
