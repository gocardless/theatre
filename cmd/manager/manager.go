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
	admin "google.golang.org/api/admin/directory/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/klog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/lawrencejones/theatre/pkg/apis"
	rbacv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/lawrencejones/theatre/pkg/controller/directoryrolebinding"
	"github.com/lawrencejones/theatre/pkg/signals"
)

var (
	app             = kingpin.New("manager", "Manages lawrjone.xyz operators 😷").Version(Version)
	subject         = app.Flag("subject", "Service Subject account").Default("robot-admin@gocardless.com").String()
	refreshInterval = app.Flag("refresh-interval", "Period to refresh our listeners").Default("10s").Duration()
	threads         = app.Flag("threads", "Number of threads for the operator").Default("2").Int()

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

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		app.Fatalf("failed to add CRDs to scheme: %v", err)
	}

	client, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		app.Fatalf("failed to create kubernetes client: %v", err)
	}

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	adminClient, err := createAdminClient(context.TODO(), *subject)
	if err != nil {
		app.Fatalf("failed to create Google Admin client: %v", err)
	}

	_, err = builder.SimpleController().
		WithManager(mgr).
		ForType(&rbacv1alpha1.DirectoryRoleBinding{}).
		Owns(&rbacv1.RoleBinding{}).
		Build(
			directoryrolebinding.NewDirectoryRoleBindingController(
				ctx, logger, mgr.GetRecorder("DirectoryRoleBinding"), client, adminClient,
			),
		)

	if err != nil {
		app.Fatalf("failed to build controller: %v", err)
	}

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}

func createAdminClient(ctx context.Context, subject string) (*admin.Service, error) {
	scopes := []string{
		admin.AdminDirectoryGroupMemberReadonlyScope,
		admin.AdminDirectoryGroupReadonlyScope,
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

	return admin.New(conf.Client(ctx))
}
