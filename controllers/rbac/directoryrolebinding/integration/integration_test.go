package integration

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	rbacv1alpha1 "github.com/gocardless/theatre/v3/apis/rbac/v1alpha1"
	"github.com/google/uuid"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func newAdminRole(namespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin",
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{rbacv1.APIGroupAll},
				Resources: []string{rbacv1.ResourceAll},
				Verbs:     []string{rbacv1.VerbAll},
			},
		},
	}
}

func newGoogleGroup(name string) rbacv1.Subject {
	return rbacv1.Subject{
		APIGroup: rbacv1alpha1.GroupVersion.Group,
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

var _ = Describe("Reconciler", func() {
	var (
		namespaceName string
		labels        map[string]string
	)

	BeforeEach(func() {
		namespaceName = uuid.New().String()

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespaceName,
			},
		}

		labels = map[string]string{
			"repo":        "foo-repo",
			"environment": "foo-env",
		}

		By("Creating test namespace: " + namespaceName)
		Expect(mgr.GetClient().Create(context.TODO(), namespace)).NotTo(
			HaveOccurred(), "failed to create test namespace",
		)

		By("Creating fixture 'admin' role")
		Expect(mgr.GetClient().Create(context.TODO(), newAdminRole(namespaceName))).NotTo(
			HaveOccurred(), "failed to create test 'admin' Role",
		)
	})

	Context("With all@ and platform@", func() {
		It("Manages DirectoryRoleBindings", func() {
			By("Creating DirectoryRoleBinding with empty subject list")
			drb := &rbacv1alpha1.DirectoryRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: namespaceName,
					Labels:    labels,
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

			// Wait to ensure that the apiserver has time to create DirectoryRoleBinding
			time.Sleep(1 * time.Second)

			By("Validate associated RoleBinding exists")
			rb := &rbacv1.RoleBinding{}
			identifier := client.ObjectKeyFromObject(drb)
			err := mgr.GetClient().Get(context.TODO(), identifier, rb)

			Expect(err).NotTo(HaveOccurred(), "failed to find associated RoleBinding for DirectoryRoleBinding")
			Expect(rb.RoleRef).To(Equal(drb.Spec.RoleRef), "associated RoleBinding should reference same Role as DRB")
			Expect(rb.Subjects).To(BeEmpty(), "initial RoleBinding should contain no subjects")

			Expect(rb.ObjectMeta.Labels).To(Equal(labels), "associated RoleBinding should have the same labels as DRB")

			By("Update subject with groups and single user")
			drb.Spec.Subjects = []rbacv1.Subject{
				newGoogleGroup("platform@gocardless.com"),
				newGoogleGroup("all@gocardless.com"),
				newUser("manuel@gocardless.com"),
			}

			err = mgr.GetClient().Update(context.TODO(), drb)
			Expect(err).NotTo(HaveOccurred(), "failed to update DirectoryRoleBinding")

			By("Refresh RoleBinding")
			err = mgr.GetClient().Get(context.TODO(), identifier, rb)
			Expect(err).NotTo(HaveOccurred(), "failed to get RoleBinding")

			Eventually(func() []rbacv1.Subject {
				mgr.GetClient().Get(context.TODO(), identifier, rb)
				return rb.Subjects
			}).Should(HaveLen(3))

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
