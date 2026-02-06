package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"time"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	deploy "github.com/gocardless/theatre/v5/internal/controller/deploy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		defaultRelease = createRelease(ctx, testNamespace, "default-target", nil)
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
			release := createRelease(ctx, testNamespace, "test-target", nil)

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

	Context("cullReleases", func() {
		BeforeEach(func() {
			// annotate namespace with max releases per target
			namespace := &v1.Namespace{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: testNamespace}, namespace)).To(Succeed())
			metav1.SetMetaDataAnnotation(&namespace.ObjectMeta, v1alpha1.AnnotationKeyReleaseLimit, "5")
			Expect(k8sClient.Update(ctx, namespace)).To(Succeed())
		})

		It("should never delete active releases", func() {
			targetName := generateTargetName()
			annotations := map[string]string{
				v1alpha1.AnnotationKeyReleaseActivate: v1alpha1.AnnotationValueReleaseActivateTrue,
			}
			createReleases(ctx, testNamespace, targetName, annotations, 6)

			// The number of releases should be 6
			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace(testNamespace), client.MatchingFields(map[string]string{deploy.IndexFieldTargetName: targetName}))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(6))

			Consistently(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace(testNamespace), client.MatchingFields(map[string]string{deploy.IndexFieldTargetName: targetName}))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(6))
		})

		It("should not delete releases that are not in the target", func() {
			otherTarget := generateTargetName()
			createReleases(ctx, testNamespace, otherTarget, nil, 3)

			targetName := generateTargetName()
			createReleases(ctx, testNamespace, targetName, nil, 6)

			// The number of releases of the first target should be 5
			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace(testNamespace), client.MatchingFields(map[string]string{deploy.IndexFieldTargetName: targetName}))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(5))

			// The number of releases in the other target should still be 3
			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace(testNamespace), client.MatchingFields(map[string]string{deploy.IndexFieldTargetName: otherTarget}))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(3))
		})

		It("should not delete releases if there are less than or equal to the max", func() {
			targetName := generateTargetName()
			createReleases(ctx, testNamespace, targetName, nil, 5)

			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace(testNamespace), client.MatchingFields(map[string]string{deploy.IndexFieldTargetName: targetName}))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(5))
		})

		It("should delete the oldest release (oldest end time) if there are more than the max", func() {
			// Create 6 releases with different end times
			targetName := generateTargetName()
			releases := createReleases(ctx, testNamespace, targetName, nil, 6)
			oldestRelease := releases[0]

			// The number of releases should be 5
			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace(testNamespace), client.MatchingFields(map[string]string{deploy.IndexFieldTargetName: targetName}))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(5))

			// The oldest release (index 0) should be deleted
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: oldestRelease.Name, Namespace: testNamespace}, &v1alpha1.Release{})
				return apierrors.IsNotFound(err)
			}).Should(BeTrue())
		})
	})
})

// Creates and returns a slice of Release objects with the specified count
// Each release has an end time annotation that is 1 hour apart
func createReleases(ctx context.Context, namespace, targetName string, extraAnnotations map[string]string, count int) []*v1alpha1.Release {
	ret := make([]*v1alpha1.Release, 0, count)
	for i := range count {
		annotations := map[string]string{
			v1alpha1.AnnotationKeyReleaseDeploymentEndTime: time.Now().Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		}
		maps.Copy(annotations, extraAnnotations)

		release := createRelease(ctx, namespace, targetName, annotations)
		ret = append(ret, release)
	}
	return ret
}

func generateTargetName() string {
	return fmt.Sprintf("test-target-%d-%d", GinkgoParallelProcess(), namespaceCounter.Add(1))
}

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
