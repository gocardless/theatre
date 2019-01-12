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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/lawrencejones/theatre/pkg/apis"
	rbacv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/lawrencejones/theatre/pkg/controllers/directoryrolebinding"
	"github.com/lawrencejones/theatre/pkg/signals"
)

var (
	app              = kingpin.New("manager", "Manages lawrjone.xyz operators 😷").Version(Version)
	subject          = app.Flag("subject", "Service Subject account").Default("robot-admin@gocardless.com").String()
	directoryRefresh = app.Flag("directory-refresh", "Refresh interval for directory operations").Default("5m").Duration()
	threads          = app.Flag("threads", "Number of threads for the operator").Default("2").Int()

	logger = kitlog.NewLogfmtLogger(os.Stderr)

	// Version is set by goreleaser
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
	if err := rbacv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		app.Fatalf("failed to add rbac scheme: %v", err)
	}

	// TODO: We don't use this clientset, but no doubt we will. Leaving it here until it
	// becomes useful.
	_, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		app.Fatalf("failed to create kubernetes client: %v", err)
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

	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		app.Fatalf("failed to add CRDs to scheme: %v", err)
	}

	// DirectoryRoleBinding controller
	directory := directoryrolebinding.NewGoogleDirectory(directoryService.Members)
	if _, err = directoryrolebinding.Add(ctx, mgr, logger, directory, *directoryRefresh); err != nil {
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
