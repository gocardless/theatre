package deploy

import (
	"time"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("AutomatedRollbackController", func() {
	var (
		policy    v1alpha1.AutomatedRollbackPolicy
		fixedTime time.Time
	)

	BeforeEach(func() {
		fixedTime = time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
		policy = v1alpha1.AutomatedRollbackPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-policy",
				Namespace: "default",
			},
			Spec: v1alpha1.AutomatedRollbackPolicySpec{
				TargetName:  "test-target",
				ResetPeriod: &metav1.Duration{Duration: time.Duration(5 * time.Minute)},
			},
			Status: v1alpha1.AutomatedRollbackPolicyStatus{
				WindowStartTime:  &metav1.Time{Time: fixedTime},
				ConsecutiveCount: 1,
			},
		}
	})

	Context("updatePolicyStatus", func() {
		It("should not reset windowStartTime and consecutiveCount when resetPeriod is not set", func() {
			policy.Spec.ResetPeriod = nil
			updatePolicyStatus(&policy)
			Expect(policy.Status.WindowStartTime.Time).To(Equal(fixedTime))
			Expect(policy.Status.ConsecutiveCount).To(Equal(int32(2)))
		})

		It("should reset windowStartTime and consecutiveCount when resetPeriod is set and expired", func() {
			updatePolicyStatus(&policy)
			Expect(policy.Status.WindowStartTime.Time).ToNot(Equal(fixedTime))
			Expect(policy.Status.ConsecutiveCount).To(Equal(int32(1)))
		})

		It("should not reset windowStartTime and consecutiveCount when resetPeriod is set and not expired", func() {
			windowStartTime := time.Now().Add(-time.Duration(4 * time.Minute))
			policy.Status.WindowStartTime = &metav1.Time{Time: windowStartTime}
			updatePolicyStatus(&policy)
			Expect(policy.Status.WindowStartTime.Time).To(Equal(windowStartTime))
			Expect(policy.Status.ConsecutiveCount).To(Equal(int32(2)))
		})
	})

	Context("evaluatePolicyConstraints", func() {
		Context("when spec.enabled=false", func() {
			It("should return allowed=false with reason SetByUser", func() {
				policy.Spec.Enabled = false
				result := evaluatePolicyConstraints(&policy)
				Expect(result.allowed).To(BeFalse())
				Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
				Expect(result.message).To(Equal("Automated rollback policy is disabled"))
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

						result := evaluatePolicyConstraints(&policy)
						Expect(result.allowed).To(BeFalse())
						Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
						Expect(result.requeueAfter).ToNot(BeNil())
						Expect(*result.requeueAfter).To(BeNumerically("~", 3*time.Minute, 5*time.Second))
					})
				})

				Context("when minInterval constraint is satisfied", func() {
					It("should return allowed=true with windowExpired=true", func() {
						policy.Spec.MinInterval = &metav1.Duration{Duration: 5 * time.Minute}
						policy.Status.LastAutomatedRollbackTime = &metav1.Time{Time: time.Now().Add(-10 * time.Minute)}

						result := evaluatePolicyConstraints(&policy)
						Expect(result.allowed).To(BeTrue())
						Expect(result.windowExpired).To(BeTrue())
						Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
					})
				})

				Context("when minInterval is nil", func() {
					It("should return allowed=true", func() {
						policy.Spec.MinInterval = nil

						result := evaluatePolicyConstraints(&policy)
						Expect(result.allowed).To(BeTrue())
						Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
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

						result := evaluatePolicyConstraints(&policy)
						Expect(result.allowed).To(BeFalse())
						Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
						Expect(result.requeueAfter).ToNot(BeNil())
						Expect(*result.requeueAfter).To(BeNumerically("~", 8*time.Minute, 5*time.Second))
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

							result := evaluatePolicyConstraints(&policy)
							Expect(result.allowed).To(BeFalse())
							Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
							Expect(result.requeueAfter).ToNot(BeNil())
							Expect(*result.requeueAfter).To(BeNumerically("~", 3*time.Minute, 5*time.Second))
						})
					})

					Context("when minInterval constraint is satisfied", func() {
						It("should return allowed=true with windowExpired=false", func() {
							policy.Spec.MinInterval = &metav1.Duration{Duration: 5 * time.Minute}
							policy.Status.LastAutomatedRollbackTime = &metav1.Time{Time: time.Now().Add(-10 * time.Minute)}

							result := evaluatePolicyConstraints(&policy)
							Expect(result.allowed).To(BeTrue())
							Expect(result.windowExpired).To(BeFalse())
							Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
						})
					})
				})

				Context("when maxConsecutiveRollbacks is nil", func() {
					It("should not enforce the limit", func() {
						policy.Spec.MaxConsecutiveRollbacks = nil
						policy.Status.ConsecutiveCount = 100

						result := evaluatePolicyConstraints(&policy)
						Expect(result.allowed).To(BeTrue())
						Expect(result.reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
					})
				})
			})
		})
	})
})
