package main

import (
	"context"
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/gocardless/theatre/cmd"
	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	theatre "github.com/gocardless/theatre/pkg/client/clientset/versioned"
	"github.com/gocardless/theatre/pkg/logging"
	"github.com/gocardless/theatre/pkg/signals"
	"github.com/gocardless/theatre/pkg/workloads/console/runner"
)

var (
	cli = kingpin.New("consoles", "Manages theatre consoles").Version(cmd.VersionStanza())

	cliContext = cli.Flag("context", "Kubernetes context to target. If not provided defaults to current context").
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
	createAttach = create.Flag("attach", "Attach to the console if it starts successfully").
			Bool()
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

	authorise     = cli.Command("authorise", "Authorise a peer-reviewed console request")
	authoriseUser = authorise.Flag("user", "Name of the user to attribute to verification. This must match the username that the Kubernetes API recognises you as").
			String()
	authoriseName = authorise.Flag("name", "Console to authorise").
			Required().
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

	if err := Run(ctx, logger); !errors.Is(err, context.Canceled) {
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
		_, err = consoleRunner.Create(
			ctx,
			runner.CreateOptions{
				Namespace:  *cliNamespace,
				Selector:   *createSelector,
				Timeout:    *createTimeout,
				Reason:     *createReason,
				Command:    *createCommand,
				Attach:     *createAttach,
				KubeConfig: config,
				IO: runner.IOStreams{
					In:     os.Stdin,
					Out:    os.Stdout,
					ErrOut: os.Stderr,
				},
				Hook: LifecyclePrinter(logger),
			},
		)
		return err
	case attach.FullCommand():
		return consoleRunner.Attach(
			ctx,
			runner.AttachOptions{
				Namespace:  *cliNamespace,
				KubeConfig: config,
				Name:       *attachName,
				IO: runner.IOStreams{
					In:     os.Stdin,
					Out:    os.Stdout,
					ErrOut: os.Stderr,
				},
				Hook: LifecyclePrinter(logger),
			},
		)
	case list.FullCommand():
		_, err = consoleRunner.List(
			ctx,
			runner.ListOptions{
				Namespace: *cliNamespace,
				Username:  *listUsername,
				Selector:  *listSelector,
				Output:    os.Stdout,
			},
		)
		return err
	case authorise.FullCommand():
		err = consoleRunner.Authorise(
			ctx,
			runner.AuthoriseOptions{
				Namespace:   *cliNamespace,
				ConsoleName: *authoriseName,
				Username:    *authoriseUser,
			},
		)
	}

	return nil
}

// LifecyclePrinter hooks into console lifecycle events,
// reporting on the change of console phases during creation or attaching
func LifecyclePrinter(logger kitlog.Logger) runner.LifecycleHook {
	return runner.DefaultLifecycleHook{
		AttachingToPodFunc: func(csl *workloadsv1alpha1.Console) error {
			logger.Log(
				"msg", "Attaching to pod",
				"console", csl.Name,
				"namespace", csl.Namespace,
				"pod", csl.Status.PodName,
			)
			return nil
		},
		ConsoleRequiresAuthorisationFunc: func(csl *workloadsv1alpha1.Console, rule *workloadsv1alpha1.ConsoleAuthorisationRule) error {
			authoriserSlice := make([]string, 0, len(rule.ConsoleAuthorisers.Subjects))
			for _, authoriser := range rule.ConsoleAuthorisers.Subjects {
				authoriserSlice = append(authoriserSlice, authoriser.Kind+":"+authoriser.Name)
			}
			authorisers := strings.Join(authoriserSlice, ",")

			logger.Log(
				"msg", "Console requires authorisation",
				"prompt", fmt.Sprintf("Please get a user from the list of authorisers to approve by running `theatre-consoles authorise --name %s --namespace %s --username {THEIR_USERNAME}`", csl.Name, csl.Namespace),
				"authorisers", authorisers,
				"console", csl.Name,
				"namespace", csl.Namespace,
				"pod", csl.Status.PodName,
			)
			return nil
		},
		ConsoleReadyFunc: func(csl *workloadsv1alpha1.Console) error {
			logger.Log(
				"msg", "Console is ready",
				"console", csl.Name,
				"namespace", csl.Namespace,
				"pod", csl.Status.PodName,
			)
			return nil
		},
		ConsoleCreatedFunc: func(csl *workloadsv1alpha1.Console) error {
			logger.Log(
				"msg", "Console has been requested",
				"console", csl.Name,
				"namespace", csl.Namespace,
			)
			return nil
		},
	}
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
