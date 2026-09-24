package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/internal/controller/deploy"
	"github.com/gocardless/theatre/v5/pkg/cicd"
)

var (
	testEnv  *envtest.Environment
	deployer *FakeDeployer
	// k8sClient talks to the API server directly, bypassing the controllers'
	// caches, so assertions never observe a stale cached object.
	k8sClient client.Client
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "controllers/deploy/integration")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Controllers here react within tens of milliseconds, so poll frequently and
	// keep the negative-assertion window short.
	SetDefaultEventuallyTimeout(5 * time.Second)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
	SetDefaultConsistentlyDuration(time.Second)
	SetDefaultConsistentlyPollingInterval(100 * time.Millisecond)

	ctx, cancel = context.WithCancel(context.Background())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "..", "config", "crd", "bases"),
			// AnalysisRun/AnalysisTemplate/ClusterAnalysisTemplate are owned by Argo
			// Rollouts, so their CRDs are vendored under config/crd/external.
			filepath.Join("..", "..", "..", "..", "config", "crd", "external"),
		},
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
	err = analysisv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	// One manager per controller, mirroring production where each runs as its
	// own binary. They cannot share a manager today: all three register the
	// IndexFieldReleaseTarget field index, which a shared cache rejects with
	// "indexer conflict".
	rollbackMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics to avoid port conflicts
		},
	})
	Expect(err).NotTo(HaveOccurred())

	releaseMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics to avoid port conflicts
		},
	})
	Expect(err).NotTo(HaveOccurred())

	automatedRollbackMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics to avoid port conflicts
		},
	})
	Expect(err).NotTo(HaveOccurred())

	deployer = NewFakeDeployer()

	err = (&deploy.RollbackReconciler{
		Client:   rollbackMgr.GetClient(),
		Scheme:   rollbackMgr.GetScheme(),
		Log:      ctrl.Log.WithName("controllers").WithName("Rollback"),
		Deployer: deployer,
	}).SetupWithManager(ctx, rollbackMgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&deploy.ReleaseReconciler{
		Client: releaseMgr.GetClient(),
		Scheme: releaseMgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("Release"),
		// Automated rollbacks are only supported alongside analysis: nothing
		// else sets the RollbackRequired condition.
		AnalysisEnabled: true,
	}).SetupWithManager(ctx, releaseMgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&deploy.AutomatedRollbackReconciler{
		Client: automatedRollbackMgr.GetClient(),
		Scheme: automatedRollbackMgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("AutomatedRollback"),
	}).SetupWithManager(ctx, automatedRollbackMgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err := rollbackMgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	go func() {
		defer GinkgoRecover()
		err := releaseMgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	go func() {
		defer GinkgoRecover()
		err := automatedRollbackMgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
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
			params.Set(k, fmt.Sprint(v))
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

// setupTestNamespace creates a namespace with an API server generated name.
// Namespaces are never deleted: envtest runs no namespace controller, so a
// delete would hang in Terminating. They are cheap and the suite is short
// lived, so we leak them deliberately.
func setupTestNamespace(ctx context.Context) string {
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-ns-",
		},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns.Name
}

func generateRelease(namespace string, target string) *deployv1alpha1.Release {
	appSHA := generateCommitSHA()
	infraSHA := generateCommitSHA()
	return &deployv1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: target + "-",
			Namespace:    namespace,
		},
		ReleaseConfig: deployv1alpha1.ReleaseConfig{
			TargetName: target,
			Revisions: []deployv1alpha1.Revision{
				{Name: "application-revision", ID: appSHA},
				{Name: "infrastructure-revision", ID: infraSHA},
			},
		},
	}
}

func createRelease(ctx context.Context, namespace string, target string, annotations map[string]string) *deployv1alpha1.Release {
	release := generateRelease(namespace, target)
	release.Annotations = annotations
	Expect(k8sClient.Create(ctx, release)).To(Succeed())
	return release
}

func generateTargetName() string {
	return "test-target-" + randomHex(4)
}

func generateCommitSHA() string {
	return randomHex(20)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	Expect(err).NotTo(HaveOccurred())
	return hex.EncodeToString(b)
}
