package main

import (
	"context"
	stdlog "log"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	level "github.com/go-kit/kit/log/level"

	"golang.org/x/oauth2/google"
	directoryv1 "google.golang.org/api/admin/directory/v1"

	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/klog"

	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/gocardless/theatre/pkg/apis"
	"github.com/gocardless/theatre/pkg/rbac/directoryrolebinding"
	"github.com/gocardless/theatre/pkg/signals"
)

var (
	app              = kingpin.New("rbac-manager", "Manages rbac.crd.gocardless.com resources").Version(Version)
	subject          = app.Flag("subject", "Service Subject account").Default("robot-admin@gocardless.com").String()
	directoryRefresh = app.Flag("directory-refresh", "Refresh interval for directory operations").Default("5m").Duration()

	logger = kitlog.NewLogfmtLogger(os.Stderr)

	// Version is set at compile time
	Version = "dev"
)

func init() {
	logger = level.NewFilter(logger, level.AllowInfo())
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", kitlog.DefaultCaller)
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	if err := apis.AddToScheme(scheme.Scheme); err != nil {
		app.Fatalf("failed to add schemes: %v", err)
	}

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	directoryService, err := createDirectoryService(context.TODO(), *subject)
	if err != nil {
		app.Fatalf("failed to create Google Admin client: %v", err)
	}

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	// DirectoryRoleBinding controller
	directory := directoryrolebinding.NewGoogleDirectory(directoryService.Members)
	if _, err = directoryrolebinding.Add(ctx, logger, mgr, directory, *directoryRefresh); err != nil {
		app.Fatalf(err.Error())
	}

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}

func createDirectoryService(ctx context.Context, subject string) (*directoryv1.Service, error) {
	scopes := []string{
		directoryv1.AdminDirectoryGroupMemberReadonlyScope,
		directoryv1.AdminDirectoryGroupReadonlyScope,
	}

	creds, err := google.FindDefaultCredentials(ctx, scopes...)
	if err != nil {
		return nil, err
	}

	conf, err := google.JWTConfigFromJSON(creds.JSON, strings.Join(scopes, " "))
	if err != nil {
		return nil, err
	}

	// Access to the directory API must be signed with a Subject to enable domain selection.
	conf.Subject = subject

	return directoryv1.New(conf.Client(ctx))
}
