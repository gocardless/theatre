package main

import (
	"context"
	stdlog "log"
	"os"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gocardless/theatre/pkg/logging"
	"github.com/gocardless/theatre/pkg/signals"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/klog"
)

var (
	app = kingpin.New("consoles", "Manage theatre consoles")

	create        = app.Command("create", "Create a new console from a console template")
	createOptions = new(CreateOptions).Bind(create)

	attach        = app.Command("attach", "Attach to a running console")
	attachOptions = new(AttachOptions).Bind(attach)
)

func main() {
	logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(os.Stderr))
	logger = level.NewFilter(logger, level.AllowInfo())
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", logging.RecorderAwareCaller())
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))

	ctx, _ := signals.SetupSignalHandler()

	if err := run(ctx, logger); err != nil {
		app.Fatalf("unexpected error: %s", err)
	}
}

func run(ctx context.Context, logger kitlog.Logger) error {
	command := kingpin.MustParse(app.Parse(os.Args[1:]))

	switch command {
	case create.FullCommand():
		return NewCreate(createOptions).Run(ctx, logger)
	case attach.FullCommand():
		return NewAttach(attachOptions).Run(ctx, logger)
	}

	return nil
}
