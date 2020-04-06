package main

import (
	"context"
	stdlog "log"
	"os"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	theatre "github.com/gocardless/theatre/pkg/client/clientset/versioned"
	"github.com/gocardless/theatre/pkg/logging"
	"github.com/gocardless/theatre/pkg/signals"
	"github.com/gocardless/theatre/pkg/workloads/console/runner"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/klog"
	kconf "sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	cli = kingpin.New("consoles", "Manages theatre consoles")

	create = cli.Command("create", "Creates a new console given a template")
)

func main() {
	// Set up logging
	logger := kitlog.NewLogfmtLogger(os.Stderr)
	logger = level.NewFilter(logger, level.AllowInfo())
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", logging.RecorderAwareCaller())
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))

	ctx, _ := signals.SetupSignalHandler()

	if err := Run(ctx, logger); err != nil {
		cli.Fatalf("unexpected error: %s", err)
	}
}

func Run(ctx context.Context, logger kitlog.Logger) error {
	config, err := kconf.GetConfig()
	if err != nil {
		return err
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	theatreClient, err := theatre.NewForConfig(config)
	if err != nil {
		return err
	}

	runner := runner.New(client, theatreClient)

	switch kingpin.MustParse(cli.Parse(os.Args[1:])) {
	case create.FullCommand():
		return Create(ctx, logger, runner)
	}

	return nil
}
