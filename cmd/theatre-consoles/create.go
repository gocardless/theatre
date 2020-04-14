package main

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"
	"github.com/gocardless/theatre/pkg/workloads/console/runner"
	consoleRunner "github.com/gocardless/theatre/pkg/workloads/console/runner"
)

// CreateOptions encapsulates the arguments to create a console
type CreateOptions struct {
	Namespace string
	Selector  string
	Timeout   time.Duration
	Reason    string
	Command   []string
}

// Create attempts to create a console in the given in the given namespace after finding the a template using selectors.
func Create(ctx context.Context, logger kitlog.Logger, runner *runner.Runner, opts CreateOptions) error {
	var err error

	// Create and attach to the console
	tpl, err := runner.FindTemplateBySelector(opts.Namespace, opts.Selector)
	if err != nil {
		return err
	}

	opt := consoleRunner.Options{Cmd: opts.Command, Timeout: int(opts.Timeout.Seconds()), Reason: opts.Reason}
	csl, err := runner.Create(tpl.Namespace, *tpl, opt)
	if err != nil {
		return nil
	}

	csl, err = runner.WaitUntilReady(ctx, *csl)
	if err != nil {
		return nil
	}

	pod, err := runner.GetAttachablePod(csl)
	if err != nil {
		return nil
	}

	logger.Log("pod", pod.Name, "msg", "console pod created")

	return err
}
