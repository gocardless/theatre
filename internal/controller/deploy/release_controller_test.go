package deploy

import (
	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		err := k8sClient.Delete(ctx, &obj)
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
			Expect(obj.Status.Phase).To(Equal(v1alpha1.PhaseActive))
			Expect(obj.Status.LastAppliedTime).ToNot(BeNil())
			Expect(obj.Status.SupersededBy).To(Equal(""))
			Expect(obj.Status.SupersededTime).To(Equal(metav1.Time{}))
		})

		It("Should set a release as superseded, and update its status", func() {
			Expect(obj).NotTo(BeNil())
			Expect(reconciler.markReleaseSuperseded(ctx, &obj, "superseded-by")).To(Succeed())
			Expect(obj.Status.Phase).To(Equal(v1alpha1.PhaseInactive))
			Expect(obj.Status.LastAppliedTime).ToNot(BeNil())
			Expect(obj.Status.SupersededBy).To(Equal("superseded-by"))
			Expect(obj.Status.SupersededTime).ToNot(BeNil())
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

			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-release", Namespace: "releases"}, &obj)).To(Succeed())
			Expect(obj.Status.Phase).To(Equal(v1alpha1.PhaseInactive))
		})
	})

})
