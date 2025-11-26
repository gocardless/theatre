package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

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

	Context("markReleaseActive and markReleaseSuperseded", func() {
		It("Should set a release as active, and update its status", func() {
			Expect(obj).NotTo(BeNil())
			Expect(reconciler.markReleaseActive(ctx, &obj)).To(Succeed())

			Eventually(func() v1alpha1.ReleaseStatus {
				releaseFromClient := v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-release", Namespace: DefaultNamespace}, &releaseFromClient)).To(Succeed())
				return releaseFromClient.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseActive)),
				HaveField("Message", Equal(MessageReleaseActive)),
				HaveField("LastAppliedTime", Not(BeNil())),
				HaveField("SupersededBy", Equal("")),
				HaveField("SupersededTime", Equal(metav1.Time{})),
			))
		})

		It("Should set a release as superseded, and update its status if previously active", func() {
			Expect(obj).NotTo(BeNil())
			Expect(reconciler.markReleaseActive(ctx, &obj)).To(Succeed())
			Expect(reconciler.markReleaseSuperseded(ctx, &obj, "superseded-by")).To(Succeed())

			Eventually(func() v1alpha1.ReleaseStatus {
				releaseFromClient := v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-release", Namespace: DefaultNamespace}, &releaseFromClient))
				return releaseFromClient.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseInactive)),
				HaveField("Message", Equal(fmt.Sprintf(MessageReleaseSuperseded, "superseded-by"))),
				HaveField("SupersededBy", Equal("superseded-by")),
				HaveField("SupersededTime", Not(BeNil())),
			))
		})

		It("Should error out if the release is new", func() {
			Expect(obj).NotTo(BeNil())
			Expect(reconciler.markReleaseSuperseded(ctx, &obj, "superseded-by")).To(MatchError(ErrCannotSupersedeNewRelease))
		})
	})

	Context("supersedePreviousReleases", func() {
		It("Should mark all other releases as inactive", func() {
			Expect(reconciler.markReleaseActive(ctx, &obj)).To(Succeed())

			newRelease := v1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-release-2",
					Namespace: DefaultNamespace,
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
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-release", Namespace: DefaultNamespace}, inactiveRelease)).To(Succeed())
				return inactiveRelease.Status
			}).Should(HaveField("Phase", Equal(v1alpha1.PhaseInactive)))
		})
	})

	Context("trimExtraReleases", func() {
		It("Should not delete any releases when count is below limit", func() {
			// Create 2 releases (below limit of 3)
			release1 := createRelease(ctx, "trim")
			release2 := createRelease(ctx, "trim")
			Expect(reconciler.markReleaseActive(ctx, release1)).To(Succeed())
			Expect(reconciler.markReleaseActive(ctx, release2)).To(Succeed())

			err := reconciler.trimExtraReleases(logr.Discard(), ctx, "releases", "trim")
			Expect(err).NotTo(HaveOccurred())

			// Verify both releases still exist
			releases := &v1alpha1.ReleaseList{}
			Expect(k8sClient.List(ctx, releases,
				client.InNamespace("releases"),
				client.MatchingFields(map[string]string{
					"spec.utopiaServiceTargetRelease": "trim",
				}),
			)).To(Succeed())
			Expect(len(releases.Items)).To(Equal(2))
		})

		It("Should not delete any releases when count equals limit", func() {
			// Create exactly 3 releases (at limit)
			release1 := createRelease(ctx, "target-trim-2")
			release2 := createRelease(ctx, "target-trim-2")
			release3 := createRelease(ctx, "target-trim-2")
			Expect(reconciler.markReleaseActive(ctx, release1)).To(Succeed())
			Expect(reconciler.markReleaseActive(ctx, release2)).To(Succeed())
			Expect(reconciler.markReleaseActive(ctx, release3)).To(Succeed())

			err := reconciler.trimExtraReleases(logr.Discard(), ctx, "releases", "target-trim-2")
			Expect(err).NotTo(HaveOccurred())

			// Verify all 3 releases still exist
			releases := &v1alpha1.ReleaseList{}
			Expect(k8sClient.List(ctx, releases,
				client.InNamespace("releases"),
				client.MatchingFields(map[string]string{
					"spec.utopiaServiceTargetRelease": "target-trim-2",
				}),
			)).To(Succeed())
			Expect(len(releases.Items)).To(Equal(3))
		})

		It("Should delete oldest releases when limit is exceeded", func() {
			// Create 5 releases (2 over limit of 3)
			releases := make([]*v1alpha1.Release, 5)
			for i := 0; i < 5; i++ {
				releases[i] = createRelease(ctx, "target-trim-3")
				Expect(reconciler.markReleaseActive(ctx, releases[i])).To(Succeed())
			}

			err := reconciler.trimExtraReleases(logr.Discard(), ctx, "releases", "target-trim-3")
			Expect(err).NotTo(HaveOccurred())

			// Verify only 3 releases remain
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"spec.utopiaServiceTargetRelease": "target-trim-3",
					}),
				)).To(Succeed())
				return len(releaseList.Items)
			}).Should(Equal(3))
		})

		It("Should preserve newest releases by LastAppliedTime when all have same phase", func() {
			// Create 4 inactive releases with different LastAppliedTime
			releases := make([]*v1alpha1.Release, 4)
			for i := 0; i < 4; i++ {
				releases[i] = createRelease(ctx, "target-trim-5")
				Expect(reconciler.markReleaseActive(ctx, releases[i])).To(Succeed())
			}

			// Mark all as inactive at different times
			Expect(reconciler.markReleaseSuperseded(ctx, releases[0], "superseded")).To(Succeed())
			Expect(reconciler.markReleaseSuperseded(ctx, releases[1], "superseded")).To(Succeed())
			Expect(reconciler.markReleaseSuperseded(ctx, releases[2], "superseded")).To(Succeed())
			Expect(reconciler.markReleaseSuperseded(ctx, releases[3], "superseded")).To(Succeed())

			err := reconciler.trimExtraReleases(logr.Discard(), ctx, "releases", "target-trim-5")
			Expect(err).NotTo(HaveOccurred())

			// Verify only 3 releases remain (the ones with most recent LastAppliedTime)
			Eventually(func() int {
				releaseList := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releaseList,
					client.InNamespace("releases"),
					client.MatchingFields(map[string]string{
						"spec.utopiaServiceTargetRelease": "target-trim-5",
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

			// Create 5 releases in 'releases' namespace
			for i := 0; i < 5; i++ {
				release := createRelease(ctx, "target-trim-6")
				Expect(reconciler.markReleaseActive(ctx, release)).To(Succeed())
			}

			err = reconciler.trimExtraReleases(logr.Discard(), ctx, "releases", "target-trim-6")
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

		It("Should not affect releases with different targets", func() {
			// Create 5 releases for target-trim-7
			for i := 0; i < 5; i++ {
				release := createRelease(ctx, "target-trim-7")
				Expect(reconciler.markReleaseActive(ctx, release)).To(Succeed())
			}

			// Create 2 releases for different-target
			differentTarget1 := createRelease(ctx, "different-target")
			differentTarget2 := createRelease(ctx, "different-target")
			Expect(reconciler.markReleaseActive(ctx, differentTarget1)).To(Succeed())
			Expect(reconciler.markReleaseActive(ctx, differentTarget2)).To(Succeed())

			err := reconciler.trimExtraReleases(logr.Discard(), ctx, "releases", "target-trim-7")
			Expect(err).NotTo(HaveOccurred())

			// Verify different-target releases are unaffected
			releaseList := &v1alpha1.ReleaseList{}
			Expect(k8sClient.List(ctx, releaseList,
				client.InNamespace("releases"),
				client.MatchingFields(map[string]string{
					"spec.utopiaServiceTargetRelease": "different-target",
				}),
			)).To(Succeed())
			Expect(len(releaseList.Items)).To(Equal(2))
		})
	})

	Context("Reconcile", func() {
		It("Should successfully reconcile a new release", func() {
			release := createRelease(ctx, "reconcile-target-1")

			result, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: DefaultNamespace,
					Name:      release.Name,
				},
			}, release)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify release is marked as active
			Eventually(func() v1alpha1.Phase {
				fetchedRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      release.Name,
					Namespace: DefaultNamespace,
				}, fetchedRelease)).To(Succeed())
				return fetchedRelease.Status.Phase
			}).Should(Equal(v1alpha1.PhaseActive))
		})

		It("Should mark new release as active during reconciliation", func() {
			release := createRelease(ctx, "reconcile-target-2")
			Expect(release.Status.Phase).To(BeEmpty())

			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release.Name},
			}, release)
			Expect(err).NotTo(HaveOccurred())

			// Verify the release was marked active
			Eventually(func() v1alpha1.ReleaseStatus {
				fetchedRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      release.Name,
					Namespace: DefaultNamespace,
				}, fetchedRelease)).To(Succeed())
				return fetchedRelease.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseActive)),
				HaveField("LastAppliedTime", Not(BeNil())),
			))
		})

		It("Should supersede previous active releases during reconciliation", func() {
			// Create and activate an older release
			oldRelease := createRelease(ctx, "reconcile-target-3")
			Expect(reconciler.markReleaseActive(ctx, oldRelease)).To(Succeed())

			// Create a new release and reconcile it
			newRelease := createRelease(ctx, "reconcile-target-3")
			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: newRelease.Name},
			}, newRelease)
			Expect(err).NotTo(HaveOccurred())

			// Verify old release is superseded
			Eventually(func() v1alpha1.ReleaseStatus {
				fetchedOldRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      oldRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedOldRelease)).To(Succeed())
				return fetchedOldRelease.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseInactive)),
				HaveField("SupersededBy", Equal(newRelease.Name)),
				HaveField("SupersededTime", Not(BeNil())),
			))

			// Verify new release is active
			Eventually(func() v1alpha1.Phase {
				fetchedNewRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      newRelease.Name,
					Namespace: DefaultNamespace,
				}, fetchedNewRelease)).To(Succeed())
				return fetchedNewRelease.Status.Phase
			}).Should(Equal(v1alpha1.PhaseActive))
		})

		It("Should keep the number of releases below the configured limit", func() {
			Expect(createRelease(ctx, "default")).ToNot(BeNil())
			Expect(createRelease(ctx, "default")).ToNot(BeNil())
			Expect(createRelease(ctx, "default")).ToNot(BeNil())
			release := createRelease(ctx, "default")

			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: DefaultNamespace}}, release)
			Expect(err).NotTo(HaveOccurred())

			// Verify that only 3 releases exist (culling happened)
			Eventually(func() int {
				releases := &v1alpha1.ReleaseList{}
				Expect(k8sClient.List(ctx, releases, client.InNamespace("releases"))).To(Succeed())
				return len(releases.Items)
			}).Should(Equal(3))
		})

		It("Should not process already active releases", func() {
			// Create and mark release as active
			release := createRelease(ctx, "reconcile-target-4")
			Expect(reconciler.markReleaseActive(ctx, release)).To(Succeed())

			// Get the current status
			fetchedRelease := &v1alpha1.Release{}
			Eventually(func() v1alpha1.ReleaseStatus {
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      release.Name,
					Namespace: DefaultNamespace,
				}, fetchedRelease)).To(Succeed())
				return fetchedRelease.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseActive)),
				HaveField("LastAppliedTime", Not(BeNil())),
			))

			originalLastAppliedTime := fetchedRelease.Status.LastAppliedTime

			// Reconcile again - should not change anything
			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release.Name},
			}, fetchedRelease)
			Expect(err).NotTo(HaveOccurred())

			// Verify status hasn't changed
			refetchedRelease := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      release.Name,
				Namespace: DefaultNamespace,
			}, refetchedRelease)).To(Succeed())

			Expect(refetchedRelease.Status.Phase).To(Equal(v1alpha1.PhaseActive))
			Expect(refetchedRelease.Status.LastAppliedTime).To(Equal(originalLastAppliedTime))
		})

		It("Should not process inactive releases", func() {
			// Create and mark release as superseded
			release := createRelease(ctx, "reconcile-target-5")
			Expect(reconciler.markReleaseActive(ctx, release)).To(Succeed())
			Expect(reconciler.markReleaseSuperseded(ctx, release, "some-other-release")).To(Succeed())

			// Get the current status
			fetchedRelease := &v1alpha1.Release{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      release.Name,
				Namespace: DefaultNamespace,
			}, fetchedRelease)).To(Succeed())

			// Reconcile - should not change anything since it's inactive
			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release.Name},
			}, fetchedRelease)
			Expect(err).NotTo(HaveOccurred())

			// Verify status hasn't changed
			// Verify status hasn't changed
			Eventually(func() v1alpha1.ReleaseStatus {
				refetchedRelease := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{
					Name:      release.Name,
					Namespace: DefaultNamespace,
				}, refetchedRelease)).To(Succeed())
				return refetchedRelease.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseInactive)),
				HaveField("SupersededBy", Equal("some-other-release")),
			))
		})

		It("Should handle multiple releases for the same target correctly", func() {
			target := "reconcile-target-6"

			// Create first release and reconcile
			release1 := createRelease(ctx, target)
			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release1.Name},
			}, release1)
			Expect(err).NotTo(HaveOccurred())

			// Create second release and reconcile
			release2 := createRelease(ctx, target)
			_, err = reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release2.Name},
			}, release2)
			Expect(err).NotTo(HaveOccurred())

			// Verify release1 is inactive and release2 is active
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				r2 := &v1alpha1.Release{}

				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: DefaultNamespace}, r2)).To(Succeed())

				return r1.Status.Phase == v1alpha1.PhaseInactive &&
					r2.Status.Phase == v1alpha1.PhaseActive &&
					r1.Status.SupersededBy == release2.Name
			}).Should(BeTrue())
		})

		It("Should handle releases for different targets independently", func() {
			// Create releases for two different targets
			release1 := createRelease(ctx, "target-a")
			release2 := createRelease(ctx, "target-b")

			// Reconcile both
			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release1.Name},
			}, release1)
			Expect(err).NotTo(HaveOccurred())

			_, err = reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release2.Name},
			}, release2)
			Expect(err).NotTo(HaveOccurred())

			// Verify both are active (they don't supersede each other)
			Eventually(func() bool {
				r1 := &v1alpha1.Release{}
				r2 := &v1alpha1.Release{}

				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: DefaultNamespace}, r2)).To(Succeed())

				return r1.Status.Phase == v1alpha1.PhaseActive &&
					r2.Status.Phase == v1alpha1.PhaseActive
			}).Should(BeTrue())
		})

		It("Should update history when a release is marked inactive", func() {
			release1 := createRelease(ctx, "history")
			release2 := createRelease(ctx, "history")

			_, err := reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release1.Name},
			}, release1)
			Expect(err).NotTo(HaveOccurred())

			_, err = reconciler.Reconcile(logr.Discard(), ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: DefaultNamespace, Name: release2.Name},
			}, release2)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() v1alpha1.ReleaseStatus {
				r1 := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1)).To(Succeed())
				return r1.Status
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseInactive)),
			))

			Eventually(func() v1alpha1.ReleaseStatusEntry {
				r1 := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release1.Name, Namespace: DefaultNamespace}, r1)).To(Succeed())
				return r1.Status.History[0]
			}).Should(SatisfyAll(
				HaveField("Phase", Equal(v1alpha1.PhaseActive)),
				HaveField("LastAppliedTime", Not(BeNil())),
				HaveField("SupersededBy", Equal(BeNil())),
				HaveField("SupersededTime", Equal(metav1.Time{})),
			))

			Eventually(func() v1alpha1.ReleaseStatus {
				r2 := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: release2.Name, Namespace: DefaultNamespace}, r2)).To(Succeed())
				return r2.Status
			}).Should(SatisfyAll(
				HaveField("Phase", v1alpha1.PhaseActive),
				HaveField("History", HaveLen(0)),
			))
		})
	})

	Context("ReleaseStatusHistory", func() {
		Context("prependToHistory", func() {
			It("Should prepend the current status to the history array if not previous history", func() {
				release := createRelease(ctx, "history")
				reconciler.prependToHistory(release)

				lastHistoryEntry := release.Status.History[0]

				Expect(lastHistoryEntry.Phase).To(Equal(release.Status.Phase))
				Expect(lastHistoryEntry.Message).To(Equal(release.Status.Message))
				Expect(lastHistoryEntry.LastAppliedTime).To(Equal(release.Status.LastAppliedTime))
				Expect(lastHistoryEntry.SupersededBy).To(Equal(release.Status.SupersededBy))
				Expect(lastHistoryEntry.SupersededTime).To(Equal(release.Status.SupersededTime))
			})

			It("Should prepend to the current status to the history if there is a previous history", func() {
				recordedTime := metav1.Now()

				release := createRelease(ctx, "history")
				release.Status.Phase = v1alpha1.PhaseActive
				release.Status.Message = "Active release"
				release.Status.LastAppliedTime = recordedTime
				reconciler.prependToHistory(release)
				previousHistoryEntry := release.Status.History[0]

				supersededTime := metav1.Now()
				mockReleaseName := "history-infraSha-appSha"
				release.Status.Phase = v1alpha1.PhaseInactive
				release.Status.Message = "Superseded by " + mockReleaseName
				release.Status.LastAppliedTime = recordedTime
				release.Status.SupersededBy = mockReleaseName
				release.Status.SupersededTime = supersededTime
				reconciler.prependToHistory(release)

				latestHistoryEntry := release.Status.History[0]

				Expect(previousHistoryEntry.Phase).To(Equal(v1alpha1.PhaseActive))
				Expect(previousHistoryEntry.Message).To(Equal("Active release"))
				Expect(previousHistoryEntry.LastAppliedTime).To(Equal(recordedTime))

				Expect(latestHistoryEntry.Phase).To(Equal(v1alpha1.PhaseInactive))
				Expect(latestHistoryEntry.Message).To(Equal("Superseded by " + mockReleaseName))
				Expect(latestHistoryEntry.LastAppliedTime).To(Equal(recordedTime))
				Expect(latestHistoryEntry.SupersededBy).To(Equal(mockReleaseName))
				Expect(latestHistoryEntry.SupersededTime).To(Equal(supersededTime))

			})

			It("Should keep the history under the configured maximum", func() {
				release := createRelease(ctx, "history")
				reconciler.prependToHistory(release)
				reconciler.prependToHistory(release)
				reconciler.prependToHistory(release)
				reconciler.prependToHistory(release)
				reconciler.prependToHistory(release)

				Expect(len(release.Status.History)).To(Equal(reconciler.MaxHistoryLimit))
			})
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
