package integration

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	rbacv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/lawrencejones/theatre/pkg/integration"
	"github.com/lawrencejones/theatre/pkg/rbac/directoryrolebinding"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	timeout = 10 * time.Second
)

func newAdminRole(namespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin",
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			rbacv1.PolicyRule{
				APIGroups: []string{rbacv1.APIGroupAll},
				Resources: []string{rbacv1.ResourceAll},
				Verbs:     []string{rbacv1.VerbAll},
			},
		},
	}
}

func newGoogleGroup(name string) rbacv1.Subject {
	return rbacv1.Subject{
		APIGroup: rbacv1alpha1.GroupName,
		Kind:     rbacv1alpha1.GoogleGroupKind,
		Name:     name,
	}
}

func newUser(name string) rbacv1.Subject {
	return rbacv1.Subject{
		APIGroup: rbacv1.GroupName,
		Kind:     rbacv1.UserKind,
		Name:     name,
	}
}

var _ = Describe("DirectoryRoleBindingReconciler", func() {
	var (
		ctx       context.Context
		cancel    func()
		namespace string
		teardown  func()
		mgr       manager.Manager
		calls     chan integration.ReconcileCall
		groups    map[string][]string
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
		namespace, teardown = integration.CreateNamespace(clientset)
		mgr = integration.StartTestManager(ctx, cfg)
		groups = map[string][]string{}

		integration.MustController(
			directoryrolebinding.Add(
				ctx,
				kitlog.NewLogfmtLogger(GinkgoWriter),
				mgr,
				directoryrolebinding.NewFakeDirectory(groups),
				time.Duration(0), // don't test our caching/re-enqueue here
				func(opt *controller.Options) {
					opt.Reconciler, calls = integration.CaptureReconcile(
						opt.Reconciler,
					)
				},
			),
		)

		By("Creating fixture 'admin' role")
		Expect(mgr.GetClient().Create(ctx, newAdminRole(namespace))).NotTo(
			HaveOccurred(), "failed to create test 'admin' Role",
		)
	})

	AfterEach(func() {
		cancel()
		teardown()
	})

	Context("With all@ and platform@", func() {
		BeforeEach(func() {
			groups["all@gocardless.com"] = []string{
				"lawrence@gocardless.com",
			}
			groups["platform@gocardless.com"] = []string{
				"lawrence@gocardless.com",
				"chris@gocardless.com",
			}
		})

		It("Manages DirectoryRoleBindings", func() {
			By("Creating DirectoryRoleBinding with empty subject list")
			drb := &rbacv1alpha1.DirectoryRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: namespace,
				},
				Spec: rbacv1alpha1.DirectoryRoleBindingSpec{
					Subjects: []rbacv1.Subject{},
					RoleRef: rbacv1.RoleRef{
						APIGroup: rbacv1.GroupName,
						Kind:     "Role",
						Name:     "admin",
					},
				},
			}

			Expect(mgr.GetClient().Create(context.TODO(), drb)).NotTo(
				HaveOccurred(), "failed to create 'foo' DirectoryRoleBinding",
			)

			By("Verifying reconcile has been triggered successfully")
			for i := 0; i < 2; i++ { // twice for the follow-up watch of the RoleBinding
				Eventually(calls, timeout).Should(
					Receive(
						integration.ReconcileResourceSuccess(namespace, "foo"),
					),
				)
			}

			By("Validate associated RoleBinding exists")
			rb := &rbacv1.RoleBinding{}
			identifier, _ := client.ObjectKeyFromObject(drb)
			err := mgr.GetClient().Get(context.TODO(), identifier, rb)

			Expect(err).NotTo(HaveOccurred(), "failed to find associated RoleBinding for DirectoryRoleBinding")
			Expect(rb.RoleRef).To(Equal(drb.Spec.RoleRef), "associated RoleBinding should reference same Role as DRB")
			Expect(rb.Subjects).To(BeEmpty(), "initial RoleBinding should contain no subjects")

			By("Update subject with groups and single user")
			drb.Spec.Subjects = []rbacv1.Subject{
				newGoogleGroup("platform@gocardless.com"),
				newGoogleGroup("all@gocardless.com"),
				newUser("manuel@gocardless.com"),
			}

			err = mgr.GetClient().Update(context.TODO(), drb)
			Expect(err).NotTo(HaveOccurred(), "failed to update DirectoryRoleBinding")

			By("Wait for successful reconciliation")
			Eventually(calls, timeout).Should(
				Receive(
					integration.ReconcileResourceSuccess(namespace, "foo"),
				),
				"expected to successfully reconcile",
			)

			By("Refresh RoleBinding")
			err = mgr.GetClient().Get(context.TODO(), identifier, rb)
			Expect(err).NotTo(HaveOccurred(), "failed to get RoleBinding")

			By("Verify RoleBinding subjects have been updated with group members")
			Expect(rb.Subjects).To(
				ConsistOf(
					newUser("lawrence@gocardless.com"),
					newUser("chris@gocardless.com"),
					newUser("manuel@gocardless.com"),
				),
			)
		})
	})
})
