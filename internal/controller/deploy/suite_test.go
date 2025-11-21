package deploy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	ctx        context.Context
	cancel     context.CancelFunc
	testEnv    *envtest.Environment
	cfg        *rest.Config
	k8sClient  client.Client
	reconciler *ReleaseReconciler
)

func TestDeploy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "controllers/deploy")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	// var err error
	err := deployv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	manager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).NotTo(HaveOccurred())

	k8sClient = manager.GetClient()

	reconciler = &ReleaseReconciler{
		Client:               k8sClient,
		Scheme:               scheme.Scheme,
		Log:                  logr.Discard(),
		MaxReleasesPerTarget: 5,
	}

	err = reconciler.SetupWithManager(ctx, manager)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		Expect(manager.Start(ctx)).To(Succeed())
	}()

	err = k8sClient.Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "releases",
		},
	})
	Expect(err).NotTo(HaveOccurred())

})

var _ = AfterSuite(func() {
	By("tearing down test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
