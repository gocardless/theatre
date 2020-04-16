package main

import (
	"context"
	"os"

	kitlog "github.com/go-kit/kit/log"
	consoleRunner "github.com/gocardless/theatre/pkg/workloads/console/runner"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubernetes/pkg/kubectl/util/term"
)

// AttachOptions encapsulates the arguments to attach to a console
type AttachOptions struct {
	Namespace string
	Client    *kubernetes.Clientset
	Config    *rest.Config
	Name      string
}

// Attach provides the ability to attach to a running console, given the console name
func Attach(ctx context.Context, logger kitlog.Logger, runner *consoleRunner.Runner, opts AttachOptions) error {
	csl, err := runner.FindConsoleByName(opts.Namespace, opts.Name)
	if err != nil {
		return err
	}

	pod, err := runner.GetAttachablePod(csl)
	if err != nil {
		return errors.Wrap(err, "could not find pod to attach to")
	}
	logger.Log("pod", pod.Name, "msg", "console pod is ready")
	logger.Log("pod", pod.Name, "msg", "If you don't see a prompt, press enter.")

	if err := NewAttacher(opts.Client, opts.Config).Attach(pod); err != nil {
		return errors.Wrap(err, "failed to attach to console")
	}

	return nil
}

func NewAttacher(clientset *kubernetes.Clientset, restconfig *rest.Config) *Attacher {
	return &Attacher{clientset, restconfig}
}

// CreateInteractiveStreamOptions constructs streaming configuration that
// attaches the default OS stdout, stderr, stdin, with a tty, and an additional
// function which should be used to wrap any interactive process that will make
// use of the tty.
func CreateInteractiveStreamOptions() (remotecommand.StreamOptions, func(term.SafeFunc) error) {
	// TODO: We may want to setup a parent interrupt handler, so that if/when the
	// pod is terminated while a user is attached, they aren't left with their
	// terminal in a strange state, if they're running something curses-based in
	// the console.
	// Parent: ...
	tty := term.TTY{
		In:     os.Stdin,
		Out:    os.Stderr,
		Raw:    true,
		TryDev: false,
	}

	// This call spawns a goroutine to monitor/update the terminal size
	sizeQueue := tty.MonitorSize(tty.GetSize())

	return remotecommand.StreamOptions{
		Stderr:            os.Stderr,
		Stdout:            os.Stdout,
		Stdin:             os.Stdin,
		Tty:               true,
		TerminalSizeQueue: sizeQueue,
	}, tty.Safe
}

// Attacher knows how to attach to stdio of an existing container, relaying io
// to the parent process file descriptors.
type Attacher struct {
	clientset  *kubernetes.Clientset
	restconfig *rest.Config
}

// Attach will interactively attach to a containers output, creating a new TTY
// and hooking this into the current processes file descriptors.
func (a *Attacher) Attach(pod *corev1.Pod) error {
	req := a.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(pod.GetNamespace()).
		Name(pod.GetName()).
		SubResource("attach")

	req.VersionedParams(
		&corev1.PodAttachOptions{
			Stdin:  true,
			Stdout: true,
			Stderr: true,
			TTY:    true,
		},
		scheme.ParameterCodec,
	)

	remoteExecutor, err := remotecommand.NewSPDYExecutor(a.restconfig, "POST", req.URL())
	if err != nil {
		return errors.Wrap(err, "failed to create SPDY executor")
	}

	streamOptions, safe := CreateInteractiveStreamOptions()

	return safe(func() error { return remoteExecutor.Stream(streamOptions) })
}
