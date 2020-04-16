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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

var (
	cli        = kingpin.New("consoles", "Manages theatre consoles")
	cliContext = cli.Flag("context", "Kubernetes context to target. If not provided defaults to current context").
			Short('c').
			Envar("KUBERNETES_CONTEXT").
			String()
	cliNamespace = cli.Flag("namespace", "Kubernetes namespace to target. If not provided defaults to target allnamespaces").
			Short('n').
			Envar("KUBERNETES_NAMESPACE").
			String()

	create         = cli.Command("create", "Creates a new console given a template")
	createSelector = create.Flag("selector", "Selector to match a console template").
			Short('s').
			Required().
			String()
	createTimeout = create.Flag("timeout", "Timeout for the new console").
			Duration()
	createReason = create.Flag("reason", "Reason for creating console").
			String()
	createCommand = create.Arg("command", "Command to run in console").
			Strings()

	attach     = cli.Command("attach", "Attach to a running console")
	attachName = attach.Flag("name", "Console name").
			Required().
			String()

	list         = cli.Command("list", "List currently running consoles")
	listUsername = list.Flag("user", "Kubernetes username. Not usually supplied, can be inferred from your gcloud login").
			Short('u').
			Default("").
			String()
	listSelector = list.Flag("selector", "Selector to match the console").
			Short('s').
			Default("").
			String()
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

// Run is the entrypoint for the cli application, after housekeeping tasks has been finished,
// e.g. setting up logging.
func Run(ctx context.Context, logger kitlog.Logger) error {
	// Parse application args using kingpin
	// This is done here to bind the flags without creating multiple global variables.
	cmd := kingpin.MustParse(cli.Parse(os.Args[1:]))

	config, err := newKubeConfig(*cliContext)
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

	consoleRunner := runner.New(client, theatreClient)

	// Match on the kingpin command and enter the main command
	switch cmd {
	case create.FullCommand():
		return Create(
			ctx, logger, consoleRunner,
			CreateOptions{
				Namespace: *cliNamespace,
				Selector:  *createSelector,
				Timeout:   *createTimeout,
				Reason:    *createReason,
				Command:   *createCommand,
			},
		)
	case attach.FullCommand():
		return Attach(
			ctx, logger, consoleRunner,
			AttachOptions{
				Namespace: *cliNamespace,
				Client:    client,
				Config:    config,
				Name:      *attachName,
			},
		)
	case list.FullCommand():
		return List(
			ctx, logger, consoleRunner,
			ListOptions{
				Namespace: *cliNamespace,
				Username:  *listUsername,
				Selector:  *listSelector,
			},
		)
	}

	return nil
}

// newKubeConfig first tries using internal kubernetes configuration, and then falls back
// to ~/.kube/config
func newKubeConfig(kctx string) (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{
			CurrentContext: kctx,
		},
	).ClientConfig()

	return config, err
}
