package integration

import (
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	rbacv1alpha1 "github.com/gocardless/theatre/v3/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"
)

var (
	cfg        *rest.Config
	kubeClient client.Client
	testEnv    *envtest.Environment

	scheme = runtime.NewScheme()
)

func TestSuite(t *testing.T) {
	SetDefaultEventuallyTimeout(3 * time.Second)
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/workloads/console/runner/integration")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "..", "config", "base", "crds")},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	user, err := testEnv.AddUser(envtest.User{Name: "user@example.com"}, &rest.Config{})
	Expect(err).ToNot(HaveOccurred())
	Expect(user).ToNot(BeNil())

	err = clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = rbacv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = workloadsv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	kubeClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred(), "could not create client")

}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	gexec.KillAndWait(5 * time.Second)
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})
