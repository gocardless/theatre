package deploy

import (
	"time"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
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

	Context("evaluateAndUpdatePolicyStatus", func() {
		Context("when evaluatePolicyConstraints returns allowed=true with windowExpired=true", func() {
			BeforeEach(func() {
				policy.Spec.Enabled = true
				// Set up state so window is expired: windowStartTime + resetPeriod is in the past
				policy.Spec.ResetPeriod = &metav1.Duration{Duration: 5 * time.Minute}
				policy.Status.WindowStartTime = &metav1.Time{Time: time.Now().Add(-10 * time.Minute)}
				policy.Status.ConsecutiveCount = 3
			})

			It("should reset windowStartTime and consecutiveCount", func() {
				result := evaluateAndUpdatePolicyStatus(&policy)

				Expect(result.Allowed).To(BeTrue())
				Expect(policy.Status.WindowStartTime).To(BeNil())
				Expect(policy.Status.ConsecutiveCount).To(Equal(int32(0)))
			})

			It("should set Active condition to True", func() {
				evaluateAndUpdatePolicyStatus(&policy)

				condition := meta.FindStatusCondition(policy.Status.Conditions, v1alpha1.AutomatedRollbackPolicyConditionActive)
				Expect(condition).ToNot(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			})
		})

		Context("when evaluatePolicyConstraints returns allowed=true with windowExpired=false", func() {
			BeforeEach(func() {
				policy.Spec.Enabled = true
				// Set up state so window is NOT expired: windowStartTime + resetPeriod is in the future
				policy.Spec.ResetPeriod = &metav1.Duration{Duration: 10 * time.Minute}
				policy.Status.WindowStartTime = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}
				policy.Status.ConsecutiveCount = 2
			})

			It("should not reset windowStartTime and consecutiveCount", func() {
				originalWindowStartTime := policy.Status.WindowStartTime

				result := evaluateAndUpdatePolicyStatus(&policy)

				Expect(result.Allowed).To(BeTrue())
				Expect(policy.Status.WindowStartTime).To(Equal(originalWindowStartTime))
				Expect(policy.Status.ConsecutiveCount).To(Equal(int32(2)))
			})

			It("should set Active condition to True", func() {
				evaluateAndUpdatePolicyStatus(&policy)

				condition := meta.FindStatusCondition(policy.Status.Conditions, v1alpha1.AutomatedRollbackPolicyConditionActive)
				Expect(condition).ToNot(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			})
		})

		Context("when evaluatePolicyConstraints returns allowed=false", func() {
			BeforeEach(func() {
				policy.Spec.Enabled = false
			})

			It("should set Active condition to False", func() {
				evaluateAndUpdatePolicyStatus(&policy)

				condition := meta.FindStatusCondition(policy.Status.Conditions, v1alpha1.AutomatedRollbackPolicyConditionActive)
				Expect(condition).ToNot(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				Expect(condition.Reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
			})

			It("should propagate the reason and message from evaluation", func() {
				result := evaluateAndUpdatePolicyStatus(&policy)

				Expect(result.Allowed).To(BeFalse())
				Expect(result.Reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
				Expect(result.Message).To(Equal("Automated rollback policy is disabled"))
			})
		})
	})
})
