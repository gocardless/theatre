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

	rbacv1alpha1 "github.com/gocardless/theatre/apis/rbac/v1alpha1"
	rbaccontroller "github.com/gocardless/theatre/controllers/rbac"
)

var (
	mgr     ctrl.Manager
	testEnv *envtest.Environment
)

func TestSuite(t *testing.T) {
	SetDefaultEventuallyTimeout(3 * time.Second)
	RegisterFailHandler(Fail)
	RunSpecs(t, "controllers/rbac/integration")
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.LoggerTo(GinkgoWriter, true))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "config", "base", "crds")},
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

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	// Create a fake provider that recognises GoogleGroup kinds for testing purposes
	groups := map[string][]string{}
	groups["all@gocardless.com"] = []string{
		"lawrence@gocardless.com",
	}
	groups["platform@gocardless.com"] = []string{
		"lawrence@gocardless.com",
		"chris@gocardless.com",
	}
	provider := rbaccontroller.DirectoryProvider{}
	provider.Register(rbacv1alpha1.GoogleGroupKind, rbaccontroller.NewFakeDirectory(groups))

	err = (&rbaccontroller.DirectoryRoleBindingReconciler{
		Client:          mgr.GetClient(),
		Ctx:             context.TODO(),
		Log:             ctrl.Log.WithName("controllers").WithName("DirectoryRoleBinding"),
		Provider:        provider,
		RefreshInterval: time.Duration(0), // don't test our caching/re-enqueue here
		Scheme:          mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	gexec.KillAndWait(5 * time.Second)
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})
