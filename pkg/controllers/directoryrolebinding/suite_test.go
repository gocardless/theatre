package directoryrolebinding

import (
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/lawrencejones/theatre/pkg/integration"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	cfg       *rest.Config
	env       *envtest.Environment
	clientset *kubernetes.Clientset
)

var _ = BeforeSuite(func() {
	cfg, env, clientset = integration.StartAPIServer("../../../config/crds")
})

var _ = AfterSuite(func() {
	env.Stop()
})

func TestSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/controllers/directoryrolebinding")
}
