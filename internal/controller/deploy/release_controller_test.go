package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ReleaseController", func() {
	Context("handleAnnotations", func() {
		It("Should update status.deploymentStartTime and status.deploymentEndTime when annotated", func() {
			testNs := setupTestNamespace(ctx)
			stringTimestamp := "2025-12-08T14:42:00Z"
			metav1Timestamp := getMetaV1Timestamp(stringTimestamp)

			release := createRelease(ctx, testNs, "test-target", nil)

			fetchedObj := &v1alpha1.Release{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release.Name, Namespace: testNs}, fetchedObj); err != nil {
					return false
				}
				return fetchedObj.Status.DeploymentEndTime.IsZero() && fetchedObj.Status.DeploymentStartTime.IsZero()
			}, "5s", "100ms").Should(BeTrue())

			By("Setting the deployment start and end time")
			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: stringTimestamp,
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime:   stringTimestamp,
			}
			Expect(k8sClient.Update(ctx, fetchedObj)).To(Succeed())

			// Verify the status was persisted to the cluster
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release.Name, Namespace: testNs}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentStartTime.Unix() == metav1Timestamp.Unix() && updatedObj.Status.DeploymentEndTime.Unix() == metav1Timestamp.Unix()
			}, "5s", "100ms").Should(BeTrue())
		})

		It("Should not set deployment start/end times if annotation is invalid", func() {
			testNs := setupTestNamespace(ctx)
			release := createRelease(ctx, testNs, "test-target", nil)

			fetchedObj := &v1alpha1.Release{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release.Name, Namespace: testNs}, fetchedObj); err != nil {
					return false
				}
				return fetchedObj.Status.DeploymentEndTime.IsZero() && fetchedObj.Status.DeploymentStartTime.IsZero()
			}, "5s", "100ms").Should(BeTrue())

			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: "not-a-timestamp",
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime:   "not-a-timestamp",
			}
			Expect(k8sClient.Update(ctx, fetchedObj)).To(Succeed())

			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release.Name, Namespace: testNs}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentEndTime.IsZero() && updatedObj.Status.DeploymentStartTime.IsZero()
			}, "5s", "100ms").Should(BeTrue())
		})
	})

	Context("Reconcile", func() {
		It("Should successfully reconcile and initialize a new release", func() {
			testNs := setupTestNamespace(ctx)
			release := createRelease(ctx, testNs, "reconcile-target-1", nil)

			// Verify release is initialized with conditions
			Eventually(func() bool {
				fetchedRelease := &v1alpha1.Release{}
				err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      release.Name,
					Namespace: testNs,
				}, fetchedRelease)
				if err != nil {
					return false
				}
				return fetchedRelease.IsStatusInitialised()
			}, "5s", "500ms").Should(BeTrue())
		})

		It("Should supersede previous active releases when deployment end time is set", func() {
			testNs := setupTestNamespace(ctx)
			// Create and activate an older release with deployment end time
			targetName := "reconcile-target-3"

			oldTime := time.Now().Add(-1 * time.Hour)
			oldRelease := createRelease(ctx, testNs, targetName, &oldTime)

			// Create a new release with a later deployment end time
			newTime := time.Now().Add(1 * time.Hour)
			newRelease := createRelease(ctx, testNs, targetName, &newTime)

			// Wait for new release to be activated and old one to be superseded
			Eventually(func() bool {
				fetchedNewRelease := &v1alpha1.Release{}
				fetchedOldRelease := &v1alpha1.Release{}

				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      newRelease.Name,
					Namespace: testNs,
				}, fetchedNewRelease); err != nil {
					return false
				}

				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      oldRelease.Name,
					Namespace: testNs,
				}, fetchedOldRelease); err != nil {
					return false
				}

				return fetchedNewRelease.IsConditionActive() &&
					!fetchedOldRelease.IsConditionActive() &&
					fetchedNewRelease.Status.PreviousRelease.ReleaseRef == oldRelease.Name
			}, "5s", "500ms").Should(BeTrue())
		})

		It("Should handle releases for different targets independently", func() {
			testNs := setupTestNamespace(ctx)
			// Create releases for two different targets with deployment end times
			time1 := time.Now()
			release1 := createRelease(ctx, testNs, "target-a", &time1)

			time2 := time.Now()
			release2 := createRelease(ctx, testNs, "target-b", &time2)

			// Wait for background controller to activate both releases
			// (they shouldn't supersede each other because they're different targets)
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				r2 := &v1alpha1.Release{}

				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: testNs}, r1); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: testNs}, r2); err != nil {
					return false
				}

				return r1.IsConditionActive() && r2.IsConditionActive()
			}, "5s", "100ms").Should(BeTrue())
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

func generateNamespace() string {
	bytes := make([]byte, 4)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}
	return "test-ns-" + hex.EncodeToString(bytes)
}

func setupTestNamespace(ctx context.Context) string {
	ns := generateNamespace()
	err := k8sClient.Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	})
	Expect(err).NotTo(HaveOccurred())
	return ns
}

func generateRelease(target string) *v1alpha1.Release {
	appSHA := generateCommitSHA()
	infraSHA := generateCommitSHA()
	return &v1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: target + "-",
		},
		ReleaseConfig: v1alpha1.ReleaseConfig{
			TargetName: target,
			Revisions: []v1alpha1.Revision{
				{Name: "application-revision", ID: appSHA},
				{Name: "infrastructure-revision", ID: infraSHA},
			},
		},
	}
}

func createRelease(ctx context.Context, namespace, target string, endTime *time.Time) *v1alpha1.Release {
	release := generateRelease(target)
	release.Namespace = namespace
	if endTime != nil {
		release.Annotations = make(map[string]string)
		release.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime] = endTime.Format(time.RFC3339)
	}

	err := k8sClient.Create(ctx, release)
	Expect(err).NotTo(HaveOccurred())
	return release
}

func getMetaV1Timestamp(ts string) metav1.Time {
	timestamp, err := time.Parse(time.RFC3339, ts)
	Expect(err).NotTo(HaveOccurred())
	return metav1.NewTime(timestamp)
}
