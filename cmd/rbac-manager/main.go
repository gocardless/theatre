package main

import (
	"context"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"

	"golang.org/x/oauth2/google"
	directoryv1 "google.golang.org/api/admin/directory/v1"

	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP

	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/gocardless/theatre/cmd"
	"github.com/gocardless/theatre/pkg/apis"
	rbacv1alpha1 "github.com/gocardless/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/gocardless/theatre/pkg/rbac/directoryrolebinding"
	"github.com/gocardless/theatre/pkg/signals"
)

var (
	app     = kingpin.New("rbac-manager", "Manages rbac.crd.gocardless.com resources").Version(Version)
	refresh = app.Flag("refresh", "Refresh interval checking directory sources").Default("1m").Duration()

	commonOpts = cmd.NewCommonOptions(app).WithMetrics(app)

	// All GoogleGroup related settings
	googleEnabled  = app.Flag("google", "Enable GoogleGroup subject Kind").Default("false").Bool()
	googleSubject  = app.Flag("google-subject", "Service account subject").Default("robot-admin@gocardless.com").String()
	googleCacheTTL = app.Flag("google-refresh", "Cache TTL for Google directory operations").Default("5m").Duration()

	// Version is set at compile time
	Version = "dev"
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	if err := apis.AddToScheme(scheme.Scheme); err != nil {
		app.Fatalf("failed to add schemes: %v", err)
	}

	go func() {
		commonOpts.ListenAndServeMetrics(logger)
	}()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	provider := directoryrolebinding.DirectoryProvider{}

	if *googleEnabled {
		googleDirectoryService, err := createGoogleDirectory(context.TODO(), *googleSubject)
		if err != nil {
			app.Fatalf("failed to create Google Admin client: %v", err)
		}

		logger.Log("event", "provider.register", "kind", rbacv1alpha1.GoogleGroupKind)
		provider.Register(
			rbacv1alpha1.GoogleGroupKind,
			directoryrolebinding.NewCachedDirectory(
				logger, directoryrolebinding.NewGoogleDirectory(googleDirectoryService.Members), *googleCacheTTL,
			),
		)
	}

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	// DirectoryRoleBinding controller
	if _, err = directoryrolebinding.Add(ctx, logger, mgr, provider, *refresh); err != nil {
		app.Fatalf(err.Error())
	}

	if err := mgr.Start(ctx.Done()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}

func createGoogleDirectory(ctx context.Context, subject string) (*directoryv1.Service, error) {
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
