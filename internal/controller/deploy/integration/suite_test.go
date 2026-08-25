package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/internal/controller/deploy"
)

var (
	testEnv     *envtest.Environment
	releaseMgr  ctrl.Manager
	ctx         context.Context
	cancel      context.CancelFunc
	testCounter atomic.Int32
)

func TestSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "controllers/deploy/integration")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := runtime.NewScheme()
	err = clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	err = deployv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	releaseMgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics to avoid port conflicts
		},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&deploy.ReleaseReconciler{
		Client: releaseMgr.GetClient(),
		Scheme: releaseMgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("Release"),
	}).SetupWithManager(ctx, releaseMgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err := releaseMgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	gexec.CleanupBuildArtifacts()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func generateNamespaceName() string {
	return fmt.Sprintf("test-ns-%d-%d", GinkgoParallelProcess(), testCounter.Add(1))
}

func setupTestNamespace(ctx context.Context) string {
	ns := generateNamespaceName()
	err := releaseMgr.GetClient().Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	})
	Expect(err).NotTo(HaveOccurred())
	return ns
}

func generateRelease(namespace string, target string) *v1alpha1.Release {
	appSHA := generateCommitSHA()
	infraSHA := generateCommitSHA()
	return &v1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: target + "-",
			Namespace:    namespace,
		},
		ReleaseConfig: v1alpha1.ReleaseConfig{
			TargetName: target,
			Revisions: []v1alpha1.Revision{
				{Name: "application-revision", ID: appSHA},
				{Name: "infrastructure-revision", ID: infraSHA},
			},
		},
	}
}

func createRelease(ctx context.Context, namespace string, target string, annotations map[string]string) *v1alpha1.Release {
	release := generateRelease(namespace, target)
	release.Annotations = annotations
	err := releaseMgr.GetClient().Create(ctx, release)
	Expect(err).NotTo(HaveOccurred())
	return release
}
