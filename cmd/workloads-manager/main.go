package main

import (
	stdlog "log"
	"os"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	level "github.com/go-kit/kit/log/level"

	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/klog"

	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/gocardless/theatre/pkg/apis"
	"github.com/gocardless/theatre/pkg/apis/workloads"
	"github.com/gocardless/theatre/pkg/logging"
	"github.com/gocardless/theatre/pkg/signals"
	"github.com/gocardless/theatre/pkg/workloads/console"
)

var (
	app         = kingpin.New("workloads-manager", "Manages workloads.crd.gocardless.com resources").Version(Version)
	webhookName = app.Flag("webhook-name", "Kubernetes mutating webhook name").Default("theatre-workloads").String()
	namespace   = app.Flag("namespace", "Kubernetes webhook service namespace").Default("theatre-system").String()
	serviceName = app.Flag("service-name", "Kubernetes webhook service name").Default("theatre-workloads-manager").String()

	logger = kitlog.NewLogfmtLogger(os.Stderr)

	// Version is set at compile time
	Version = "dev"
)

func init() {
	logger = level.NewFilter(logger, level.AllowInfo())
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", logging.RecorderAwareCaller())
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
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
		CertDir: "/tmp/theatre-workloads",
		BootstrapOptions: &webhook.BootstrapOptions{
			MutatingWebhookConfigName: *webhookName,
			Service: &webhook.Service{
				Namespace: *namespace,
				Name:      *serviceName,
				Selectors: map[string]string{
					"app":   "theatre",
					"group": workloads.GroupName,
				},
			},
		},
		Port: 8443,
	}

	svr, err := webhook.NewServer("workloads", mgr, opts)
	if err != nil {
		app.Fatalf("failed to create admission server: %v", err)
	}

	// Console controller
	if _, err = console.Add(ctx, logger, mgr); err != nil {
		app.Fatalf(err.Error())
	}

	// Console webhook
	var wh *admission.Webhook
	if wh, err = console.NewWebhook(logger, mgr); err != nil {
		app.Fatalf(err.Error())
	}

	if err := svr.Register(wh); err != nil {
		app.Fatalf(err.Error())
	}

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}
