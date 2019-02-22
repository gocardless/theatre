package main

import (
	"bytes"
	"fmt"
	stdlog "log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"k8s.io/klog"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gocardless/theatre/pkg/signals"
	consoleAcceptance "github.com/gocardless/theatre/pkg/workloads/console/acceptance"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
)

var (
	app    = kingpin.New("acceptance", "Acceptance test suite for theatre").Version("0.0.0")
	logger = kitlog.NewLogfmtLogger(os.Stderr)

	prepare      = app.Command("prepare", "Creates test Kubernetes cluster and other resources")
	prepareName  = prepare.Flag("name", "Name of Kubernetes context to create").Default("e2e").String()
	prepareImage = prepare.Flag("image", "Docker image tag used for exchanging test images").Default("theatre:latest").String()
	prepareBin   = prepare.Flag("bin", "Path to manager binaries").Default("./bin").ExistingDir()

	destroy     = app.Command("destroy", "Destroys the test Kubernetes cluster and other resources")
	destroyName = destroy.Flag("name", "Name of Kubernetes context to destroy").Default("e2e").String()

	run         = app.Command("run", "Runs the acceptance test suite")
	runName     = run.Flag("name", "Name of Kubernetes context to against").Default("e2e").String()
	contextName = "e2e"
)

// AcceptanceDockerfile defines the docker instructions used to create an acceptance
// docker image which will be pushed into the acceptance test cluster.
const AcceptanceDockerfile = `
FROM alpine:3.8
RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*
COPY rbac-manager.linux_amd64 /rbac-manager
COPY workloads-manager.linux_amd64 /workloads-manager
`

func init() {
	logger = level.NewFilter(logger, level.AllowInfo())
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", kitlog.DefaultCaller)
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))
	app.HelpFlag.Short('h')
}

func main() {
	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	switch kingpin.MustParse(app.Parse(os.Args[1:])) {
	case prepare.FullCommand():
		logger = kitlog.With(logger, "clusterName", *prepareName)

		clusters, err := exec.CommandContext(ctx, "kind", "get", "clusters").CombinedOutput()
		if err != nil {
			app.Fatalf("failed to create kubernetes cluster with kind: %v", err)
		}

		if !strings.Contains(string(clusters), fmt.Sprintf("%s\n", *prepareName)) {
			logger.Log("msg", "creating new cluster")
			if err = pipeOutput(exec.CommandContext(ctx, "kind", "create", "cluster", "--name", *prepareName)).Run(); err != nil {
				app.Fatalf("failed to create kubernetes cluster with kind: %v", err)
			}
		}

		controlPlaneIDBytes, err := exec.CommandContext(
			ctx, "docker", "ps", "--filter", fmt.Sprintf("name=kind-%s-control-plane", *prepareName), "--format", "{{.ID}}",
		).Output()
		controlPlaneID := string(bytes.TrimSpace(controlPlaneIDBytes))
		if controlPlaneID == "" || err != nil {
			app.Fatalf("failed to find control plane container: %v", err)
		}

		cfgPathBytes, err := exec.CommandContext(ctx, "kind", "get", "kubeconfig-path", "--name", *prepareName).Output()
		cfgPath := string(bytes.TrimSpace(cfgPathBytes))
		if err != nil {
			app.Fatalf("failed to discover kind cluster config path: %v", err)
		}

		logger.Log("msg", "preparing acceptance docker image")
		buildCmd := exec.CommandContext(ctx, "docker", "build", "-t", *prepareImage, "-f", "-", *prepareBin)
		buildCmd.Stdin = strings.NewReader(AcceptanceDockerfile)

		if err := pipeOutput(buildCmd).Run(); err != nil {
			app.Fatalf("failed to build acceptance docker image: %v", err)
		}

		logger.Log("msg", "loading docker image into control plane", "controlPlane", controlPlaneID)
		saveCmd := exec.CommandContext(ctx, "docker", "save", *prepareImage)
		saveOut, _ := saveCmd.StdoutPipe()
		loadCmd := exec.CommandContext(ctx, "docker", "exec", "-i", controlPlaneID, "docker", "load")
		loadCmd.Stdin = saveOut

		if err := saveCmd.Start(); err != nil {
			app.Fatalf("failed to save acceptance image: %v", err)
		}

		if err := pipeOutput(loadCmd).Run(); err != nil {
			app.Fatalf("failed to load saved image into control plane: %v", err)
		}

		logger.Log("msg", "generating installation manifests")
		manifests, err := exec.CommandContext(ctx, "kustomize", "build", "config/overlays/acceptance").Output()
		if err != nil {
			app.Fatalf("failed to kustomize installation: %v", err)
		}

		logger.Log("msg", "installing manager into cluster")
		applyCmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
		applyCmd.Stdin = bytes.NewReader(manifests)
		applyCmd.Env = append(applyCmd.Env, fmt.Sprintf("KUBECONFIG=%s", cfgPath))

		if err := pipeOutput(applyCmd).Run(); err != nil {
			app.Fatalf("failed to install manager: %v", err)
		}

	case destroy.FullCommand():
		logger = kitlog.With(logger, "clusterName", *destroyName)

		_, err := exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", *destroyName).CombinedOutput()
		if err != nil {
			app.Fatalf("failed to destroy kubernetes cluster with kind: %v", err)
		}

		logger.Log("msg", "successfully destroyed cluster")

	case run.FullCommand():
		contextName = *runName
		RegisterFailHandler(Fail)

		SetDefaultEventuallyTimeout(time.Minute)
		SetDefaultEventuallyPollingInterval(100 * time.Millisecond)

		config.DefaultReporterConfig.Verbose = true
		if RunSpecs(new(testing.T), "theatre/cmd/acceptance") {
			os.Exit(0)
		} else {
			os.Exit(1)
		}
	}
}

var _ = Describe("Acceptance", func() {
	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	cfgPathBytes, err := exec.CommandContext(ctx, "kind", "get", "kubeconfig-path", "--name", contextName).Output()
	cfgPath := string(bytes.TrimSpace(cfgPathBytes))
	if err != nil {
		app.Fatalf("failed to discover kind cluster config path: %v", err)
	}

	consoleAcceptance.Run(kitlog.NewLogfmtLogger(GinkgoWriter), cfgPath)
})

func pipeOutput(cmd *exec.Cmd) *exec.Cmd {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}
