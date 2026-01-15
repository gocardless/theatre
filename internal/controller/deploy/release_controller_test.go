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

	Context("cullReleases", func() {
		It("Should not delete any inactive releases when count is below or equals limit", func() {
			testNs := setupTestNamespace(ctx)
			// Create 3 inactive releases (below limit of 3)
			for i := 0; i < 3; i++ {
				time := time.Now().Add(-1 * time.Hour)
				createRelease(ctx, testNs, "trim", &time)
			}

			type counts struct {
				Active int
				Total  int
			}
			Eventually(func() counts {
				// Verify all releases still exist and only one is active
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases,
					client.InNamespace(testNs),
					client.MatchingFields(map[string]string{
						"config.targetName": "trim",
					}),
				)).To(Succeed())

				activeReleasesCount := 0
				for _, r := range releases.Items {
					if r.IsConditionActive() {
						activeReleasesCount++
					}
				}

				return counts{
					Active: activeReleasesCount,
					Total:  len(releases.Items),
				}
			}, "5s", "500ms").Should(Equal(counts{Active: 1, Total: 3}))
		})

		It("Should delete oldest inactive release and preserve the newest when limit is exceeded", func() {
			testNs := setupTestNamespace(ctx)
			// Create 4 releases (1 over limit of 3)
			releases := make([]*v1alpha1.Release, 4)
			newestInactiveTime := time.Now()
			for i := 0; i < 4; i++ {
				endTime := time.Now().Add(-time.Duration(i) * time.Hour)
				releases[i] = createRelease(ctx, testNs, "target-trim-3", &endTime)

				if i == 1 {
					// the second release is the newest inactive
					newestInactiveTime = endTime
				}

				// time.Sleep(1 * time.Second)
			}
			// the last one is the oldest
			oldestInactive := releases[3]

			type counts struct {
				Active              int
				NewestInactiveFound bool
				OldestInactiveFound bool
				Total               int
			}
			// Verify only 3 releases remain
			Eventually(func() counts {
				releaseList := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releaseList,
					client.InNamespace(testNs),
					client.MatchingFields(map[string]string{
						"config.targetName": "target-trim-3",
					}),
				)).To(Succeed())

				activeReleasesCount := 0
				newestInactiveFound := false
				oldestInactiveFound := false
				for _, r := range releaseList.Items {
					if r.IsConditionActive() {
						activeReleasesCount++
					}

					if r.Status.DeploymentEndTime.Time.Equal(oldestInactive.Status.DeploymentEndTime.Time) {
						oldestInactiveFound = true
					}
					// validates that the newest release was indeed preserved
					if r.Status.DeploymentEndTime.Time.Unix() == newestInactiveTime.Unix() {
						newestInactiveFound = true
					}
				}

				return counts{
					Active:              activeReleasesCount,
					NewestInactiveFound: newestInactiveFound,
					OldestInactiveFound: oldestInactiveFound,
					Total:               len(releaseList.Items),
				}
			}, "10s", "500ms").Should(Equal(counts{Active: 1, NewestInactiveFound: true, OldestInactiveFound: false, Total: 3}))
		})

		It("Should not affect releases in different namespaces", func() {
			testNs := setupTestNamespace(ctx)
			otherNs := setupTestNamespace(ctx)

			// Create releases in different namespace
			otherNsRelease := generateRelease("target-trim-6")
			otherNsRelease.Namespace = otherNs
			Expect(k8sClient.Create(ctx, otherNsRelease)).To(Succeed())

			// Create 4 inactive releases in testNs namespace
			releases := make([]*v1alpha1.Release, 4)
			for i := 0; i < 4; i++ {
				releases[i] = createRelease(ctx, testNs, "target-trim-6", nil)
			}

			// Wait for all releases to be indexed as inactive
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace(testNs),
					client.MatchingFields(map[string]string{
						"config.targetName": "target-trim-6",
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}, "10s", "500ms").Should(Equal(3))

			// Verify release in other namespace still exists
			fetchedRelease := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      otherNsRelease.Name,
				Namespace: otherNs,
			}, fetchedRelease)).To(Succeed())
		})

		It("Should not affect inactive releases with different targets", func() {
			testNs := setupTestNamespace(ctx)
			// Create 3 inactive releases for target-trim-7
			releases := make([]*v1alpha1.Release, 3)
			for i := 0; i < 3; i++ {
				releases[i] = createRelease(ctx, testNs, "target-trim-7", nil)
			}

			// Wait for all releases to be indexed as inactive
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace(testNs),
					client.MatchingFields(map[string]string{
						"config.targetName": "target-trim-7",
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}, "5s", "100ms").Should(Equal(3))

			// Create 4 inactive releases for different-target
			releases = make([]*v1alpha1.Release, 4)
			for i := 0; i < 4; i++ {
				releases[i] = createRelease(ctx, testNs, "different-target", nil)
			}

			// Verify different-target releases are unaffected
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releaseList,
					client.InNamespace(testNs),
					client.MatchingFields(map[string]string{
						"config.targetName": "different-target",
					}),
				)).To(Succeed())

				return len(releaseList.Items)
			}, "5s", "100ms").Should(Equal(3))

			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releaseList,
					client.InNamespace(testNs),
					client.MatchingFields(map[string]string{
						"config.targetName": "target-trim-7",
					}),
				)).To(Succeed())

				return len(releaseList.Items)
			}, "5s", "100ms").Should(Equal(3))
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

		It("Should cull inactive releases when limit is exceeded", func() {
			testNs := setupTestNamespace(ctx)
			targetName := "reconcile-cull-target"
			// Create multiple inactive releases
			releases := make([]*v1alpha1.Release, 4)
			for i := 0; i < 4; i++ {
				endTime := time.Now().Add(-time.Duration(i) * time.Hour)
				releases[i] = createRelease(ctx, testNs, targetName, &endTime)
			}

			// Verify that culling happened
			// Culling only operates on releases with Active=False, not Unknown
			// So we expect exactly 3 releases with Active=False after culling
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace(testNs),
					client.MatchingFields(map[string]string{
						"config.targetName": targetName,
						// "status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return -1
				}
				return len(releaseList.Items)
			}, "5s", "100ms").Should(Equal(3))
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
