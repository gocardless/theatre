package main

import (
	"context"
	"os"

	kitlog "github.com/go-kit/kit/log"
	"github.com/gocardless/theatre/pkg/workloads/console/runner"
	"github.com/pkg/errors"
	"k8s.io/kubernetes/pkg/kubectl/cmd/get"
)

// A Lister provides the ability to list consoles
type Lister interface {
	List(context.Context, kitlog.Logger, *runner.Runner) error
}

// ListOptions encapsulates the arguments used to list consoles
type ListOptions struct {
	Namespace string
	Username  string
	Selector  string
}

func NewLister(namespace string, username string, selector string) Lister {
	return &ListOptions{
		Namespace: namespace,
		Username:  username,
		Selector:  selector,
	}
}

// List provides the ability to list consoles, given a selector and/or username
func (opts ListOptions) List(ctx context.Context, logger kitlog.Logger, runner *runner.Runner) error {
	consoles, err := runner.ListConsolesByLabelsAndUser(opts.Namespace, opts.Username, opts.Selector)
	if err != nil {
		return errors.Wrap(err, "failed to list consoles")
	}

	printer, err := get.NewGetPrintFlags().ToPrinter()
	if err != nil {
		return err
	}

	for _, cnsl := range consoles {
		printer.PrintObj(&cnsl, os.Stdout)
	}

	return nil
}
