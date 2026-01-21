package integration

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
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

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/internal/controller/deploy"
	"github.com/gocardless/theatre/v5/pkg/cicd"
)

var (
	testEnv          *envtest.Environment
	deployer         *FakeDeployer
	mgr              ctrl.Manager
	ctx              context.Context
	cancel           context.CancelFunc
	namespaceCounter atomic.Int32
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

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})
	Expect(err).NotTo(HaveOccurred())

	deployer = NewFakeDeployer()

	err = (&deploy.RollbackReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Log:      ctrl.Log.WithName("controllers").WithName("Rollback"),
		Deployer: deployer,
	}).SetupWithManager(ctx, mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&deploy.ReleaseReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("Release"),
	}).SetupWithManager(ctx, mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err := mgr.Start(ctx)
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

// TriggerResult holds the result for a TriggerDeployment call
type TriggerResult struct {
	Result *cicd.DeploymentResult
	Err    error
}

// StatusResult holds the result for a GetDeploymentStatus call
type StatusResult struct {
	Result *cicd.DeploymentResult
	Err    error
}

// FakeDeployer is a thread-safe fake implementation of the cicd.Deployer interface
type FakeDeployer struct {
	TriggerResults sync.Map // map[string]TriggerResult keyed by "namespace/name"
	StatusResults  sync.Map // map[string]StatusResult keyed by deploymentID
}

func NewFakeDeployer() *FakeDeployer {
	return &FakeDeployer{}
}

func (f *FakeDeployer) TriggerDeployment(ctx context.Context, req cicd.DeploymentRequest) (*cicd.DeploymentResult, error) {
	key := req.Rollback.Namespace + "/" + req.Rollback.Name
	if val, ok := f.TriggerResults.Load(key); ok {
		result := val.(TriggerResult)
		return result.Result, result.Err
	}

	// Default: return a pending deployment with options encoded in URL
	deploymentURL := "https://example.com/deployments/" + req.Rollback.Name
	if len(req.Options) > 0 {
		params := url.Values{}
		for k, v := range req.Options {
			params.Set(k, v)
		}
		deploymentURL += "?" + params.Encode()
	}
	return &cicd.DeploymentResult{
		ID:      "default-deployment-" + req.Rollback.Name,
		URL:     deploymentURL,
		Status:  cicd.DeploymentStatusPending,
		Message: "Deployment created",
	}, nil
}

func (f *FakeDeployer) GetDeploymentStatus(ctx context.Context, deploymentID string) (*cicd.DeploymentResult, error) {
	if val, ok := f.StatusResults.Load(deploymentID); ok {
		result := val.(StatusResult)
		return result.Result, result.Err
	}

	// Default: return success
	return &cicd.DeploymentResult{
		ID:      deploymentID,
		Status:  cicd.DeploymentStatusSucceeded,
		Message: "Deployment succeeded",
	}, nil
}

func (f *FakeDeployer) Name() string {
	return "fake"
}

func (f *FakeDeployer) SetTriggerResult(namespace, name string, result TriggerResult) {
	f.TriggerResults.Store(namespace+"/"+name, result)
}

func (f *FakeDeployer) SetStatusResult(deploymentID string, result StatusResult) {
	f.StatusResults.Store(deploymentID, result)
}

func generateNamespaceName() string {
	return fmt.Sprintf("test-ns-%d-%d", GinkgoParallelProcess(), namespaceCounter.Add(1))
}

func setupTestNamespace(ctx context.Context) string {
	ns := generateNamespaceName()
	err := mgr.GetClient().Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	})
	Expect(err).NotTo(HaveOccurred())
	return ns
}

func generateRelease(target string, namespace string) *v1alpha1.Release {
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

func createRelease(ctx context.Context, namespace string, target string) *v1alpha1.Release {
	release := generateRelease(target, namespace)
	err := mgr.GetClient().Create(ctx, release)
	Expect(err).NotTo(HaveOccurred())
	return release
}
