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
			// Retry update in case of conflicts from background reconciliation
			fetchedObj := &v1alpha1.Release{}
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: stringTimestamp,
				}
				return k8sClient.Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for annotation to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
				if err != nil {
					return false
				}
				_, exists := fetchedObj.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime]
				return exists
			}).Should(BeTrue())
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())

			// Verify the status was persisted to the cluster
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentStartTime.Unix() == metav1Timestamp.Unix()
			}).Should(BeTrue())
		})

		It("Should not update status when deploymentStartTime already matches annotation", func() {
			// Set initial start time
			fetchedObj := &v1alpha1.Release{}
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Status.DeploymentStartTime = metav1Timestamp
				return k8sClient.Status().Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for status update to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
				if err != nil {
					return false
				}
				return fetchedObj.Status.DeploymentStartTime.Unix() == metav1Timestamp.Unix()
			}).Should(BeTrue())

			// Add annotation with same timestamp (in memory only, to simulate annotation being set)
			fetchedObj.Annotations = map[string]string{
				v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: stringTimestamp,
			}
			originalResourceVersion := fetchedObj.ResourceVersion
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())

			// Verify no status update occurred by checking resource version hasn't changed
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.ResourceVersion == originalResourceVersion
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
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: stringTimestamp,
				}
				return k8sClient.Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for annotation to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
				if err != nil {
					return false
				}
				_, exists := fetchedObj.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())

			// Verify the status was persisted to the cluster
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentEndTime.Unix() == metav1Timestamp.Unix()
			}).Should(BeTrue())
		})

		It("Should not update status when deploymentEndTime already matches annotation", func() {
			// Set initial end time
			fetchedObj := &v1alpha1.Release{}
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Status.DeploymentEndTime = metav1Timestamp
				return k8sClient.Status().Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for status update to propagate, then add annotation with same timestamp
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime: stringTimestamp,
				}
				return k8sClient.Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for annotation to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
				if err != nil {
					return false
				}
				_, exists := fetchedObj.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())
			originalResourceVersion := fetchedObj.ResourceVersion
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())

			// Verify no status update occurred by checking resource version hasn't changed
			updatedObj := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
			Expect(updatedObj.ResourceVersion).To(Equal(originalResourceVersion))
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
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: startTimestamp,
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime:   endTimestamp,
				}
				return k8sClient.Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for annotations to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
				if err != nil {
					return false
				}
				_, startExists := fetchedObj.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime]
				_, endExists := fetchedObj.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return startExists && endExists
			}).Should(BeTrue())
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())

			// Verify both status fields were persisted to the cluster
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentStartTime.Unix() == metav1StartTimestamp.Unix() &&
					updatedObj.Status.DeploymentEndTime.Unix() == metav1EndTimestamp.Unix()
			}).Should(BeTrue())
		})

		It("Should only update deploymentStartTime when deploymentEndTime already matches", func() {
			startTimestamp := "2025-12-08T14:32:00Z"
			metav1StartTimestamp := getMetaV1Timestamp(startTimestamp)
			endTimestamp := "2025-12-08T14:42:00Z"
			metav1EndTimestamp := getMetaV1Timestamp(endTimestamp)

			// Set initial end time
			fetchedObj := &v1alpha1.Release{}
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Status.DeploymentEndTime = metav1EndTimestamp
				return k8sClient.Status().Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for status update to propagate, then add both annotations
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj); err != nil {
					return err
				}
				fetchedObj.Annotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime: startTimestamp,
					v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime:   endTimestamp,
				}
				return k8sClient.Update(ctx, fetchedObj)
			}).Should(Succeed())

			// Wait for annotations to propagate
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, fetchedObj)
				if err != nil {
					return false
				}
				_, startExists := fetchedObj.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentStartTime]
				_, endExists := fetchedObj.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return startExists && endExists
			}).Should(BeTrue())
			Expect(reconciler.handleAnnotations(ctx, logr.Discard(), fetchedObj)).To(Succeed())

			// Verify only start time was updated
			Eventually(func() bool {
				updatedObj := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: obj.Name, Namespace: obj.Namespace}, updatedObj)).To(Succeed())
				return updatedObj.Status.DeploymentStartTime.Unix() == metav1StartTimestamp.Unix() &&
					updatedObj.Status.DeploymentEndTime.Unix() == metav1EndTimestamp.Unix()
			}).Should(BeTrue())
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

			// Activate with retry logic
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1); err != nil {
					return err
				}
				release1.Activate(MessageReleaseActive, nil)
				return k8sClient.Status().Update(ctx, release1)
			}).Should(Succeed())

			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2); err != nil {
					return err
				}
				release2.Activate(MessageReleaseActive, nil)
				return k8sClient.Status().Update(ctx, release2)
			}).Should(Succeed())

			// Deactivate them with retry logic
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1); err != nil {
					return err
				}
				release1.Deactivate(MessageReleaseSuperseded, release2)
				return k8sClient.Status().Update(ctx, release1)
			}).Should(Succeed())

			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2); err != nil {
					return err
				}
				release2.Deactivate(MessageReleaseSuperseded, nil)
				return k8sClient.Status().Update(ctx, release2)
			}).Should(Succeed())

			// Wait for releases to be indexed as inactive
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"config.targetName":        "trim",
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}).Should(Equal(2))

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

			for i := 0; i < len(releases); i++ {
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
						"config.targetName":        "target-trim-2",
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}).Should(Equal(3))

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
						"config.targetName":        "target-trim-3",
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}).Should(Equal(5))

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
			for i := 0; i < 4; i++ {
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
			}).Should(Equal(4))

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
			releases := make([]*v1alpha1.Release, 5)
			for i := 0; i < 5; i++ {
				releases[i] = createRelease(ctx, "target-trim-6")

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
			}).Should(Equal(5))

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
			releases := make([]*v1alpha1.Release, 5)
			for i := 0; i < 5; i++ {
				releases[i] = createRelease(ctx, "target-trim-7")

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
			}).Should(Equal(5))

			// Create 2 inactive releases for different-target
			differentTarget1 := createRelease(ctx, "different-target")
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

			differentTarget2 := createRelease(ctx, "different-target")
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
			Skip("wip")
			// Create and activate an older release with deployment end time
			oldRelease := createRelease(ctx, "reconcile-target-3")
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
			newRelease := createRelease(ctx, "reconcile-target-3")
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
				releases[i] = createRelease(ctx, targetName)

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
						"config.targetName":        targetName,
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return 0
				}
				return len(releaseList.Items)
			}).Should(Equal(4))

			// Trigger reconcile on one of the existing releases to trigger culling
			// This ensures culling runs when the field index has all 4 inactive releases
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: releases[0].Name, Namespace: releases[0].Namespace}, releases[0]); err != nil {
					return err
				}
				_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
					NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: releases[0].Name},
				}, releases[0])
				return err
			}).Should(Succeed())

			// Verify that culling happened
			// Culling only operates on releases with Active=False, not Unknown
			// So we expect exactly 3 releases with Active=False after culling
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				err := k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"config.targetName":        targetName,
						"status.conditions.active": string(metav1.ConditionFalse),
					}),
				)
				if err != nil {
					return -1
				}
				return len(releaseList.Items)
			}, "5s", "100ms").Should(Equal(3))
		})

		It("Should handle multiple releases with deployment end times", func() {
			Skip("wip")
			target := "reconcile-target-6"

			// Create first release with deployment end time
			release1 := createRelease(ctx, target)
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
			release2 := createRelease(ctx, target)
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
			Skip("wip")
			// Create releases for two different targets with deployment end times
			release1 := createRelease(ctx, "target-a")
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

				_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
					NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release1.Name},
				}, release1)
				return err
			}).Should(Succeed())

			release2 := createRelease(ctx, "target-b")
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

				_, err := reconciler.Reconcile(ctx, logr.Discard(), ctrl.Request{
					NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release2.Name},
				}, release2)
				return err
			}).Should(Succeed())

			// Wait for annotations to propagate and refetch
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: release1.Namespace}, release1); err != nil {
					return false
				}
				_, exists := release1.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())

			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: release2.Namespace}, release2); err != nil {
					return false
				}
				_, exists := release2.Annotations[v1alpha1.AnnotationKeyReleaseSetDeploymentEndTime]
				return exists
			}).Should(BeTrue())

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
