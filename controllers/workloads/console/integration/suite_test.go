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
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	rbacv1alpha1 "github.com/gocardless/theatre/v3/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"
	consolecontroller "github.com/gocardless/theatre/v3/controllers/workloads/console"
	"github.com/gocardless/theatre/v3/pkg/workloads/console/events"
)

var (
	mgr     ctrl.Manager
	testEnv *envtest.Environment

	finished = make(chan struct{})
)

func TestSuite(t *testing.T) {
	SetDefaultEventuallyTimeout(3 * time.Second)
	RegisterFailHandler(Fail)
	RunSpecs(t, "controllers/workloads/integration")
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "base", "crds")},
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			DirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "base", "webhooks")},
		},
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: "user@example.com",
	}

	scheme := runtime.NewScheme()
	err = clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = rbacv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = workloadsv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	lifecycleRecorder := workloadsv1alpha1.NewLifecycleEventRecorder("test", ctrl.Log.Logger, events.NewNopPublisher())

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,

		Port:    testEnv.WebhookInstallOptions.LocalServingPort,
		Host:    testEnv.WebhookInstallOptions.LocalServingHost,
		CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
	})
	Expect(err).ToNot(HaveOccurred())

	// console authenticator webhook
	mgr.GetWebhookServer().Register("/mutate-consoles", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthenticatorWebhook(
			lifecycleRecorder,
			ctrl.Log.WithName("webhooks").WithName("console-authenticator"),
		),
	})

	// console authorisation webhook
	mgr.GetWebhookServer().Register("/validate-consoleauthorisations", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleAuthorisationWebhook(
			mgr.GetClient(),
			lifecycleRecorder,
			ctrl.Log.WithName("webhooks").WithName("console-authorisation"),
		),
	})

	// console template webhook
	mgr.GetWebhookServer().Register("/validate-consoletemplates", &admission.Webhook{
		Handler: workloadsv1alpha1.NewConsoleTemplateValidationWebhook(
			ctrl.Log.WithName("webhooks").WithName("console-template"),
		),
	})

	// workloads pod PriorityClass webhook
	mgr.GetWebhookServer().Register("/mutate-pods", &admission.Webhook{
		Handler: workloadsv1alpha1.NewPriorityInjector(
			mgr.GetClient(),
			ctrl.Log.WithName("webhooks").WithName("priority-injector"),
		),
	})

	err = (&consolecontroller.ConsoleReconciler{
		Client:            mgr.GetClient(),
		LifecycleRecorder: lifecycleRecorder,
		Log:               ctrl.Log.WithName("controllers").WithName("console"),
		Scheme:            mgr.GetScheme(),
	}).SetupWithManager(context.TODO(), mgr)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		<-ctrl.SetupSignalHandler()
		close(finished)
	}()

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(finished)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
		gexec.KillAndWait(4 * time.Second)
		err := testEnv.Stop()
		Expect(err).ToNot(HaveOccurred())
	}()

	close(done)
}, 60)

var _ = AfterSuite(func() {
	close(finished)
})
