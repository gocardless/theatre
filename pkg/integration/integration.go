package integration

import (
	"context"
	"time"

	"github.com/lawrencejones/theatre/pkg/apis"
	"github.com/onsi/gomega/types"
	"github.com/satori/go.uuid"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

// StartAPIServer creates a fake Kubernetes API server that can be used for integration
// testing Kubernetes controllers. Returns the rest.Config that can be used to connect to
// the API server.
func StartAPIServer(crdDirectoryPath string) (*rest.Config, *envtest.Environment, *kubernetes.Clientset) {
	Expect(apis.AddToScheme(scheme.Scheme)).NotTo(
		HaveOccurred(), "failed to load apis into kubernetes scheme",
	)

	env := &envtest.Environment{
		CRDDirectoryPaths: []string{crdDirectoryPath},
	}

	cfg, err := env.Start()
	Expect(err).NotTo(HaveOccurred(), "failed to start test Kubernetes API server")

	clientset, err := kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes clientset")

	return cfg, env, clientset
}

// CreateNamespace creates a test namespace with a random name for use in integration
// tests. We return the name of the namespace and a closure that can be used to destroy
// the namespace.
func CreateNamespace(clientset *kubernetes.Clientset) (string, func()) {
	name := uuid.NewV4().String()
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	By("Creating test namespace: " + name)
	namespace, err := clientset.CoreV1().Namespaces().Create(namespace)
	Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	return name, func() {
		By("Deleting test namespace: " + name)
		err := clientset.CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
	}
}

// StartTestManager generates a new Manager connected to the given cluster configuration.
func StartTestManager(ctx context.Context, cfg *rest.Config) manager.Manager {
	mgr, err := manager.New(cfg, manager.Options{})
	Expect(err).NotTo(HaveOccurred(), "failed to create test manager")

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx.Done())).NotTo(
			HaveOccurred(), "failed to run manager",
		)
	}()

	return mgr
}

// CaptureReconcile wraps the given reconciler and returns a channel that emits all
// reconciliation calls that the inner reconciler has received.
func CaptureReconcile(inner reconcile.Reconciler) (reconcile.Reconciler, chan ReconcileCall) {
	calls := make(chan ReconcileCall)
	captured := reconcile.Func(func(req reconcile.Request) (reconcile.Result, error) {
		result, err := inner.Reconcile(req)
		calls <- ReconcileCall{Namespace: req.Namespace, Name: req.Name, Request: req, Result: result, Error: err}
		return result, err
	})

	return captured, calls
}

// ReconcileCall is a record of each requested reconciliation, both the request and the
// returned response/error. This can be used to assert against the behaviour of a
// reconciliation loop.
type ReconcileCall struct {
	Namespace, Name string
	reconcile.Request
	reconcile.Result
	Error error
}

func ReconcileResourceSuccess(namespace, name string) types.GomegaMatcher {
	return SatisfyAll(
		ReconcileResource(namespace, name),
		ReconcileSuccessfully(),
		ReconcileNoRetry(),
	)
}

func ReconcileSuccessfully() types.GomegaMatcher {
	return MatchFields(IgnoreExtras, Fields{"Error": BeNil()})
}

func ReconcileNoRetry() types.GomegaMatcher {
	return MatchFields(IgnoreExtras, Fields{"Result": Equal(reconcile.Result{})})
}

func ReconcileResource(namespace, name string) types.GomegaMatcher {
	return MatchFields(IgnoreExtras, Fields{"Namespace": Equal(namespace), "Name": Equal(name)})
}

// Drain can be used to drain all calls from the channel before continuing with a test
func Drain(calls chan ReconcileCall) {
	for {
		select {
		case <-calls:
		case <-time.After(time.Second):
			return
		}
	}
}
