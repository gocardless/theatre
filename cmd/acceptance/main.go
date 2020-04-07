package main

import (
	"bytes"
	"fmt"
	stdlog "log"
	"os"
	"os/exec"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gocardless/theatre/pkg/signals"

	theatreEnvconsulAcceptance "github.com/gocardless/theatre/cmd/theatre-envconsul/acceptance"
	consoleAcceptance "github.com/gocardless/theatre/pkg/workloads/console/acceptance"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
)

var (
	app         = kingpin.New("acceptance", "Acceptance test suite for theatre").Version("0.0.0")
	clusterName = app.Flag("cluster-name", "Name of Kubernetes context to against").Default("e2e").String()
	logger      = kitlog.NewLogfmtLogger(os.Stderr)

	prepare           = app.Command("prepare", "Creates test Kubernetes cluster and other resources")
	prepareImage      = prepare.Flag("image", "Docker image tag used for exchanging test images").Default("theatre:latest").String()
	prepareConfigFile = prepare.Flag("config-file", "Path to Kind config file").Default("kind-e2e.yaml").ExistingFile()
	prepareDockerfile = prepare.Flag("dockerfile", "Path to acceptance dockerfile").Default("Dockerfile").ExistingFile()

	destroy = app.Command("destroy", "Destroys the test Kubernetes cluster and other resources")

	run        = app.Command("run", "Runs the acceptance test suite")
	runTargets = run.Flag("target", "Run acceptance tests where the name contains these substrings").Strings()
	runVerbose = run.Flag("verbose", "Use the verbose reporter").Short('v').Bool()
)

// Runners contains all the acceptance test suites within theatre. Adding your runner here
// will ensure the acceptance binary prepares and runs tests appropriately.
//
// In future, we'll make this more ginkgo native. For now, this will do.
var Runners = []runner{
	&theatreEnvconsulAcceptance.Runner{},
	&consoleAcceptance.Runner{},
}

type runner interface {
	Name() string
	Prepare(kitlog.Logger, *rest.Config) error
	Run(kitlog.Logger, *rest.Config)
}

func main() {
	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	logger = level.NewFilter(logger, level.AllowInfo())
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", kitlog.DefaultCaller)
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))

	switch kingpin.MustParse(app.Parse(os.Args[1:])) {
	case prepare.FullCommand():
		logger = kitlog.With(logger, "clusterName", *clusterName)

		clusters, err := exec.CommandContext(ctx, "kind", "get", "clusters").CombinedOutput()
		if err != nil {
			app.Fatalf("failed to create kubernetes cluster with kind: %v", err)
		}

		if !strings.Contains(string(clusters), fmt.Sprintf("%s\n", *clusterName)) {
			logger.Log("msg", "creating new cluster")
			if err = pipeOutput(exec.CommandContext(ctx, "kind", "create", "cluster", "--name", *clusterName, "--config", *prepareConfigFile)).Run(); err != nil {
				app.Fatalf("failed to create kubernetes cluster with kind: %v", err)
			}
		}

		controlPlaneIDBytes, err := exec.CommandContext(
			ctx, "docker", "ps", "--filter", fmt.Sprintf("name=%s-control-plane", *clusterName), "--format", "{{.ID}}",
		).Output()
		controlPlaneID := string(bytes.TrimSpace(controlPlaneIDBytes))
		if controlPlaneID == "" || err != nil {
			app.Fatalf("failed to find control plane container: %v", err)
		}

		logger.Log("msg", "preparing acceptance docker image")
		buildCmd := exec.CommandContext(ctx, "docker", "build", "-t", *prepareImage, "-f", *prepareDockerfile, path.Dir(*prepareDockerfile))

		if err := pipeOutput(buildCmd).Run(); err != nil {
			app.Fatalf("failed to build acceptance docker image: %v", err)
		}

		logger.Log("msg", "loading docker image into control plane", "controlPlane", controlPlaneID)
		loadCmd := exec.CommandContext(ctx, "kind", "load", "docker-image", "--name", *clusterName, *prepareImage)
		if err := pipeOutput(loadCmd).Run(); err != nil {
			app.Fatalf("failed to load image into control plane: %v", err)
		}

		logger.Log("msg", "generating installation manifests")
		manifests, err := exec.CommandContext(ctx, "kustomize", "build", "config/overlays/acceptance").Output()
		if err != nil {
			app.Fatalf("failed to kustomize installation: %v", err)
		}

		logger.Log("msg", "installing manager into cluster")
		applyCmd := exec.CommandContext(ctx, "kubectl", "--context", fmt.Sprintf("kind-%s", *clusterName), "apply", "-f", "-")
		applyCmd.Stdin = bytes.NewReader(manifests)

		if err := pipeOutput(applyCmd).Run(); err != nil {
			app.Fatalf("failed to install manager: %v", err)
		}

		config := mustClusterConfig()
		for _, runner := range Runners {
			logger.Log("msg", "running prepare", "runner", reflect.TypeOf(runner).Elem().Name())
			if err := runner.Prepare(logger, config); err != nil {
				app.Fatalf("failed to execute runner prepare: %v", err)
			}
		}

	case destroy.FullCommand():
		logger = kitlog.With(logger, "clusterName", *clusterName)

		_, err := exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", *clusterName).CombinedOutput()
		if err != nil {
			app.Fatalf("failed to destroy kubernetes cluster with kind: %v", err)
		}

		logger.Log("msg", "successfully destroyed cluster")

	case run.FullCommand():
		RegisterFailHandler(Fail)

		SetDefaultEventuallyTimeout(time.Minute)
		SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
		if *runVerbose {
			config.DefaultReporterConfig.Verbose = true
		}

		logger := kitlog.NewLogfmtLogger(GinkgoWriter)
		config := mustClusterConfig()

		var _ = Describe("Acceptance", func() {
			for _, runner := range Runners {
				// Provide suite filtering while we don't use native ginkgo
				var shouldRun = len(*runTargets) == 0
				for _, target := range *runTargets {
					if strings.Contains(runner.Name(), target) {
						shouldRun = true
					}
				}

				if shouldRun {
					runner.Run(logger, config)
				}
			}
		})

		if RunSpecs(new(testing.T), "theatre/cmd/acceptance") {
			os.Exit(0)
		} else {
			os.Exit(1)
		}
	}
}

func mustClusterConfig() *rest.Config {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{
			CurrentContext: fmt.Sprintf("kind-%s", *clusterName),
		},
	).ClientConfig()
	if err != nil {
		app.Fatalf("failed to authenticate against kind cluster", err)
	}

	return config
}

func pipeOutput(cmd *exec.Cmd) *exec.Cmd {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}
