package cmd

import (
	"fmt"
	stdlog "log"
	"net/http"
	"os"
	"runtime"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"k8s.io/klog"

	"github.com/gocardless/theatre/pkg/logging"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type commonOptions struct {
	MetricAddress string
	MetricPort    uint16
	Debug         bool
}

func NewCommonOptions(cmd *kingpin.Application) *commonOptions {
	opt := &commonOptions{}

	cmd.Flag("debug", "Enable debug logging").Default("false").BoolVar(&opt.Debug)

	return opt
}

func (opt *commonOptions) WithMetrics(cmd *kingpin.Application) *commonOptions {
	cmd.Flag("metrics-address", "Address to bind HTTP metrics listener").Default("127.0.0.1").StringVar(&opt.MetricAddress)
	cmd.Flag("metrics-port", "Port to bind HTTP metrics listener").Default("9525").Uint16Var(&opt.MetricPort)

	return opt
}

func (opt *commonOptions) Logger() kitlog.Logger {
	// Output logs to STDERR
	logger := kitlog.NewLogfmtLogger(kitlog.NewSyncWriter(os.Stderr))

	// While debugging, it may be useful to provide debug log lines that
	// include sensitive or large payloads.
	if opt.Debug {
		logger = level.NewFilter(logger, level.AllowDebug())
	} else {
		logger = level.NewFilter(logger, level.AllowInfo())
	}

	// @template: caller is optional, but can be useful to highlight where the log-line is
	// originating from. This configuration has to happen after we've leveled the logger, to
	// avoid the caller being set to level.go:63 all the time.
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", logging.RecorderAwareCaller())

	// Connect the Go standard library logger into our kitlog instance so other
	// dependencies output via kitlog.
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))

	return logger
}

func (opt *commonOptions) ListenAndServeMetrics(logger kitlog.Logger) {
	logger.Log("event", "metrics_listen", "address", opt.MetricAddress, "port", opt.MetricPort)

	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(fmt.Sprintf("%s:%v", opt.MetricAddress, opt.MetricPort), nil)
}

// Set via compiler flags
var (
	Version   = "dev"
	Commit    = "none"
	Date      = "unknown"
	GoVersion = runtime.Version()
)

func VersionStanza() string {
	return fmt.Sprintf(
		"Version: %v\nGit SHA: %v\nGo Version: %v\nGo OS/Arch: %v/%v\nBuilt at: %v",
		Version, Commit, GoVersion, runtime.GOOS, runtime.GOARCH, Date,
	)
}
