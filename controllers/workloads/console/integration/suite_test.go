package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	rbacv1alpha1 "github.com/gocardless/theatre/v4/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/v4/apis/workloads/v1alpha1"
	consolecontroller "github.com/gocardless/theatre/v4/controllers/workloads/console"
	"github.com/gocardless/theatre/v4/pkg/workloads/console/events"
)

var (
	mgr     ctrl.Manager
	testEnv *envtest.Environment
)

func TestSuite(t *testing.T) {
	SetDefaultEventuallyTimeout(3 * time.Second)
	RegisterFailHandler(Fail)
	RunSpecs(t, "controllers/workloads/integration")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "base", "crds")},
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "..", "config", "base", "webhooks")},
		},
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	user, err := testEnv.AddUser(envtest.User{Name: "user@example.com"}, &rest.Config{})
	Expect(err).ToNot(HaveOccurred())
	Expect(user).ToNot(BeNil())

	scheme := runtime.NewScheme()
	err = clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = rbacv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = workloadsv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	idBuilder := workloadsv1alpha1.NewConsoleIdBuilder("test")
	lifecycleRecorder := workloadsv1alpha1.NewLifecycleEventRecorder("test", ctrl.Log, events.NewNopPublisher(), idBuilder)

	server := webhook.NewServer(webhook.Options{
		Host:    testEnv.WebhookInstallOptions.LocalServingHost,
		CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
		Port:    testEnv.WebhookInstallOptions.LocalServingPort,
	})

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:        scheme,
		WebhookServer: server,
	})
	Expect(err).ToNot(HaveOccurred())

	// console authenticator webhook
	mgr.GetWebhookServer().Register("/mutate-consoles", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthenticatorWebhook(
			lifecycleRecorder,
			ctrl.Log.WithName("webhooks").WithName("console-authenticator"),
			mgr.GetScheme(),
		),
	})

	// console authorisation webhook
	mgr.GetWebhookServer().Register("/validate-consoleauthorisations", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthorisationWebhook(
			mgr.GetClient(),
			lifecycleRecorder,
			ctrl.Log.WithName("webhooks").WithName("console-authorisation"),
			mgr.GetScheme(),
		),
	})

	// console template webhook
	mgr.GetWebhookServer().Register("/validate-consoletemplates", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleTemplateValidationWebhook(
			ctrl.Log.WithName("webhooks").WithName("console-template"),
			mgr.GetScheme(),
		),
	})

	err = (&consolecontroller.ConsoleReconciler{
		Client:                     mgr.GetClient(),
		LifecycleRecorder:          lifecycleRecorder,
		Log:                        ctrl.Log.WithName("controllers").WithName("console"),
		Scheme:                     mgr.GetScheme(),
		ConsoleIdBuilder:           workloadsv1alpha1.NewConsoleIdBuilder("test"),
		EnableDirectoryRoleBinding: true,
	}).SetupWithManager(context.TODO(), mgr)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
		gexec.KillAndWait(4 * time.Second)
		err := testEnv.Stop()
		Expect(err).ToNot(HaveOccurred())
	}()

}, 60)
