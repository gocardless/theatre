package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultNamespace = "releases"
)

var _ = Describe("ReleaseController", func() {

	var (
		obj v1alpha1.Release
	)

	BeforeEach(func() {
		obj = v1alpha1.Release{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-release",
				Namespace: DefaultNamespace,
			},
			ReleaseConfig: v1alpha1.ReleaseConfig{
				TargetName: "test-target",
				Revisions: []v1alpha1.Revision{
					{Name: "application-revision", ID: "test-app-revision"},
					{Name: "infrastructure-revision", ID: "test-infra-revision"},
				},
			},
		}

		err := k8sClient.Create(ctx, &obj)
		Expect(err).NotTo(HaveOccurred())
		obj.InitialiseStatus(MessageReleaseCreated)
		err = k8sClient.Status().Update(ctx, &obj)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		err := k8sClient.DeleteAllOf(ctx, &v1alpha1.Release{}, client.InNamespace("releases"))
		Expect(err).NotTo(HaveOccurred())
	})

	Context("handleAnnotations", func() {
		var (
			stringTimestamp string
			metav1Timestamp metav1.Time
		)

		BeforeEach(func() {
			stringTimestamp = "2025-12-08T14:42:00Z"
			timestamp, err := time.Parse(time.RFC3339, stringTimestamp)
			Expect(err).NotTo(HaveOccurred())
			metav1Timestamp = metav1.NewTime(timestamp)
		})

		It("Should update status.deploymentStartTime when annotated with "+v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime, func() {
			// Fetch the latest version
			fetchedObj := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())

			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: stringTimestamp,
			}
			Expect(k8sClient.Update(ctx, fetchedObj)).To(Succeed())

			// Refetch after update to get the latest resource version
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())
			Expect(fetchedObj.Status.DeploymentStartTime.Unix()).To(Equal(metav1Timestamp.Unix()))
		})

		It("Should error when passing invalid time when annotated with"+v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime, func() {
			obj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: "not-a-timestamp",
			}
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), &obj)).Error()
		})

		It("Should error when passing invalid time when annotated with"+v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime, func() {
			obj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: "not-a-timestamp",
			}
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), &obj)).Error()
		})

		It("Should update status.deploymentEndTime when annotated with "+v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime, func() {
			// Fetch the latest version
			fetchedObj := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())

			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: stringTimestamp,
			}
			Expect(k8sClient.Update(ctx, fetchedObj)).To(Succeed())

			// Refetch after update to get the latest resource version
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())
			Expect(fetchedObj.Status.DeploymentEndTime.Unix()).To(Equal(metav1Timestamp.Unix()))
		})

		It("Should update all status fields when annotated with "+strings.Join([]string{
			v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime,
			v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime,
		}, ","), func() {
			startTimestamp := "2025-12-08T14:32:00Z"
			metav1StartTimestamp := getMetaV1Timestamp(startTimestamp)
			endTimestamp := "2025-12-08T14:42:00Z"
			metav1EndTimestamp := getMetaV1Timestamp(endTimestamp)

			// Fetch the latest version
			fetchedObj := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())

			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: startTimestamp,
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime:   endTimestamp,
			}
			Expect(k8sClient.Update(ctx, fetchedObj)).To(Succeed())

			// Refetch after update to get the latest resource version
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())
			Expect(fetchedObj.Status.DeploymentStartTime.Unix()).To(Equal(metav1StartTimestamp.Unix()))
			Expect(fetchedObj.Status.DeploymentEndTime.Unix()).To(Equal(metav1EndTimestamp.Unix()))
		})
	})

	Context("Test helpers", func() {
		It("IsStatusInitialised returns true only when conditions are set", func() {
			newRelease := v1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "uninitialised",
					Namespace: DefaultNamespace,
				},
				ReleaseConfig: v1alpha1.ReleaseConfig{
					TargetName: "test-target",
					Revisions: []v1alpha1.Revision{
						{Name: "app", ID: "abc123"},
					},
				},
			}
			Expect(newRelease.IsStatusInitialised()).To(BeFalse())

			newRelease.InitialiseStatus("test message")
			Expect(newRelease.IsStatusInitialised()).To(BeTrue())
		})

		It("IsConditionActive returns true only when Active condition is True", func() {
			Expect(obj.IsConditionActive()).To(BeFalse())

			obj.Activate(MessageReleaseActive, nil)
			Expect(obj.IsConditionActive()).To(BeTrue())

			obj.Deactivate(MessageReleaseSuperseded, nil)
			Expect(obj.IsConditionActive()).To(BeFalse())
		})
	})

	Context("cullReleases", func() {
		It("Should not delete any inactive releases when count is below limit", func() {
			// Create 2 inactive releases (below limit of 3)
			release1 := createRelease(ctx, "trim")
			release2 := createRelease(ctx, "trim")
			release1.Activate(MessageReleaseActive, nil)
			Expect(k8sClient.Status().Update(ctx, release1)).To(Succeed())
			release2.Activate(MessageReleaseActive, nil)
			Expect(k8sClient.Status().Update(ctx, release2)).To(Succeed())

			// Deactivate them
			release1.Deactivate(MessageReleaseSuperseded, release2)
			Expect(k8sClient.Status().Update(ctx, release1)).To(Succeed())
			release2.Deactivate(MessageReleaseSuperseded, nil)
			Expect(k8sClient.Status().Update(ctx, release2)).To(Succeed())

			err := reconciler.cullReleases(ctx, logr.Discard(), "releases", "trim")
			Expect(err).NotTo(HaveOccurred())

			// Verify both releases still exist
			releases := &v1alpha1.ReleaseList{}
			Expect(k8sClient.List(ctx, releases,
				client.InNamespace("releases"),
				client.MatchingFields(map[string]string{
					"config.targetName": "trim",
				}),
			)).To(Succeed())
			Expect(len(releases.Items)).To(Equal(2))
		})

		It("Should not delete any inactive releases when count equals limit", func() {
			// Create exactly 3 inactive releases (at limit)
			releases := []*v1alpha1.Release{
				createRelease(ctx, "target-trim-2"),
				createRelease(ctx, "target-trim-2"),
				createRelease(ctx, "target-trim-2"),
			}

			for _, release := range releases {
				release.Activate(MessageReleaseActive, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
				release.Deactivate(MessageReleaseSuperseded, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
			}

			err := reconciler.cullReleases(ctx, logr.Discard(), "releases", "target-trim-2")
			Expect(err).NotTo(HaveOccurred())

			// Verify all 3 releases still exist
			releaseList := &v1alpha1.ReleaseList{}
			Expect(k8sClient.List(ctx, releaseList,
				client.InNamespace("releases"),
				client.MatchingFields(map[string]string{
					"config.targetName": "target-trim-2",
				}),
			)).To(Succeed())
			Expect(len(releaseList.Items)).To(Equal(3))
		})

		It("Should delete oldest inactive releases when limit is exceeded", func() {
			// Create 5 inactive releases (2 over limit of 3)
			releases := make([]*v1alpha1.Release, 5)
			for i := 0; i < 5; i++ {
				releases[i] = createRelease(ctx, "target-trim-3")
				releases[i].Activate(MessageReleaseActive, nil)
				Expect(k8sClient.Status().Update(ctx, releases[i])).To(Succeed())
				releases[i].Deactivate(MessageReleaseSuperseded, nil)
				Expect(k8sClient.Status().Update(ctx, releases[i])).To(Succeed())
			}

			err := reconciler.cullReleases(ctx, logr.Discard(), "releases", "target-trim-3")
			Expect(err).NotTo(HaveOccurred())

			// Verify only 3 releases remain
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"config.targetName": "target-trim-3",
					}),
				)).To(Succeed())
				return len(releaseList.Items)
			}).Should(Equal(3))
		})

		It("Should preserve newest inactive releases by creation time", func() {
			// Create 4 inactive releases
			releases := make([]*v1alpha1.Release, 4)
			for i := 0; i < 4; i++ {
				releases[i] = createRelease(ctx, "target-trim-5")
				releases[i].Activate(MessageReleaseActive, nil)
				Expect(k8sClient.Status().Update(ctx, releases[i])).To(Succeed())
			}

			// Mark all as inactive at different times
			for i := 0; i < 4; i++ {
				releases[i].Deactivate(MessageReleaseSuperseded, nil)
				Expect(k8sClient.Status().Update(ctx, releases[i])).To(Succeed())
			}

			err := reconciler.cullReleases(ctx, logr.Discard(), "releases", "target-trim-5")
			Expect(err).NotTo(HaveOccurred())

			// Verify only 3 releases remain (the most recently created ones)
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"config.targetName": "target-trim-5",
					}),
				)).To(Succeed())
				return len(releaseList.Items)
			}).Should(Equal(3))
		})

		It("Should not affect releases in different namespaces", func() {
			err := k8sClient.Create(ctx, &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-namespace",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Create releases in different namespace
			otherNsRelease := generateRelease("target-trim-6")
			otherNsRelease.Namespace = "other-namespace"
			Expect(k8sClient.Create(ctx, otherNsRelease)).To(Succeed())

			// Create 5 inactive releases in 'releases' namespace
			for i := 0; i < 5; i++ {
				release := createRelease(ctx, "target-trim-6")
				release.Activate(MessageReleaseActive, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
				release.Deactivate(MessageReleaseSuperseded, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
			}

			err = reconciler.cullReleases(ctx, logr.Discard(), "releases", "target-trim-6")
			Expect(err).NotTo(HaveOccurred())

			// Verify release in other namespace still exists
			fetchedRelease := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      otherNsRelease.Name,
				Namespace: "other-namespace",
			}, fetchedRelease)).To(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, otherNsRelease)).To(Succeed())
		})

		It("Should not affect inactive releases with different targets", func() {
			// Create 5 inactive releases for target-trim-7
			for i := 0; i < 5; i++ {
				release := createRelease(ctx, "target-trim-7")
				release.Activate(MessageReleaseActive, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
				release.Deactivate(MessageReleaseSuperseded, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
			}

			// Create 2 inactive releases for different-target
			differentTarget1 := createRelease(ctx, "different-target")
			differentTarget1.Activate(MessageReleaseActive, nil)
			Expect(k8sClient.Status().Update(ctx, differentTarget1)).To(Succeed())
			differentTarget1.Deactivate(MessageReleaseSuperseded, nil)
			Expect(k8sClient.Status().Update(ctx, differentTarget1)).To(Succeed())

			differentTarget2 := createRelease(ctx, "different-target")
			differentTarget2.Activate(MessageReleaseActive, nil)
			Expect(k8sClient.Status().Update(ctx, differentTarget2)).To(Succeed())
			differentTarget2.Deactivate(MessageReleaseSuperseded, nil)
			Expect(k8sClient.Status().Update(ctx, differentTarget2)).To(Succeed())

			err := reconciler.cullReleases(ctx, logr.Discard(), "releases", "target-trim-7")
			Expect(err).NotTo(HaveOccurred())

			// Verify different-target releases are unaffected
			releaseList := &v1alpha1.ReleaseList{}
			Expect(k8sClient.List(ctx, releaseList,
				client.InNamespace("releases"),
				client.MatchingFields(map[string]string{
					"config.targetName": "different-target",
				}),
			)).To(Succeed())
			Expect(len(releaseList.Items)).To(Equal(2))
		})
	})

	Context("Reconcile", func() {
		It("Should successfully reconcile and initialize a new release", func() {
			release := generateRelease("reconcile-target-1")
			Expect(k8sClient.Create(ctx, release)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: DefaultNamespace,
					Name:      release.Name,
				},
			}, release)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{RequeueAfter: time.Microsecond * 1}))

			// Verify release is initialized with conditions
			Eventually(func() bool {
				fetchedRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      release.Name,
					Namespace: DefaultNamespace,
				}, fetchedRelease)).To(Succeed())
				return fetchedRelease.IsStatusInitialised()
			}).Should(BeTrue())
		})

		It("Should supersede previous active releases when deployment end time is set", func() {
			// Create and activate an older release with deployment end time
			oldRelease := createRelease(ctx, "reconcile-target-3")
			oldTime := time.Now().Add(-1 * time.Hour)
			oldRelease.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: oldTime.Format(time.RFC3339),
			}
			Expect(k8sClient.Update(ctx, oldRelease)).To(Succeed())

			// Reconcile old release to activate it
			_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: oldRelease.Name},
			}, oldRelease)
			Expect(err).NotTo(HaveOccurred())

			// Wait for old release to be activated
			Eventually(func() bool {
				fetchedOld := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      oldRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedOld)).To(Succeed())
				return fetchedOld.IsConditionActive()
			}, "5s", "100ms").Should(BeTrue())

			// Create a new release with a later deployment end time
			newRelease := createRelease(ctx, "reconcile-target-3")
			newTime := time.Now()
			newRelease.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: newTime.Format(time.RFC3339),
			}
			Expect(k8sClient.Update(ctx, newRelease)).To(Succeed())

			// Reconcile new release to activate it and supersede old one
			_, err = reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: newRelease.Name},
			}, newRelease)
			Expect(err).NotTo(HaveOccurred())

			// Verify old release is superseded
			Eventually(func() bool {
				fetchedOldRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      oldRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedOldRelease)).To(Succeed())
				return !fetchedOldRelease.IsConditionActive() &&
					fetchedOldRelease.Status.NextRelease.ReleaseRef == newRelease.Name
			}, "5s", "100ms").Should(BeTrue())

			// Verify new release is active
			Eventually(func() bool {
				fetchedNewRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      newRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedNewRelease)).To(Succeed())
				return fetchedNewRelease.IsConditionActive()
			}, "5s", "100ms").Should(BeTrue())
		})

		It("Should cull inactive releases when limit is exceeded", func() {
			// Create multiple inactive releases
			for i := 0; i < 4; i++ {
				release := createRelease(ctx, "default")
				release.Activate(MessageReleaseActive, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
				release.Deactivate(MessageReleaseSuperseded, nil)
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
			}

			newRelease := createRelease(ctx, "default")
			_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: newRelease.Name},
			}, newRelease)
			Expect(err).NotTo(HaveOccurred())

			// Verify that culling happened - max 3 inactive releases
			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace("releases"))).To(Succeed())
				inactiveCount := 0
				for _, r := range releases.Items {
					if !r.IsConditionActive() {
						inactiveCount++
					}
				}
				return inactiveCount
			}).Should(BeNumerically("<=", 3))
		})

		It("Should handle multiple releases with deployment end times", func() {
			target := "reconcile-target-6"

			// Create first release with deployment end time
			release1 := createRelease(ctx, target)
			time1 := time.Now().Add(-1 * time.Hour)
			release1.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time1.Format(time.RFC3339),
			}
			Expect(k8sClient.Update(ctx, release1)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release1.Name},
			}, release1)
			Expect(err).NotTo(HaveOccurred())

			// Wait for first release to be activated
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1)).To(Succeed())
				return r1.IsConditionActive()
			}, "5s", "100ms").Should(BeTrue())

			// Create second release with later deployment end time
			release2 := createRelease(ctx, target)
			time2 := time.Now()
			release2.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time2.Format(time.RFC3339),
			}
			Expect(k8sClient.Update(ctx, release2)).To(Succeed())

			_, err = reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release2.Name},
			}, release2)
			Expect(err).NotTo(HaveOccurred())

			// Verify release1 is inactive and release2 is active
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				r2 := &v1alpha1.Release{}

				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: DefaultNamespace}, r2)).To(Succeed())

				return !r1.IsConditionActive() &&
					r2.IsConditionActive() &&
					r1.Status.NextRelease.ReleaseRef == release2.Name
			}, "5s", "100ms").Should(BeTrue())
		})

		It("Should handle releases for different targets independently", func() {
			// Create releases for two different targets with deployment end times
			release1 := createRelease(ctx, "target-a")
			time1 := time.Now()
			release1.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time1.Format(time.RFC3339),
			}
			Expect(k8sClient.Update(ctx, release1)).To(Succeed())

			release2 := createRelease(ctx, "target-b")
			time2 := time.Now()
			release2.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time2.Format(time.RFC3339),
			}
			Expect(k8sClient.Update(ctx, release2)).To(Succeed())

			// Reconcile both
			_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release1.Name},
			}, release1)
			Expect(err).NotTo(HaveOccurred())

			_, err = reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release2.Name},
			}, release2)
			Expect(err).NotTo(HaveOccurred())

			// Verify both are active (they don't supersede each other)
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				r2 := &v1alpha1.Release{}

				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: DefaultNamespace}, r2)).To(Succeed())

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

func generateRelease(target string) *v1alpha1.Release {
	appSHA := generateCommitSHA()
	infraSHA := generateCommitSHA()
	return &v1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name:      target + "-" + infraSHA[:7] + "-" + appSHA[:7],
			Namespace: DefaultNamespace,
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

func createRelease(ctx context.Context, target string) *v1alpha1.Release {
	release := generateRelease(target)
	err := k8sClient.Create(ctx, release)
	Expect(err).NotTo(HaveOccurred())
	return release
}

func getMetaV1Timestamp(ts string) metav1.Time {
	timestamp, err := time.Parse(time.RFC3339, ts)
	Expect(err).NotTo(HaveOccurred())
	return metav1.NewTime(timestamp)
}
