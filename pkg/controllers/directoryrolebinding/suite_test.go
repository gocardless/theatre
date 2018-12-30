package directoryrolebinding

import (
	"context"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/lawrencejones/theatre/pkg/integration"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	cfg *rest.Config
	env *envtest.Environment

	adminRole = &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin",
			Namespace: "default",
		},
		Rules: []rbacv1.PolicyRule{
			rbacv1.PolicyRule{
				APIGroups: []string{rbacv1.APIGroupAll},
				Resources: []string{rbacv1.ResourceAll},
				Verbs:     []string{rbacv1.VerbAll},
			},
		},
	}
)

var _ = BeforeSuite(func() {
	cfg, env = integration.StartAPIServer("../../../config/crds")

	c, _ := client.New(cfg, client.Options{})
	Expect(c.Create(context.TODO(), adminRole)).NotTo(
		HaveOccurred(), "failed to create test 'admin' Role",
	)
})

var _ = AfterSuite(func() {
	env.Stop()
})

func TestSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/controllers/directoryrolebinding")
}
