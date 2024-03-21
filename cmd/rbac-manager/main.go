package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cloud.google.com/go/compute/metadata"
	"github.com/alecthomas/kingpin"
	"golang.org/x/oauth2/google"
	directoryv1 "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	rbacv1alpha1 "github.com/gocardless/theatre/v3/apis/rbac/v1alpha1"
	"github.com/gocardless/theatre/v3/cmd"
	directoryrolebinding "github.com/gocardless/theatre/v3/controllers/rbac/directoryrolebinding"
	"github.com/gocardless/theatre/v3/pkg/signals"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	app = kingpin.New("rbac-manager", "Manages rbac.crd.gocardless.com resources").Version(cmd.VersionStanza())

	refresh    = app.Flag("refresh", "Refresh interval checking directory sources").Default("1m").Duration()
	commonOpts = cmd.NewCommonOptions(app).WithMetrics(app)

	// All GoogleGroup related settings
	googleEnabled  = app.Flag("google", "Enable GoogleGroup subject Kind").Default("false").Bool()
	googleSubject  = app.Flag("google-subject", "Service account subject").Default("robot-admin@gocardless.com").String()
	googleCacheTTL = app.Flag("google-refresh", "Cache TTL for Google directory operations").Default("5m").Duration()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rbacv1alpha1.AddToScheme(scheme)

	// Register custom metrics with the global controller runtime prometheus registry
	metrics.Registry.MustRegister(cmd.BuildInfo)
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	provider := directoryrolebinding.DirectoryProvider{}

	if *googleEnabled {
		googleDirectoryService, err := createGoogleDirectory(ctx, *googleSubject)
		if err != nil {
			app.Fatalf("failed to create Google Admin client: %v", err)
		}

		logger.Info(
			"registering provider",
			"event", "provider.register", "kind", rbacv1alpha1.GoogleGroupKind)
		provider.Register(
			rbacv1alpha1.GoogleGroupKind,
			directoryrolebinding.NewCachedDirectory(
				logger, directoryrolebinding.NewGoogleDirectory(googleDirectoryService.Members), *googleCacheTTL,
			),
		)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: fmt.Sprintf("%s:%d", commonOpts.MetricAddress, commonOpts.MetricPort),
		Port:               9443,
		LeaderElection:     commonOpts.ManagerLeaderElection,
		LeaderElectionID:   "rbac.crds.gocardless.com",
	})
	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	if err = (&directoryrolebinding.DirectoryRoleBindingReconciler{
		Client:          mgr.GetClient(),
		Ctx:             ctx,
		Log:             ctrl.Log.WithName("controllers").WithName("DirectoryRoleBinding"),
		Provider:        provider,
		RefreshInterval: *refresh,
		Scheme:          mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DirectoryRoleBinding")
		os.Exit(1)
	}

	if err := mgr.Start(ctx); err != nil {
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

	// If the found credential doesn't contain JSON, try to fallback to workload identity
	if len(creds.JSON) == 0 {
		// Get the email address associated with the service account. The account may be empty
		// or the string "default" to use the instance's main account.
		principal, err := metadata.Email("default")
		if err != nil {
			return nil, err
		}

		// Access to the directory API must be signed with a Subject to enable domain selection.
		config := impersonate.CredentialsConfig{
			TargetPrincipal: principal,
			Scopes:          scopes,
			Subject:         subject,
		}

		ts, err := impersonate.CredentialsTokenSource(ctx, config, option.WithCredentials(creds))
		if err != nil {
			return nil, err
		}

		return directoryv1.NewService(ctx, option.WithTokenSource(ts))
	}

	conf, err := google.JWTConfigFromJSON(creds.JSON, strings.Join(scopes, " "))
	if err != nil {
		return nil, err
	}

	// Access to the directory API must be signed with a Subject to enable domain selection.
	conf.Subject = subject

	return directoryv1.NewService(ctx, option.WithHTTPClient(conf.Client(ctx)))
}
