package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/alecthomas/kingpin"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/cmd"
	"github.com/google/go-github/v34/github"
	"golang.org/x/oauth2"

	rollbackcontroller "github.com/gocardless/theatre/v5/internal/controller/deploy"
	rollbackwebhook "github.com/gocardless/theatre/v5/internal/webhook/deploy/v1alpha1/rollback"
	"github.com/gocardless/theatre/v5/pkg/cicd"
	argocddeployer "github.com/gocardless/theatre/v5/pkg/cicd/argocd"
	ghdeployer "github.com/gocardless/theatre/v5/pkg/cicd/github"
	"github.com/gocardless/theatre/v5/pkg/signals"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	scheme               = runtime.NewScheme()
	app                  = kingpin.New("rollback-manager", "Manages rollback.deploy.crd.gocardless.com resources").Version(cmd.VersionStanza())
	rollbackHistoryLimit = app.Flag("rollback-history-limit", "Maximum number of rollbacks to keep per target name. All rollbacks older than this will be deleted by the reconciler.").
				Default("10").
				Envar("ROLLBACK_MANAGER_HISTORY_LIMIT").
				Int()
	cicdBackend = app.Flag("cicd-backend", "CICD backend to use (noop, github, argocd)").
			Default("noop").
			Envar("ROLLBACK_MANAGER_CICD_BACKEND").
			Enum("noop", "github", "argocd")
	githubToken = app.Flag("github-token", "GitHub API token for the github cicd backend").
			Envar("ROLLBACK_MANAGER_GITHUB_TOKEN").
			String()
	argocdServerURL = app.Flag("argocd-server-url", "ArgoCD server URL for the argocd cicd backend").
			Envar("ROLLBACK_MANAGER_ARGOCD_SERVER_URL").
			String()
	argocdAuthToken = app.Flag("argocd-auth-token", "ArgoCD auth token for the argocd cicd backend").
			Envar("ROLLBACK_MANAGER_ARGOCD_AUTH_TOKEN").
			String()
	argocdAppNameTemplate = app.Flag("argocd-app-name-template", "Go template for deriving ArgoCD application name. Available fields: {{.Namespace}}, {{.Target}}").
				Envar("ROLLBACK_MANAGER_ARGOCD_APP_NAME_TEMPLATE").
				String()
	commonOptions = cmd.NewCommonOptions(app).WithMetrics(app)

	// deployer holds the configured CICD deployer implementation.
	// This is set during initialization based on configuration.
	deployer cicd.Deployer
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(deployv1alpha1.AddToScheme(scheme))

	// Default to noop deployer - users should configure their own implementation
	// by setting the deployer variable before main() or via a plugin system.
	// Example implementations could be injected via build tags or configuration.
	deployer = &cicd.NoopDeployer{}
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOptions.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	// Initialize the deployer based on the configured backend
	deployer, err := createDeployer(ctx, *cicdBackend)
	if err != nil {
		app.Fatalf("failed to create deployer: %v", err)
	}

	webhookOpts := webhook.Options{Port: 9443}
	webhookServer := webhook.NewServer(webhookOpts)

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		LeaderElection:   commonOptions.ManagerLeaderElection,
		LeaderElectionID: "rollback.deploy.crd.gocardless.com",
		Scheme:           scheme,
		Metrics: metricsserver.Options{
			BindAddress: fmt.Sprintf("%s:%d", commonOptions.MetricAddress, commonOptions.MetricPort),
		},
		WebhookServer: webhookServer,
	})

	if err != nil {
		app.Fatalf("failed to create manager: %v", err)
	}

	err = (&rollbackcontroller.RollbackReconciler{
		Client:               manager.GetClient(),
		Scheme:               scheme,
		Log:                  logger,
		RollbackHistoryLimit: *rollbackHistoryLimit,
		Deployer:             deployer,
	}).SetupWithManager(ctx, manager)

	if err != nil {
		app.Fatalf("failed to create controller: %v", err)
	}

	// Register mutating webhook to auto-set rollback target
	manager.GetWebhookServer().Register("/mutate-rollbacks", &admission.Webhook{
		Handler: rollbackwebhook.NewRollbackTargetWebhook(logger, manager.GetScheme(), manager.GetClient()),
	})

	// Register validating webhook to prevent multiple rollbacks for the same target at the same time
	manager.GetWebhookServer().Register("/validate-rollbacks", &admission.Webhook{
		Handler: rollbackwebhook.NewRollbackValidateWebhook(logger, manager.GetScheme(), manager.GetClient()),
	})

	if err := manager.Start(ctx); err != nil {
		app.Fatalf("failed to start manager: %v", err)
	}
}

func createDeployer(ctx context.Context, backend string) (cicd.Deployer, error) {
	switch backend {
	case "noop":
		return &cicd.NoopDeployer{}, nil
	case "github":
		if *githubToken == "" {
			return nil, fmt.Errorf("github-token is required when using the github deployer backend")
		}
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: *githubToken})
		httpClient := oauth2.NewClient(ctx, ts)
		ghClient := github.NewClient(httpClient)
		logger := zap.New(zap.UseDevMode(true))
		return ghdeployer.NewDeployer(ghClient, logger), nil
	case "argocd":
		if *argocdServerURL == "" {
			return nil, fmt.Errorf("argocd-server-url is required when using the argocd deployer backend")
		}
		if *argocdAuthToken == "" {
			return nil, fmt.Errorf("argocd-auth-token is required when using the argocd deployer backend")
		}

		logger := zap.New(zap.UseDevMode(true))
		return argocddeployer.NewDeployer(http.DefaultClient, *argocdServerURL, *argocdAuthToken, *argocdAppNameTemplate, logger)
	default:
		return nil, fmt.Errorf("unknown deployer backend: %s", backend)
	}
}
