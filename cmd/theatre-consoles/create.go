package main

import (
	"context"
	"time"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	theatre "github.com/gocardless/theatre/pkg/client/clientset/versioned"
	"github.com/gocardless/theatre/pkg/workloads/console/runner"
	consoleRunner "github.com/gocardless/theatre/pkg/workloads/console/runner"
	"k8s.io/client-go/kubernetes"
)

const createUsage = `TODO`

type Create struct {
	opt *CreateOptions
}

type CreateOptions struct {
	Reason   string
	Selector string
	Timeout  time.Duration
	Command  []string

	KubernetesOptions
}

func (opt *CreateOptions) Bind(cmd *kingpin.CmdClause) *CreateOptions {
	cmd.Flag("reason", "Reason for creating console").StringVar(&opt.Reason)
	cmd.Flag("selector", "Lable selector to match against console template").StringVar(&opt.Selector)
	cmd.Flag("timeout", "Timeout for the new console").DurationVar(&opt.Timeout)
	cmd.Arg("command", "Command to run in console").StringsVar(&opt.Command)

	opt.KubernetesOptions.Bind(cmd)

	return opt
}

func NewCreate(opt *CreateOptions) *Create {
	return &Create{opt: opt}
}

func (c *Create) Run(ctx context.Context, logger kitlog.Logger) error {
	config, err := newKubeConfig(c.opt.Context)
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

	tpl, err := runner.FindTemplateBySelector(c.opt.Namespace, c.opt.Selector)
	if err != nil {
		return err
	}

	opt := consoleRunner.Options{Cmd: c.opt.Command, Timeout: int(c.opt.Timeout.Seconds()), Reason: c.opt.Reason}
	csl, err := runner.Create(tpl.Namespace, *tpl, opt)
	if err != nil {
		return err
	}

	csl, err = runner.WaitUntilReady(ctx, *csl)
	if err != nil {
		return err
	}

	pod, err := runner.GetAttachablePod(csl)
	if err != nil {
		return err
	}

	logger.Log("pod", pod.Name, "msg", "console pod created")

	return nil
}
