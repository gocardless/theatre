package main

import (
	"context"
	"os"

	kitlog "github.com/go-kit/kit/log"
	"github.com/gocardless/theatre/pkg/workloads/console/runner"
	"github.com/pkg/errors"
	"k8s.io/kubernetes/pkg/kubectl/cmd/get"
)

var (
	listUsername = list.Flag("user", "Kubernetes username. Not usually supplied, can be inferred from your gcloud login").
		Short('u').
		Default("").
		String()
)

func List(ctx context.Context, logger kitlog.Logger, runner *runner.Runner, namespace string) error {
	username := ""
	if listUsername != nil {
		username = *listUsername
	}

	selector := ""
	if cliSelector != nil {
		selector = *cliSelector
	}

	consoles, err := runner.ListConsolesByLabelsAndUser(namespace, username, selector)
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
