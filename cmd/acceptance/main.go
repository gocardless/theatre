package main

import (
	"bytes"
	"context"
	"fmt"
	stdlog "log"
	"os"
	"os/exec"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/gocardless/theatre/v4/pkg/signals"

	vaultManagerAcceptance "github.com/gocardless/theatre/v4/cmd/vault-manager/acceptance"
	workloadsManagerAcceptance "github.com/gocardless/theatre/v4/cmd/workloads-manager/acceptance"
)

var (
	app         = kingpin.New("acceptance", "Acceptance test suite for theatre").Version("0.0.0")
	clusterName = app.Flag("cluster-name", "Name of Kubernetes context to against").Default("e2e").String()
	logger      = kitlog.NewLogfmtLogger(os.Stderr)

	prepare              = app.Command("prepare", "Creates test Kubernetes cluster and other resources")
	prepareImage         = prepare.Flag("image", "Docker image tag used for exchanging test images").Default("theatre:latest").String()
	prepareConfigFile    = prepare.Flag("config-file", "Path to Kind config file").Default("kind-e2e.yaml").ExistingFile()
	prepareDockerfile    = prepare.Flag("dockerfile", "Path to acceptance dockerfile").Default("Dockerfile").ExistingFile()
	prepareKindNodeImage = prepare.Flag("kind-node-image", "Kind Node Image").Default("kindest/node:v1.27.3").String()
	prepareVerbose       = prepare.Flag("verbose", "Use a higher log level when creating the cluster").Short('v').Bool()

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
	&vaultManagerAcceptance.Runner{},
	&workloadsManagerAcceptance.Runner{},
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

			// Kind uses Klog, so should be following these levels:
			// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md
			logLevel := 1
			if *prepareVerbose {
				logLevel = 5
			}

			if err = pipeOutput(exec.CommandContext(ctx,
				"kind", "create", "cluster", "--name", *clusterName,
				"--config", *prepareConfigFile, "--image", *prepareKindNodeImage,
				"--verbosity", fmt.Sprintf("%d", logLevel))).Run(); err != nil {
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

		logger.Log("msg", "generating setup manifests")
		setupManifests, err := exec.CommandContext(ctx, "kustomize", "build", "config/acceptance/setup").Output()
		if err != nil {
			app.Fatalf("failed to kustomize setup manifests: %v", err)
		}

		logger.Log("msg", "installing setup resources into cluster")
		applyCmd := exec.CommandContext(ctx, "kubectl", "--context", fmt.Sprintf("kind-%s", *clusterName), "apply", "-f", "-")
		applyCmd.Stdin = bytes.NewReader(setupManifests)

		if err := pipeOutput(applyCmd).Run(); err != nil {
			app.Fatalf("failed to install setup manifests into cluster: %v", err)
		}

		logger.Log("msg", "waiting for setup resources to run")
		contextTimeout := 3 * time.Minute
		ctx, deadline := context.WithTimeout(ctx, contextTimeout)
		defer deadline()

		// Wait for Deployments
		// We do this to guard against a race condition where, if you only have the "wait for
		// pods" check below, but the controller hasn't yet actually *spawned* any pods for
		// deployments, then you can proceed with the preparation when the cluster isn't in a
		// good state.
		// The most notable issue is cert-manager; if the pods aren't up, and therefore
		// serving webhooks, then subsequently the installation of any controllers which have
		// webhooks, and therefore require a certificate, will fail.
		waitCmd := exec.CommandContext(ctx, "kubectl", "--context", fmt.Sprintf("kind-%s", *clusterName), "wait", "--all-namespaces", "--for", "condition=Available", "deployments", "--all", "--timeout", "2m")
		if err := pipeOutput(waitCmd).Run(); err != nil {
			app.Fatalf("not all setup resources are running: %v", err)
		}
		// Pods - covers those created by Statefulsets
		waitCmd = exec.CommandContext(ctx, "kubectl", "--context", fmt.Sprintf("kind-%s", *clusterName), "wait", "--all-namespaces", "--for", "condition=Ready", "pods", "--all", "--timeout", "2m")
		if err := pipeOutput(waitCmd).Run(); err != nil {
			app.Fatalf("not all setup resources are running: %v", err)
		}

		if ctx.Err() == context.DeadlineExceeded {
			app.Fatalf("context deadline: no all setup resources are running: %v", err)
		}

		logger.Log("msg", "generating theatre manifests")
		manifests, err := exec.CommandContext(ctx, "kustomize", "build", "config/acceptance").Output()
		if err != nil {
			app.Fatalf("failed to kustomize theatre manifests: %v", err)
		}

		logger.Log("msg", "installing theatre into cluster")
		applyCmd = exec.CommandContext(ctx, "kubectl", "--context", fmt.Sprintf("kind-%s", *clusterName), "apply", "-f", "-")
		applyCmd.Stdin = bytes.NewReader(manifests)

		if err := pipeOutput(applyCmd).Run(); err != nil {
			app.Fatalf("failed to install theatre manifests into cluster: %v", err)
		}

		logger.Log("msg", "waiting for Theatre resources are running")
		ctx, deadline = context.WithTimeout(ctx, contextTimeout)
		defer deadline()
		waitCmd = exec.CommandContext(ctx, "kubectl", "--context", fmt.Sprintf("kind-%s", *clusterName), "wait", "--all-namespaces", "--for", "condition=Ready", "pods", "--all", "--timeout", "2m")

		if err := pipeOutput(waitCmd).Run(); err != nil {
			app.Fatalf("not all theatre resources are running: %v", err)
		}

		if ctx.Err() == context.DeadlineExceeded {
			app.Fatalf("context deadline: no all theatre resources are running: %v", err)
		}

		cfg := mustClusterConfig()

		for _, runner := range Runners {
			logger.Log("msg", "running prepare", "runner", reflect.TypeOf(runner).Elem().Name())
			if err := runner.Prepare(logger, cfg); err != nil {
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
		cfg := mustClusterConfig()

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
					runner.Run(logger, cfg)
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
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{
			CurrentContext: fmt.Sprintf("kind-%s", *clusterName),
		},
	).ClientConfig()
	if err != nil {
		app.Fatalf("failed to authenticate against kind cluster", err)
	}

	return cfg
}

func pipeOutput(cmd *exec.Cmd) *exec.Cmd {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}
