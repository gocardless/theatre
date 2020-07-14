package cmd

import (
	"fmt"
	"net/http"
	"os"
	"runtime"

	"github.com/alecthomas/kingpin"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	zaplogfmt "github.com/sykesm/zap-logfmt"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type commonOptions struct {
	MetricAddress         string
	MetricPort            uint16
	ManagerMetricAddress  string
	ManagerMetricPort     uint16
	ManagerLeaderElection bool
	Debug                 bool
}

func NewCommonOptions(cmd *kingpin.Application) *commonOptions {
	opt := &commonOptions{}

	cmd.Flag("manager-leader-election", "Enable manager leader election").Default("false").BoolVar(&opt.ManagerLeaderElection)
	cmd.Flag("debug", "Enable debug logging").Default("false").BoolVar(&opt.Debug)

	return opt
}

func (opt *commonOptions) WithMetrics(cmd *kingpin.Application) *commonOptions {
	cmd.Flag("metrics-address", "Address to bind HTTP metrics listener").Default("127.0.0.1").StringVar(&opt.MetricAddress)
	cmd.Flag("metrics-port", "Port to bind HTTP metrics listener").Default("9525").Uint16Var(&opt.MetricPort)

	cmd.Flag("manager-metrics-address", "Address to bind manager HTTP metrics listener").Default("127.0.0.1").StringVar(&opt.ManagerMetricAddress)
	cmd.Flag("manager-metrics-port", "Port to bind manager HTTP metrics listener").Default("9526").Uint16Var(&opt.ManagerMetricPort)

	return opt
}

func (opt *commonOptions) Logger() logr.Logger {
	// While debugging, it may be useful to provide debug log lines that
	// include sensitive or large payloads.
	logLevel := zapcore.InfoLevel
	if opt.Debug {
		logLevel = zapcore.DebugLevel
	}

	logger := zap.New(
		zap.Encoder(zaplogfmt.NewEncoder(zapcore.EncoderConfig{})),
		zap.WriteTo(os.Stderr),
		zap.Level(logLevel),
	)
	ctrl.SetLogger(logger)

	// @template: caller is optional, but can be useful to highlight where the log-line is
	// originating from. This configuration has to happen after we've leveled the logger, to
	// avoid the caller being set to level.go:63 all the time.
	// logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", logging.RecorderAwareCaller())

	return logger
}

func (opt *commonOptions) ListenAndServeMetrics(logger logr.Logger) {
	logger.Info("listening on metrics", "event", "metrics_listen", "address", opt.MetricAddress, "port", opt.MetricPort)

	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(fmt.Sprintf("%s:%d", opt.MetricAddress, opt.MetricPort), nil)
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
