package integration

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gocardless/theatre/pkg/apis"
	types "github.com/onsi/gomega/types"
	"github.com/satori/go.uuid"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	atypes "sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"

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
		CRDDirectoryPaths:  []string{crdDirectoryPath},
		KubeAPIServerFlags: kubeAPIServerFlags(),
	}

	cfg, err := env.Start()
	Expect(err).NotTo(HaveOccurred(), "failed to start test Kubernetes API server")

	clientset, err := kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes clientset")

	return cfg, env, clientset
}

// kubeAPIServerFlags provides a customised list of flags for the API server, as opposed
// to what the controller-runtime sets by default. This is important as at time of
// writing, the controller-runtime uses a deprecated flag called --admission-control which
// sets an absolute list of admission controllers, thereby removing the default webhook
// controllers.
//
// Given this will prevent any of the normal admission controller behaviour, along with
// preventing us from integration testing webhooks, we omit this flag and instead enable
// the AlwaysAdmit plugin which does not clobber the webhook plugin.
//
// TODO: why is the ServiceAccount plugin disabled? Does it need to be?
func kubeAPIServerFlags() []string {
	flags, found := []string{
		"--disable-admission-plugins=ServiceAccount",
		"--enable-admission-plugins=AlwaysAdmit",
	}, false
	for _, flag := range envtest.DefaultKubeAPIServerFlags {
		if flag == "--admission-control=AlwaysAdmit" {
			found = true
			continue
		}

		flags = append(flags, flag)
	}

	if !found {
		panic("controller-runtime have changed the default api-server flags! Please check this function is still appropriate")
	}

	return flags
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

	// We would normally delete the namespace once our tests complete however this upsets
	// the Kubernetes event recorder who is very async. If it fails to record an event- like
	// when the target resource namespace has disappeared- it will scream into stderr which
	// dominates our test output.
	//
	// It probably makes sense for us to implement our own event recorder that can be made
	// synchronous, but this takes effort that is best applied elsewhere until we know for
	// sure it's worth it.
	return name, func() {
		/* err := clientset.CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}) */
		/* Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace") */
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

// MustController cleans up tests when you just want to create a controller and don't care
// what the error might be.
func MustController(ctrl controller.Controller, err error) controller.Controller {
	Expect(err).NotTo(HaveOccurred(), "failed to create controller")
	return ctrl
}

var localhost = "localhost"

// NewServer generates a webhook server attached to the given manager that listens on an
// ephemeral localhost port. It should be used in integration tests to register webhooks
// into the api-server that point at localhost, allowing us to test the functionality
// against a local server.
//
// The value in using NewServer is to provide a simple API to install webhooks into a
// cluster and ensure that once called, both the server and webhooks are all running
// correctly.
func NewServer(mgr manager.Manager, awhs ...*admission.Webhook) *webhook.Server {
	certDir, err := ioutil.TempDir("", "theatre-integration-")
	Expect(err).NotTo(HaveOccurred(), "failed to create temporary directory")

	listen, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred(), "failed to bind ephemeral port")

	listen.Close()
	port, _ := strconv.Atoi(strings.Split(listen.Addr().String(), ":")[1])

	opts := webhook.ServerOptions{
		Port:    int32(port),
		CertDir: certDir,
		BootstrapOptions: &webhook.BootstrapOptions{
			MutatingWebhookConfigName:   "theatre-integration-webhook",
			ValidatingWebhookConfigName: "theatre-integration-webhook",
			Host: &localhost,
		},
	}

	svr, err := webhook.NewServer("theatre-integration", mgr, opts)
	Expect(err).NotTo(HaveOccurred(), "failed to create webhook server")

	// This works around a bug in the controller-runtime where we'll segfault if we haven't
	// set a Service for our webhook server and also have no NamespaceSelector. It's a safe
	// tweak as it would be very unusual for us to want control-plane events, so we can
	// probably keep this around without issue until the bug is fixed.
	whs := []webhook.Webhook{}
	for _, awh := range awhs {
		if awh.NamespaceSelector == nil {
			awh.NamespaceSelector = &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "control-plane",
						Operator: metav1.LabelSelectorOpDoesNotExist,
					},
				},
			}
		}

		whs = append(whs, awh)
	}

	err = svr.Register(whs...)
	Expect(err).NotTo(HaveOccurred(), "failed to register webhooks with the server")

	timeout, pollInterval := 10*time.Second, 250*time.Millisecond
	connect := func() error {
		_, err := net.Dial("tcp", fmt.Sprintf("%s:%d", *svr.Host, svr.Port))
		return err
	}

	Eventually(connect, timeout, pollInterval).
		Should(
			Succeed(), "timed out waiting for webhook server to become available",
		)

	return svr
}

// MustWebhook cleans up tests when you just want to create a webhook and don't care what
// the error might be.
func MustWebhook(wh *admission.Webhook, err error) *admission.Webhook {
	Expect(err).NotTo(HaveOccurred(), "failed to create webhook")
	return wh
}

// CaptureWebhook wraps the given handler and returns a handler that, when called, will
// send a record of the request and response it received down the given channel. This can
// be used to test webhooks inside of integration tests.
func CaptureWebhook(mgr manager.Manager, inner admission.Handler) (admission.Handler, chan HandleCall) {
	// We have to use a custom decoder to get our object in an abstract sense, as the
	// admission request object is not guaranteed to have all fields populated.
	decoder := serializer.NewCodecFactory(mgr.GetScheme()).UniversalDeserializer()

	calls := make(chan HandleCall, 1) // TODO: Justify buffered channel
	captured := admission.HandlerFunc(func(ctx context.Context, req atypes.Request) atypes.Response {
		obj, _, err := decoder.Decode(req.AdmissionRequest.Object.Raw, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		resp := inner.Handle(ctx, req)
		calls <- HandleCall{
			Namespace: obj.(metav1.Object).GetNamespace(),
			Name:      obj.(metav1.Object).GetName(),
			Object:    obj,
			Request:   req,
			Response:  resp,
		}

		return resp
	})

	return captured, calls
}

// HandleCall is a record of each request received and response provided by a webhook
// handler. This can be used to assert against the behaviour of a webhook handler from
// outside of the manager.
type HandleCall struct {
	Namespace, Name string
	runtime.Object
	atypes.Request
	atypes.Response
}

func HandleResource(namespace, name string) types.GomegaMatcher {
	return MatchFields(IgnoreExtras, Fields{"Namespace": Equal(namespace), "Name": Equal(name)})
}

// CaptureReconcile wraps the given reconciler and returns a channel that emits all
// reconciliation calls that the inner reconciler has received.
func CaptureReconcile(inner reconcile.Reconciler) (reconcile.Reconciler, chan ReconcileCall) {
	calls := make(chan ReconcileCall)
	captured := reconcile.Func(func(req reconcile.Request) (reconcile.Result, error) {
		result, err := inner.Reconcile(req)
		calls <- ReconcileCall{
			Namespace: req.Namespace,
			Name:      req.Name,
			Request:   req,
			Result:    result,
			Error:     err,
		}

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
