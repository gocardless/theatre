package v1alpha1

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Helpers", func() {
	Context("Release", func() {
		Context("Equals", func() {
			var a, b ReleaseConfig

			BeforeEach(func() {
				a = ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "application", ID: "abc123"},
						{Name: "infrastructure", ID: "xyz789"},
					},
				}
				b = ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "application", ID: "abc123"},
						{Name: "infrastructure", ID: "xyz789"},
					},
				}
			})

			It("Should be equal for identical configs", func() {
				Expect(a.Equals(&b)).To(BeTrue())
			})

			It("Should not be equal if targetNames are different", func() {
				b.TargetName = "different-target"
				Expect(a.Equals(&b)).To(BeFalse())
			})

			It("Should not be equal if revisions are different", func() {
				b.Revisions[0].ID = "different-id"
				Expect(a.Equals(&b)).To(BeFalse())
			})

			It("Should not be equal if number of revisions are different", func() {
				b.Revisions = append(b.Revisions, Revision{Name: "additional", ID: "extra123"})
				Expect(a.Equals(&b)).To(BeFalse())
			})

			It("Should not be equal if revision names are different", func() {
				b.Revisions[0].Name = "different-app"
				Expect(a.Equals(&b)).To(BeFalse())
			})
		})

		Context("InitialiseStatus", func() {
			var release Release

			BeforeEach(func() {
				release = Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "test-target",
						Revisions: []Revision{
							{Name: "application", ID: "abc123"},
						},
					},
				}
			})

			It("should set the signature when initialised", func() {
				Expect(release.Status.Signature).To(BeEmpty())

				release.InitialiseStatus("test message")

				Expect(release.Status.Signature).NotTo(BeEmpty())
				Expect(release.Status.Signature).To(HaveLen(SignatureLength))
			})

			It("should set conditions when initialised", func() {
				Expect(release.Status.Conditions).To(BeEmpty())

				release.InitialiseStatus("test message")

				Expect(release.Status.Conditions).To(ContainElement(HaveField("Type", ReleaseConditionActive)))
			})

			It("should set the message when initialised", func() {
				release.InitialiseStatus("custom message")

				Expect(release.Status.Message).To(Equal("custom message"))
			})

			It("should use default message when empty string provided", func() {
				release.InitialiseStatus("")

				Expect(release.Status.Message).To(Equal("Release initialised successfully"))
			})
		})

		Context("Signature", func() {
			It("should produce different signatures for releases with different target names", func() {
				releaseA := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "target-a",
						Revisions: []Revision{
							{Name: "app", ID: "abc123"},
						},
					},
				}
				releaseB := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "target-b",
						Revisions: []Revision{
							{Name: "app", ID: "abc123"},
						},
					},
				}

				releaseA.InitialiseStatus("init")
				releaseB.InitialiseStatus("init")

				Expect(releaseA.Status.Signature).NotTo(Equal(releaseB.Status.Signature))
			})

			It("should produce different signatures for releases with different revision IDs", func() {
				releaseA := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "test-target",
						Revisions: []Revision{
							{Name: "app", ID: "abc123"},
						},
					},
				}
				releaseB := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "test-target",
						Revisions: []Revision{
							{Name: "app", ID: "xyz789"},
						},
					},
				}

				releaseA.InitialiseStatus("init")
				releaseB.InitialiseStatus("init")

				Expect(releaseA.Status.Signature).NotTo(Equal(releaseB.Status.Signature))
			})

			It("should produce different signatures for releases with different revision names", func() {
				releaseA := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "test-target",
						Revisions: []Revision{
							{Name: "app-a", ID: "abc123"},
						},
					},
				}
				releaseB := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "test-target",
						Revisions: []Revision{
							{Name: "app-b", ID: "abc123"},
						},
					},
				}

				releaseA.InitialiseStatus("init")
				releaseB.InitialiseStatus("init")

				Expect(releaseA.Status.Signature).NotTo(Equal(releaseB.Status.Signature))
			})

			It("should produce identical signatures for identical release configs", func() {
				releaseA := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "test-target",
						Revisions: []Revision{
							{Name: "app", ID: "abc123"},
						},
					},
				}
				releaseB := Release{
					ReleaseConfig: ReleaseConfig{
						TargetName: "test-target",
						Revisions: []Revision{
							{Name: "app", ID: "abc123"},
						},
					},
				}

				releaseA.InitialiseStatus("init")
				releaseB.InitialiseStatus("init")

				Expect(releaseA.Status.Signature).To(Equal(releaseB.Status.Signature))
			})
		})
	})

	Context("AutomatedRollbackPolicy", func() {
		var (
			policy    AutomatedRollbackPolicy
			fixedTime time.Time
		)

		BeforeEach(func() {
			fixedTime = time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
			policy = AutomatedRollbackPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-policy",
					Namespace: "default",
				},
				Spec: AutomatedRollbackPolicySpec{
					TargetName:  "test-target",
					ResetPeriod: &metav1.Duration{Duration: time.Duration(5 * time.Minute)},
				},
				Status: AutomatedRollbackPolicyStatus{
					WindowStartTime:  &metav1.Time{Time: fixedTime},
					ConsecutiveCount: 1,
				},
			}
		})

		Context("evaluatePolicyConstraints", func() {
			Context("when spec.enabled=false", func() {
				It("should return allowed=false with reason SetByUser", func() {
					policy.Spec.Enabled = false
					result := policy.EvaluatePolicyConstraints()
					Expect(result.Allowed).To(BeFalse())
					Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonSetByUser))
					Expect(result.Message).To(Equal("Automated rollback policy is disabled"))
				})
			})

			Context("when spec.enabled=true", func() {
				BeforeEach(func() {
					policy.Spec.Enabled = true
				})

				Context("when not within reset period", func() {
					BeforeEach(func() {
						// Make reset period expired: windowStartTime + resetPeriod is in the past
						policy.Spec.ResetPeriod = &metav1.Duration{Duration: 5 * time.Minute}
						policy.Status.WindowStartTime = &metav1.Time{Time: time.Now().Add(-10 * time.Minute)}
					})

					Context("when minInterval constraint is violated", func() {
						It("should return allowed=false with reason DisabledByController", func() {
							policy.Spec.MinInterval = &metav1.Duration{Duration: 5 * time.Minute}
							policy.Status.LastAutomatedRollbackTime = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}

							result := policy.EvaluatePolicyConstraints()
							Expect(result.Allowed).To(BeFalse())
							Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonDisabledByController))
							Expect(result.RequeueAfter).ToNot(BeNil())
							Expect(*result.RequeueAfter).To(BeNumerically("~", 3*time.Minute, 5*time.Second))
						})
					})

					Context("when minInterval constraint is satisfied", func() {
						It("should return allowed=true with windowExpired=true", func() {
							policy.Spec.MinInterval = &metav1.Duration{Duration: 5 * time.Minute}
							policy.Status.LastAutomatedRollbackTime = &metav1.Time{Time: time.Now().Add(-10 * time.Minute)}

							result := policy.EvaluatePolicyConstraints()
							Expect(result.Allowed).To(BeTrue())
							Expect(result.WindowExpired).To(BeTrue())
							Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonSetByUser))
						})
					})

					Context("when minInterval is nil", func() {
						It("should return allowed=true", func() {
							policy.Spec.MinInterval = nil

							result := policy.EvaluatePolicyConstraints()
							Expect(result.Allowed).To(BeTrue())
							Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonSetByUser))
						})
					})
				})

				Context("when within reset period", func() {
					BeforeEach(func() {
						// Make reset period active: windowStartTime + resetPeriod is in the future
						policy.Spec.ResetPeriod = &metav1.Duration{Duration: 10 * time.Minute}
						policy.Status.WindowStartTime = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}
					})

					Context("when maxConsecutiveRollbacks is reached", func() {
						It("should return allowed=false with requeueAfter set", func() {
							maxRollbacks := int32(3)
							policy.Spec.MaxConsecutiveRollbacks = &maxRollbacks
							policy.Status.ConsecutiveCount = 3

							result := policy.EvaluatePolicyConstraints()
							Expect(result.Allowed).To(BeFalse())
							Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonDisabledByController))
							Expect(result.RequeueAfter).ToNot(BeNil())
							Expect(*result.RequeueAfter).To(BeNumerically("~", 8*time.Minute, 5*time.Second))
						})
					})

					Context("when maxConsecutiveRollbacks is not reached", func() {
						BeforeEach(func() {
							maxRollbacks := int32(5)
							policy.Spec.MaxConsecutiveRollbacks = &maxRollbacks
							policy.Status.ConsecutiveCount = 2
						})

						Context("when minInterval constraint is violated", func() {
							It("should return allowed=false with requeueAfter set", func() {
								policy.Spec.MinInterval = &metav1.Duration{Duration: 5 * time.Minute}
								policy.Status.LastAutomatedRollbackTime = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}

								result := policy.EvaluatePolicyConstraints()
								Expect(result.Allowed).To(BeFalse())
								Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonDisabledByController))
								Expect(result.RequeueAfter).ToNot(BeNil())
								Expect(*result.RequeueAfter).To(BeNumerically("~", 3*time.Minute, 5*time.Second))
							})
						})

						Context("when minInterval constraint is satisfied", func() {
							It("should return allowed=true with windowExpired=false", func() {
								policy.Spec.MinInterval = &metav1.Duration{Duration: 5 * time.Minute}
								policy.Status.LastAutomatedRollbackTime = &metav1.Time{Time: time.Now().Add(-10 * time.Minute)}

								result := policy.EvaluatePolicyConstraints()
								Expect(result.Allowed).To(BeTrue())
								Expect(result.WindowExpired).To(BeFalse())
								Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonSetByUser))
							})
						})
					})

					Context("when maxConsecutiveRollbacks is nil", func() {
						It("should not enforce the limit", func() {
							policy.Spec.MaxConsecutiveRollbacks = nil
							policy.Status.ConsecutiveCount = 100

							result := policy.EvaluatePolicyConstraints()
							Expect(result.Allowed).To(BeTrue())
							Expect(result.Reason).To(Equal(AutomatedRollbackPolicyReasonSetByUser))
						})
					})
				})
			})
		})
	})
})
