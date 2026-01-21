package integration

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ReleaseController", func() {
	var (
		testNamespace  string
		defaultRelease *v1alpha1.Release
		k8sClient      client.Client
	)

	BeforeEach(func() {
		testNamespace = setupTestNamespace(ctx)
		defaultRelease = createRelease(ctx, testNamespace, "default-target")
		k8sClient = mgr.GetClient()
	})

	Context("handleAnnotations", func() {
		var fetchedRelease *v1alpha1.Release

		JustBeforeEach(func() {
			fetchedRelease = &v1alpha1.Release{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, fetchedRelease)
				if err != nil {
					return err
				}

				if !fetchedRelease.IsStatusInitialised() {
					return fmt.Errorf("release hasn't been initialised by the reconciler")
				}
				return nil
			}, "5s", "100ms").Should(Succeed())
		})

		Context("AnnotationKeyReleaseDeploymentStartTime", func() {
			It("should set status.deploymentStartTime when annotation is added", func() {
				stringTimestamp := "2025-12-08T14:42:00Z"
				metav1Timestamp := getMetaV1Timestamp(stringTimestamp)

				By("Setting the deployment start time annotation")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseDeploymentStartTime: stringTimestamp,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.DeploymentStartTime.Unix() == metav1Timestamp.Unix()
				}, "5s", "100ms").Should(BeTrue())
			})

			It("should clear status.deploymentStartTime when annotation is removed", func() {
				stringTimestamp := "2025-12-08T14:42:00Z"

				By("Setting the deployment start time annotation")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseDeploymentStartTime: stringTimestamp,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return !updatedObj.Status.DeploymentStartTime.IsZero()
				}, "5s", "100ms").Should(BeTrue())

				By("Removing the annotation")
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, fetchedRelease)).To(Succeed())
				delete(fetchedRelease.Annotations, v1alpha1.AnnotationKeyReleaseDeploymentStartTime)
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.DeploymentStartTime.IsZero()
				}, "5s", "100ms").Should(BeTrue())
			})

			It("should not update status.deploymentStartTime when annotation has invalid timestamp", func() {
				By("Setting the deployment start time annotation with an invalid timestamp")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseDeploymentStartTime: "not-a-valid-timestamp",
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Consistently(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.DeploymentStartTime.IsZero()
				}, "2s", "100ms").Should(BeTrue())
			})
		})

		Context("AnnotationKeyReleaseDeploymentEndTime", func() {
			It("should set status.deploymentEndTime when annotation is added", func() {
				stringTimestamp := "2025-12-08T15:30:00Z"
				metav1Timestamp := getMetaV1Timestamp(stringTimestamp)

				By("Setting the deployment end time annotation")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseDeploymentEndTime: stringTimestamp,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.DeploymentEndTime.Unix() == metav1Timestamp.Unix()
				}, "5s", "100ms").Should(BeTrue())
			})

			It("should clear status.deploymentEndTime when annotation is removed", func() {
				stringTimestamp := "2025-12-08T15:30:00Z"

				By("Setting the deployment end time annotation")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseDeploymentEndTime: stringTimestamp,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return !updatedObj.Status.DeploymentEndTime.IsZero()
				}, "5s", "100ms").Should(BeTrue())

				By("Removing the annotation")
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, fetchedRelease)).To(Succeed())
				delete(fetchedRelease.Annotations, v1alpha1.AnnotationKeyReleaseDeploymentEndTime)
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.DeploymentEndTime.IsZero()
				}, "5s", "100ms").Should(BeTrue())
			})

			It("should not update status.deploymentEndTime when annotation has invalid timestamp", func() {
				By("Setting the deployment end time annotation with an invalid timestamp")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseDeploymentEndTime: "invalid-timestamp-format",
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Consistently(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.DeploymentEndTime.IsZero()
				}, "2s", "100ms").Should(BeTrue())
			})
		})

		Context("AnnotationKeyReleaseActivate", func() {
			It("should set status.conditions.active to true when annotation is added with value 'true'", func() {
				By("Setting the activate annotation")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseActivate: v1alpha1.AnnotationValueReleaseActivateTrue,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.IsConditionActiveTrue()
				}, "5s", "100ms").Should(BeTrue())
			})

			It("should set status.conditions.active to false when annotation is removed", func() {
				By("Setting the annotation to activate")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseActivate: v1alpha1.AnnotationValueReleaseActivateTrue,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.IsConditionActiveTrue()
				}, "5s", "100ms").Should(BeTrue())

				By("Removing the annotation")
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, fetchedRelease)).To(Succeed())
				delete(fetchedRelease.Annotations, v1alpha1.AnnotationKeyReleaseActivate)
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return meta.IsStatusConditionFalse(updatedObj.Status.Conditions, v1alpha1.ReleaseConditionActive)
				}, "5s", "100ms").Should(BeTrue())
			})

			It("should not activate when annotation value is not 'true'", func() {
				By("Setting the activate annotation to false")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseActivate: "false",
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Consistently(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return meta.IsStatusConditionPresentAndEqual(updatedObj.Status.Conditions, v1alpha1.ReleaseConditionActive, metav1.ConditionUnknown)
				}, "2s", "100ms").Should(BeTrue())
			})
		})

		Context("AnnotationKeyReleasePreviousRelease", func() {
			It("should set status.previousRelease.releaseRef when annotation is added", func() {
				previousReleaseName := "previous-release-abc123"

				By("Setting the previous release annotation")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleasePreviousRelease: previousReleaseName,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.PreviousRelease.ReleaseRef == previousReleaseName
				}, "5s", "100ms").Should(BeTrue())
			})

			It("should clear status.previousRelease.releaseRef when annotation is removed", func() {
				previousReleaseName := "previous-release-xyz789"

				By("Setting the previous release annotation")
				fetchedRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleasePreviousRelease: previousReleaseName,
				}
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.PreviousRelease.ReleaseRef == previousReleaseName
				}, "5s", "100ms").Should(BeTrue())

				By("Removing the previous release annotation")
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, fetchedRelease)).To(Succeed())
				delete(fetchedRelease.Annotations, v1alpha1.AnnotationKeyReleasePreviousRelease)
				Expect(k8sClient.Update(ctx, fetchedRelease)).To(Succeed())

				Eventually(func() bool {
					updatedObj := &v1alpha1.Release{}
					Expect(k8sClient.Get(ctx, client.ObjectKey{Name: defaultRelease.Name, Namespace: testNamespace}, updatedObj)).To(Succeed())
					return updatedObj.Status.PreviousRelease.ReleaseRef == ""
				}, "5s", "100ms").Should(BeTrue())
			})
		})
	})

	Context("Reconcile", func() {
		It("Should initialise status of new releases", func() {
			release := createRelease(ctx, testNamespace, "test-target")

			Eventually(func() bool {
				fetchedRelease := &v1alpha1.Release{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: release.Name, Namespace: testNamespace}, fetchedRelease)
				if err != nil {
					return false
				}

				return fetchedRelease.IsStatusInitialised() && fetchedRelease.Status.Signature != ""
			}).Should(BeTrue())
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

func getMetaV1Timestamp(ts string) metav1.Time {
	timestamp, err := time.Parse(time.RFC3339, ts)
	Expect(err).NotTo(HaveOccurred())
	return metav1.NewTime(timestamp)
}
