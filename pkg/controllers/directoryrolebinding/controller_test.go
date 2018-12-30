package directoryrolebinding

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rbacv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/lawrencejones/theatre/pkg/integration"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	timeout = 10 * time.Second
)

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
		ctx    context.Context
		cancel func()
		mgr    manager.Manager
		ctrl   reconcile.Reconciler
		calls  chan integration.ReconcileCall
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
		mgr = integration.StartTestManager(ctx, cfg)

		ctrl, calls = integration.CaptureReconcile(
			newReconciler(
				ctx,
				mgr,
				kitlog.NewLogfmtLogger(GinkgoWriter),
				NewFakeDirectory(map[string][]string{
					"all@gocardless.com": []string{
						"lawrence@gocardless.com",
					},
					"platform@gocardless.com": []string{
						"lawrence@gocardless.com",
						"chris@gocardless.com",
					},
				}),
			),
		)

		_, err := add(mgr, ctrl)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		cancel()
	})

	It("Manages the group membership", func() {
		By("Creating DirectoryRoleBinding with empty subject list")
		drb := &rbacv1alpha1.DirectoryRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: "default",
			},
			Spec: rbacv1alpha1.DirectoryRoleBindingSpec{
				RoleBinding: rbacv1.RoleBinding{
					Subjects: []rbacv1.Subject{},
					RoleRef: rbacv1.RoleRef{
						APIGroup: rbacv1.GroupName,
						Kind:     "Role",
						Name:     adminRole.GetName(),
					},
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
					integration.ReconcileResourceSuccess("default/foo"),
				),
			)
		}

		By("Validate associated RoleBinding exists")
		rb := &rbacv1.RoleBinding{}
		identifier, _ := client.ObjectKeyFromObject(drb)
		err := mgr.GetClient().Get(context.TODO(), identifier, rb)

		Expect(err).NotTo(HaveOccurred(), "failed to find associated RoleBinding for DirectoryRoleBinding")
		Expect(rb.RoleRef).To(Equal(drb.Spec.RoleBinding.RoleRef), "associated RoleBinding should reference same Role as DRB")
		Expect(rb.Subjects).To(BeEmpty(), "initial RoleBinding should contain no subjects")

		By("Update subject with groups and single user")
		drb.Spec.RoleBinding.Subjects = []rbacv1.Subject{
			newGoogleGroup("platform@gocardless.com"),
			newGoogleGroup("all@gocardless.com"),
			newUser("manuel@gocardless.com"),
		}

		err = mgr.GetClient().Update(context.TODO(), drb)
		Expect(err).NotTo(HaveOccurred(), "failed to update DirectoryRoleBinding")

		By("Wait for successful reconciliation")
		Eventually(calls, timeout).Should(
			Receive(
				SatisfyAll(
					integration.ReconcileResource("default/foo"),
					integration.ReconcileSuccessfully(),
					integration.ReconcileNoRetry(),
				),
			),
			"expected to successfully reconcile default/foo",
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
