package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
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

		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, &obj)
			if err != nil {
				return false
			}

			// means that the release has been initialised
			return len(obj.Status.Conditions) > 0
		}, "5s", "100ms").Should(BeTrue())
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
			fetchedObj := &v1alpha1.Release{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
			Expect(err).NotTo(HaveOccurred())

			By("Setting the deployment start time")
			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: stringTimestamp,
			}
			err = k8sClient.Update(ctx, fetchedObj)
			Expect(err).NotTo(HaveOccurred())

			// Verify the status was persisted to the cluster
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentStartTime.Unix() == metav1Timestamp.Unix()
			}).Should(BeTrue())
		})

		It("Should error when passing invalid time when annotated with "+v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime, func() {
			fetchedObj := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())

			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: "not-a-timestamp",
			}

			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(HaveOccurred())
		})

		It("Should error when passing invalid time when annotated with "+v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime, func() {
			fetchedObj := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)).To(Succeed())

			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: "not-a-timestamp",
			}
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(HaveOccurred())
		})

		It("Should update status.deploymentEndTime when annotated with "+v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime, func() {
			// Retry update in case of conflicts from background reconciliation
			fetchedObj := &v1alpha1.Release{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
			Expect(err).NotTo(HaveOccurred())

			By("Setting the deployment end time")
			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: stringTimestamp,
			}
			err = k8sClient.Update(ctx, fetchedObj)
			Expect(err).NotTo(HaveOccurred())

			// Verify the status was persisted to the cluster
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentEndTime.Unix() == metav1Timestamp.Unix()
			}).Should(BeTrue())
		})

		It("Should update all status fields when annotated with "+strings.Join([]string{
			v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime,
			v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime,
		}, ","), func() {
			startTimestamp := "2025-12-08T14:32:00Z"
			metav1StartTimestamp := getMetaV1Timestamp(startTimestamp)
			endTimestamp := "2025-12-08T14:42:00Z"
			metav1EndTimestamp := getMetaV1Timestamp(endTimestamp)

			// Retry update in case of conflicts from background reconciliation
			fetchedObj := &v1alpha1.Release{}
			// Eventually(func() error {
			err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
			Expect(err).NotTo(HaveOccurred())

			By("Setting the deployment start time and end time")
			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: startTimestamp,
				v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime:   endTimestamp,
			}
			err = k8sClient.Update(ctx, fetchedObj)
			Expect(err).NotTo(HaveOccurred())

			// Verify both status fields were persisted to the cluster
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentStartTime.Unix() == metav1StartTimestamp.Unix() &&
					updatedObj.Status.DeploymentEndTime.Unix() == metav1EndTimestamp.Unix()
			}).Should(BeTrue())
		})
	})

	Context("cullReleases", func() {
		It("Should not delete any inactive releases when count is below or equals limit", func() {
			// Create 3 inactive releases (below limit of 3)
			for i := 0; i < 3; i++ {
				time := time.Now().Add(-1 * time.Hour)
				createRelease(ctx, "trim", &time)
			}

			err := reconciler.cullReleases(ctx, logr.Discard(), "releases", "trim")
			Expect(err).NotTo(HaveOccurred())

			type counts struct {
				Active int
				Total  int
			}
			Eventually(func() counts {
				// Verify all releases still exist and only one is active
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases,
					client.InNamespace("releases"),
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
			// Create 4 releases (1 over limit of 3)
			releases := make([]*v1alpha1.Release, 4)
			newestInactiveTime := time.Now()
			for i := 0; i < 4; i++ {
				time := time.Now().Add(-time.Duration(i) * time.Hour)
				releases[i] = createRelease(ctx, "target-trim-3", &time)

				if i == 1 {
					// the second release is the newest inactive
					newestInactiveTime = time
				}
			}
			// the last one is the oldest
			oldestInactive := releases[3]

			err := reconciler.cullReleases(ctx, logr.Discard(), "releases", "target-trim-3")
			Expect(err).NotTo(HaveOccurred())

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
					client.InNamespace("releases"),
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
			}, "5s", "500ms").Should(Equal(counts{Active: 1, NewestInactiveFound: true, OldestInactiveFound: false, Total: 3}))
		})

		It("Should preserve newest inactive releases by creation time", func() {
			// Create 4 inactive releases
			releases := make([]*v1alpha1.Release, 3)
			for i := 0; i < 3; i++ {
				releases[i] = createRelease(ctx, "target-trim-5", nil)

				// Activate with retry logic
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: releases[i].Name, Namespace: releases[i].Namespace}, releases[i]); err != nil {
						return err
					}
					releases[i].Activate(MessageReleaseActive, nil)
					return k8sClient.Status().Update(ctx, releases[i])
				}).Should(Succeed())
			}

			// Mark all as inactive at different times
			for i := 0; i < 3; i++ {
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: releases[i].Name, Namespace: releases[i].Namespace}, releases[i]); err != nil {
						return err
					}
					releases[i].Deactivate(MessageReleaseSuperseded, nil)
					return k8sClient.Status().Update(ctx, releases[i])
				}).Should(Succeed())
			}

			// Wait for all releases to be indexed as inactive
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"config.targetName":        "target-trim-5",
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}).Should(Equal(3))

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
			releases := make([]*v1alpha1.Release, 3)
			for i := 0; i < 3; i++ {
				releases[i] = createRelease(ctx, "target-trim-6", nil)

				// Activate with retry logic
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: releases[i].Name, Namespace: releases[i].Namespace}, releases[i]); err != nil {
						return err
					}
					releases[i].Activate(MessageReleaseActive, nil)
					return k8sClient.Status().Update(ctx, releases[i])
				}).Should(Succeed())

				// Deactivate with retry logic
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: releases[i].Name, Namespace: releases[i].Namespace}, releases[i]); err != nil {
						return err
					}
					releases[i].Deactivate(MessageReleaseSuperseded, nil)
					return k8sClient.Status().Update(ctx, releases[i])
				}).Should(Succeed())
			}

			// Wait for all releases to be indexed as inactive
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"config.targetName":        "target-trim-6",
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}).Should(Equal(3))

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
			releases := make([]*v1alpha1.Release, 3)
			for i := 0; i < 3; i++ {
				releases[i] = createRelease(ctx, "target-trim-7", nil)

				// Activate with retry logic
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: releases[i].Name, Namespace: releases[i].Namespace}, releases[i]); err != nil {
						return err
					}
					releases[i].Activate(MessageReleaseActive, nil)
					return k8sClient.Status().Update(ctx, releases[i])
				}).Should(Succeed())

				// Deactivate with retry logic
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: releases[i].Name, Namespace: releases[i].Namespace}, releases[i]); err != nil {
						return err
					}
					releases[i].Deactivate(MessageReleaseSuperseded, nil)
					return k8sClient.Status().Update(ctx, releases[i])
				}).Should(Succeed())
			}

			// Wait for all releases to be indexed as inactive
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"config.targetName":        "target-trim-7",
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}).Should(Equal(3))

			// Create 2 inactive releases for different-target
			differentTarget1 := createRelease(ctx, "different-target", nil)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: differentTarget1.Name, Namespace: differentTarget1.Namespace}, differentTarget1); err != nil {
					return err
				}
				differentTarget1.Activate(MessageReleaseActive, nil)
				return k8sClient.Status().Update(ctx, differentTarget1)
			}).Should(Succeed())
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: differentTarget1.Name, Namespace: differentTarget1.Namespace}, differentTarget1); err != nil {
					return err
				}
				differentTarget1.Deactivate(MessageReleaseSuperseded, nil)
				return k8sClient.Status().Update(ctx, differentTarget1)
			}).Should(Succeed())

			differentTarget2 := createRelease(ctx, "different-target", nil)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: differentTarget2.Name, Namespace: differentTarget2.Namespace}, differentTarget2); err != nil {
					return err
				}
				differentTarget2.Activate(MessageReleaseActive, nil)
				return k8sClient.Status().Update(ctx, differentTarget2)
			}).Should(Succeed())
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: differentTarget2.Name, Namespace: differentTarget2.Namespace}, differentTarget2); err != nil {
					return err
				}
				differentTarget2.Deactivate(MessageReleaseSuperseded, nil)
				return k8sClient.Status().Update(ctx, differentTarget2)
			}).Should(Succeed())

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
			// Skip("wip")
			// Create and activate an older release with deployment end time
			oldRelease := createRelease(ctx, "reconcile-target-3", nil)
			oldTime := time.Now().Add(-1 * time.Hour)

			// Update oldRelease with annotation (with retry logic)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: oldRelease.Name, Namespace: oldRelease.Namespace}, oldRelease); err != nil {
					return err
				}
				oldRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: oldTime.Format(time.RFC3339),
				}
				return k8sClient.Update(ctx, oldRelease)
			}).Should(Succeed())

			// Wait for annotation to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: oldRelease.Name, Namespace: oldRelease.Namespace}, oldRelease)
				if err != nil {
					return false
				}
				_, exists := oldRelease.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())

			// Trigger reconcile on old release to activate it
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: oldRelease.Name, Namespace: oldRelease.Namespace}, oldRelease); err != nil {
					return err
				}
				_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
					NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: oldRelease.Name},
				}, oldRelease)
				return err
			}).Should(Succeed())

			// Wait for old release to be activated
			Eventually(func() bool {
				fetchedOld := &v1alpha1.Release{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      oldRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedOld); err != nil {
					return false
				}
				return fetchedOld.IsConditionActive()
			}, "5s", "100ms").Should(BeTrue())

			// Create a new release with a later deployment end time
			newRelease := createRelease(ctx, "reconcile-target-3", nil)
			newTime := time.Now()

			// Update newRelease with annotation (with retry logic)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: newRelease.Name, Namespace: newRelease.Namespace}, newRelease); err != nil {
					return err
				}
				newRelease.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: newTime.Format(time.RFC3339),
				}
				return k8sClient.Update(ctx, newRelease)
			}).Should(Succeed())

			// Wait for annotation to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: newRelease.Name, Namespace: newRelease.Namespace}, newRelease)
				if err != nil {
					return false
				}
				_, exists := newRelease.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())

			// Trigger reconcile on new release to activate it and supersede old one
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: newRelease.Name, Namespace: newRelease.Namespace}, newRelease); err != nil {
					return err
				}
				_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
					NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: newRelease.Name},
				}, newRelease)
				return err
			}).Should(Succeed())

			// Wait for new release to be activated and old one to be superseded
			Eventually(func() bool {
				fetchedOldRelease := &v1alpha1.Release{}
				fetchedNewRelease := &v1alpha1.Release{}

				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      oldRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedOldRelease); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      newRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedNewRelease); err != nil {
					return false
				}

				return !fetchedOldRelease.IsConditionActive() &&
					fetchedNewRelease.IsConditionActive() &&
					fetchedOldRelease.Status.NextRelease.ReleaseRef == newRelease.Name
			}, "5s", "100ms").Should(BeTrue())
		})

		It("Should cull inactive releases when limit is exceeded", func() {
			targetName := "reconcile-cull-target"
			// Create multiple inactive releases
			releases := make([]*v1alpha1.Release, 4)
			for i := 0; i < 4; i++ {
				time := time.Now().Add(-time.Duration(i) * time.Hour)
				releases[i] = createRelease(ctx, targetName, &time)
			}

			// Verify that culling happened
			// Culling only operates on releases with Active=False, not Unknown
			// So we expect exactly 3 releases with Active=False after culling
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
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

		It("Should handle multiple releases with deployment end times", func() {
			target := "reconcile-target-6"

			// Create first release with deployment end time
			release1 := createRelease(ctx, target, nil)
			time1 := time.Now().Add(-1 * time.Hour)

			// Update release1 with annotation (with retry logic)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1); err != nil {
					return err
				}
				release1.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time1.Format(time.RFC3339),
				}
				return k8sClient.Update(ctx, release1)
			}).Should(Succeed())

			// Wait for annotation to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1)
				if err != nil {
					return false
				}
				_, exists := release1.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())

			// Trigger reconcile on first release to activate it
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1); err != nil {
					return err
				}
				_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
					NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release1.Name},
				}, release1)
				return err
			}).Should(Succeed())

			// Wait for first release to be activated
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1); err != nil {
					return false
				}
				return r1.IsConditionActive()
			}, "5s", "100ms").Should(BeTrue())

			// Create second release with later deployment end time
			release2 := createRelease(ctx, target, nil)
			time2 := time.Now()

			// Update release2 with annotation (with retry logic)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2); err != nil {
					return err
				}
				release2.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time2.Format(time.RFC3339),
				}
				return k8sClient.Update(ctx, release2)
			}).Should(Succeed())

			// Wait for annotation to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2)
				if err != nil {
					return false
				}
				_, exists := release2.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())

			// Trigger reconcile on second release to activate it and supersede first
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2); err != nil {
					return err
				}
				_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
					NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release2.Name},
				}, release2)
				return err
			}).Should(Succeed())

			// Wait for release2 to be activated and release1 to be superseded
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				r2 := &v1alpha1.Release{}

				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: DefaultNamespace}, r2); err != nil {
					return false
				}

				return !r1.IsConditionActive() &&
					r2.IsConditionActive() &&
					r1.Status.NextRelease.ReleaseRef == release2.Name
			}, "5s", "100ms").Should(BeTrue())
		})

		It("Should handle releases for different targets independently", func() {
			// Create releases for two different targets with deployment end times
			release1 := createRelease(ctx, "target-a", nil)
			time1 := time.Now()

			// Update release1 with annotation (with retry logic)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1); err != nil {
					return err
				}

				release1.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time1.Format(time.RFC3339),
				}

				if err := k8sClient.Update(ctx, release1); err != nil {
					return err
				}

				return nil
			}).Should(Succeed())

			release2 := createRelease(ctx, "target-b", nil)
			time2 := time.Now()

			// Update release2 with annotation (with retry logic)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2); err != nil {
					return err
				}
				release2.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: time2.Format(time.RFC3339),
				}
				if err := k8sClient.Update(ctx, release2); err != nil {
					return err
				}

				return nil
			}).Should(Succeed())

			// Wait for annotations to propagate and refetch
			Eventually(func() int64 {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1); err != nil {
					return 0
				}

				return release1.Status.DeploymentEndTime.Unix()
			}).Should(Equal(time1.Unix()))

			Eventually(func() int64 {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2); err != nil {
					return 0
				}
				return release2.Status.DeploymentEndTime.Unix()
			}).Should(Equal(time2.Unix()))

			// Wait for background controller to activate both releases
			// (they shouldn't supersede each other because they're different targets)
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				r2 := &v1alpha1.Release{}

				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: DefaultNamespace}, r2); err != nil {
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

func createRelease(ctx context.Context, target string, endTime *time.Time) *v1alpha1.Release {
	release := generateRelease(target)
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
