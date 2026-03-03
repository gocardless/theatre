package deploy

import (
	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("AutomatedRollback", func() {
	var (
		policy v1alpha1.AutomatedRollbackPolicy
	)

	BeforeEach(func() {
		policy = v1alpha1.AutomatedRollbackPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-policy",
				Namespace: "default",
			},
			Spec: v1alpha1.AutomatedRollbackPolicySpec{
				TargetName: "test-target",
			},
			Status: v1alpha1.AutomatedRollbackPolicyStatus{},
		}
	})

	Context("disableAutomationAfterRollback", func() {
		It("should set LastAutomatedRollbackTime and update condition", func() {
			disableAutomationAfterRollback(&policy)

			Expect(policy.Status.LastAutomatedRollbackTime).NotTo(BeNil())
			Expect(policy.Status.Conditions).NotTo(BeEmpty())
			Expect(policy.Status.Conditions[0].Type).To(Equal(v1alpha1.AutomatedRollbackPolicyConditionActive))
			Expect(policy.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
			Expect(policy.Status.Conditions[0].Reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
		})
	})

	Context("evaluateAndUpdatePolicyStatus", func() {
		Context("when evaluatePolicyConstraints returns allowed=true", func() {
			BeforeEach(func() {
				policy.Spec.Enabled = true
			})

			It("should set Active condition to True", func() {
				evaluateAndUpdatePolicyStatus(&policy, nil)

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
				evaluateAndUpdatePolicyStatus(&policy, nil)

				condition := meta.FindStatusCondition(policy.Status.Conditions, v1alpha1.AutomatedRollbackPolicyConditionActive)
				Expect(condition).ToNot(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				Expect(condition.Reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
			})

			It("should propagate the reason and message from evaluation", func() {
				result := evaluateAndUpdatePolicyStatus(&policy, nil)

				Expect(result.Allowed).To(BeFalse())
				Expect(result.Reason).To(Equal(v1alpha1.AutomatedRollbackPolicyReasonSetByUser))
				Expect(result.Message).To(Equal("Automated rollback policy is disabled"))
			})
		})
	})
})
