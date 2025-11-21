package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/go-logr/logr"
	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ReleaseController", func() {

	var (
		obj v1alpha1.Release
	)

	BeforeEach(func() {
		obj = v1alpha1.Release{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-release",
				Namespace: "releases",
			},
			Spec: v1alpha1.ReleaseSpec{
				UtopiaServiceTargetRelease: "default",
				ApplicationRevision: v1alpha1.Revision{
					ID: "0367fc26ff4839002e1b27f10ae2836bbc364f08",
				},
				InfrastructureRevision: v1alpha1.Revision{
					ID: "c4823b38972027ac73d615fc6ec9ddcedad857a4",
				},
			},
		}

		err := k8sClient.Create(ctx, &obj)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		err := k8sClient.DeleteAllOf(ctx, &v1alpha1.Release{}, client.InNamespace("releases"))
		Expect(err).NotTo(HaveOccurred())
	})

	Context("Test helpers", func() {
		It("isNewRelease returns true only when phase is empty", func() {
			Expect(isNewRelease(v1alpha1.PhaseActive)).To(BeFalse())
			Expect(isNewRelease(v1alpha1.PhaseInactive)).To(BeFalse())
			Expect(isNewRelease("")).To(BeTrue())
		})
	})

	Context("Update Release Status", func() {
		It("Should set a release as active, and update its status", func() {
			Expect(obj).NotTo(BeNil())
			Expect(reconciler.markReleaseActive(ctx, &obj)).To(Succeed())

			Eventually(func() v1alpha1.ReleaseStatus {
				releaseFromClient := v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-release", Namespace: "releases"}, &releaseFromClient)).To(Succeed())
				return releaseFromClient.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseActive)),
				HaveField("LastAppliedTime", Not(BeNil())),
				HaveField("SupersededBy", Equal("")),
				HaveField("SupersededTime", Equal(metav1.Time{})),
			))
		})

		It("Should set a release as superseded, and update its status", func() {
			Expect(obj).NotTo(BeNil())
			Expect(reconciler.markReleaseSuperseded(ctx, &obj, "superseded-by")).To(Succeed())

			Eventually(func() v1alpha1.ReleaseStatus {
				releaseFromClient := v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-release", Namespace: "releases"}, &releaseFromClient))
				return releaseFromClient.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseInactive)),
				HaveField("SupersededBy", Equal("superseded-by")),
				HaveField("SupersededTime", Not(BeNil())),
			))
		})
	})

	Context("Supersede Previous Releases", func() {

		It("Should mark all other releases as inactive", func() {
			Expect(reconciler.markReleaseActive(ctx, &obj)).To(Succeed())

			newRelease := v1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-release-2",
					Namespace: "releases",
				},
				Spec: v1alpha1.ReleaseSpec{
					UtopiaServiceTargetRelease: "default",
					ApplicationRevision: v1alpha1.Revision{
						ID: "0367fc26ff4839002e1b27f10ae2836bbc364f08",
					},
					InfrastructureRevision: v1alpha1.Revision{
						ID: "c4823b38972027ac73d615fc6ec9ddcedad857a4",
					},
				},
			}

			err := k8sClient.Create(ctx, &newRelease)
			Expect(err).NotTo(HaveOccurred())

			Expect(reconciler.markReleaseActive(ctx, &newRelease)).To(Succeed())

			err = reconciler.supersedePreviousReleases(ctx, &newRelease)
			Expect(err).NotTo(HaveOccurred())

			Expect(newRelease.Status.Phase).To(Equal(v1alpha1.PhaseActive))

			Eventually(func() v1alpha1.ReleaseStatus {
				inactiveRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-release", Namespace: "releases"}, inactiveRelease)).To(Succeed())
				return inactiveRelease.Status
			}).Should(HaveField("Phase", Equal(v1alpha1.PhaseInactive)))
		})
	})

	Context("Cull releases", func() {
		It("Should keep the number of releases below the configured limit", func() {
			Expect(createRelease(ctx, "default")).ToNot(BeNil())
			Expect(createRelease(ctx, "default")).ToNot(BeNil())
			Expect(createRelease(ctx, "default")).ToNot(BeNil())
			release := createRelease(ctx, "default")

			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "releases"}}, release)
			Expect(err).NotTo(HaveOccurred())

			// Verify that only 3 releases exist (culling happened)
			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace("releases"))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(3))
		})
	})
})

func generateCommitSHA() string {
	bytes := make([]byte, 20)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}

func generateRelease(target string) *v1alpha1.Release {
	appSHA := generateCommitSHA()
	infraSHA := generateCommitSHA()
	return &v1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name:      target + "-" + infraSHA[:7] + "-" + appSHA[:7],
			Namespace: "releases",
		},
		Spec: v1alpha1.ReleaseSpec{
			UtopiaServiceTargetRelease: target,
			ApplicationRevision: v1alpha1.Revision{
				ID: appSHA,
			},
			InfrastructureRevision: v1alpha1.Revision{
				ID: infraSHA,
			},
		},
	}
}

func createRelease(ctx context.Context, target string) *v1alpha1.Release {
	release := generateRelease(target)
	err := k8sClient.Create(ctx, release)
	Expect(err).NotTo(HaveOccurred())
	return release
}
