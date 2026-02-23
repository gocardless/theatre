package integration

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
)

var _ = Describe("AutomatedRollbackReconciler", func() {
	var (
		testNamespace string
		policy        *deployv1alpha1.AutomatedRollbackPolicy
		targetName    string
		k8sClient     client.Client
	)

	BeforeEach(func() {
		testNamespace = setupTestNamespace(ctx)
		k8sClient = releaseMgr.GetClient()
		targetName = generateTargetName()
		policy = createPolicy(testNamespace, targetName, nil)
	})

	Describe("Policy evaluation", func() {
		Context("when policy is disabled", func() {
			BeforeEach(func() {
				policy.Spec.Enabled = false
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())
			})

			It("should not trigger rollback even if release meets trigger condition", func() {
				createActiveReleaseWithTriggerCondition(k8sClient, testNamespace, targetName)
				expectNoRollbackCreated(k8sClient, testNamespace)
			})

			It("should set Active condition to False with reason SetByUser", func() {
				By("Verifying policy status has Active=False with reason SetByUser")
				Eventually(func(g Gomega) {
					p := &deployv1alpha1.AutomatedRollbackPolicy{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p)).To(Succeed())
					cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
					g.Expect(cond).NotTo(BeNil())
					g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(cond.Reason).To(Equal(deployv1alpha1.AutomatedRollbackPolicyReasonSetByUser))
				}).Should(Succeed())
			})
		})

		Context("when policy is enabled", func() {
			BeforeEach(func() {
				policy.Spec.Enabled = true
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())
			})

			It("should set Active condition to True", func() {
				By("Verifying policy status has Active=True")
				Eventually(func(g Gomega) {
					p := &deployv1alpha1.AutomatedRollbackPolicy{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p)).To(Succeed())
					cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
					g.Expect(cond).NotTo(BeNil())
					g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				}).Should(Succeed())
			})

			Context("when maxConsecutiveRollbacks is reached", func() {
				BeforeEach(func() {
					By("Waiting for policy to be reconciled")
					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
					}).Should(Succeed())

					By("Updating policy spec and status to simulate max consecutive rollbacks reached")
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
					maxRollbacks := int32(2)
					policy.Spec.MaxConsecutiveRollbacks = &maxRollbacks
					policy.Spec.ResetPeriod = &metav1.Duration{Duration: 10 * time.Minute}
					Expect(k8sClient.Update(ctx, policy)).To(Succeed())

					now := metav1.Now()
					policy.Status.ConsecutiveCount = 2
					policy.Status.WindowStartTime = &now
					Expect(k8sClient.Status().Update(ctx, policy)).To(Succeed())
				})

				It("should set Active condition to False with reason DisabledByController", func() {
					By("Verifying policy status has Active=False with reason DisabledByController")
					Eventually(func(g Gomega) {
						p := &deployv1alpha1.AutomatedRollbackPolicy{}
						g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p)).To(Succeed())
						cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
						g.Expect(cond).NotTo(BeNil())
						g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						g.Expect(cond.Reason).To(Equal(deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
					}).Should(Succeed())
				})

				It("should not trigger rollback even if release meets trigger condition", func() {
					createActiveReleaseWithTriggerCondition(k8sClient, testNamespace, targetName)
					expectNoRollbackCreated(k8sClient, testNamespace)
				})
			})

			Context("when minInterval has not elapsed", func() {
				BeforeEach(func() {
					By("Waiting for policy to be reconciled")
					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
					}).Should(Succeed())

					By("Updating policy spec and status to simulate minInterval not elapsed")
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
					policy.Spec.MinInterval = &metav1.Duration{Duration: 10 * time.Minute}
					Expect(k8sClient.Update(ctx, policy)).To(Succeed())

					fiveMinutesAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))
					policy.Status.LastAutomatedRollbackTime = &fiveMinutesAgo
					Expect(k8sClient.Status().Update(ctx, policy)).To(Succeed())
				})

				It("should not trigger rollback", func() {
					createActiveReleaseWithTriggerCondition(k8sClient, testNamespace, targetName)
					expectNoRollbackCreated(k8sClient, testNamespace)
				})

				It("should set Active condition to False with reason DisabledByController", func() {
					By("Verifying policy status indicates minInterval constraint and requeue time")
					Eventually(func(g Gomega) {
						p := &deployv1alpha1.AutomatedRollbackPolicy{}
						g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p)).To(Succeed())
						cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
						g.Expect(cond).NotTo(BeNil())
						g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						g.Expect(cond.Reason).To(Equal(deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
						g.Expect(cond.Message).To(ContainSubstring("Min interval"))
						g.Expect(cond.Message).To(ContainSubstring("Will be enabled again at"))
					}).Should(Succeed())
				})
			})

			Context("when resetPeriod has elapsed", func() {
				BeforeEach(func() {
					By("Waiting for policy to be reconciled")
					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
					}).Should(Succeed())

					By("Updating policy spec and status to simulate resetPeriod elapsed")
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
					policy.Spec.ResetPeriod = &metav1.Duration{Duration: 5 * time.Minute}
					Expect(k8sClient.Update(ctx, policy)).To(Succeed())

					tenMinutesAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))
					policy.Status.WindowStartTime = &tenMinutesAgo
					policy.Status.ConsecutiveCount = 3
					Expect(k8sClient.Status().Update(ctx, policy)).To(Succeed())
				})

				It("should reset windowStartTime and consecutiveCount and become active", func() {
					By("Verifying status is reset and policy becomes active after reconciliation")
					Eventually(func(g Gomega) {
						p := &deployv1alpha1.AutomatedRollbackPolicy{}
						g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p)).To(Succeed())
						g.Expect(p.Status.WindowStartTime).To(BeNil())
						g.Expect(p.Status.ConsecutiveCount).To(Equal(int32(0)))
						cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
						g.Expect(cond).NotTo(BeNil())
						g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
					}).Should(Succeed())
				})
			})
		})
	})

	Describe("Rollback triggering", func() {
		Context("when release meets trigger condition", func() {
			var release *deployv1alpha1.Release

			BeforeEach(func() {
				By("Creating enabled policy")
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())

				By("Waiting for policy to be reconciled")
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
				}).Should(Succeed())

				By("Creating active release with trigger condition")
				release = createActiveReleaseWithTriggerCondition(k8sClient, testNamespace, targetName)
			})

			It("should create a Rollback with correct spec and initiatedBy", func() {
				By("Waiting for Rollback to be created")
				rollback := expectRollbackCreated(k8sClient, testNamespace)

				By("Verifying Rollback spec")
				Expect(rollback.Spec.ToReleaseRef.Target).To(Equal(targetName))
				Expect(rollback.Spec.Reason).To(ContainSubstring(release.Name))
				Expect(rollback.Spec.Reason).To(ContainSubstring(deployv1alpha1.ReleaseConditionRollbackRequired))
				Expect(rollback.Spec.InitiatedBy.Principal).To(Equal("automated-rollback-controller"))
				Expect(rollback.Spec.InitiatedBy.Type).To(Equal("system"))
			})

			It("should update policy status with consecutiveCount, lastAutomatedRollbackTime, and windowStartTime", func() {
				By("Waiting for Rollback to be created")
				expectRollbackCreated(k8sClient, testNamespace)

				By("Verifying policy status is updated")
				Eventually(func(g Gomega) {
					p := &deployv1alpha1.AutomatedRollbackPolicy{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p)).To(Succeed())
					g.Expect(p.Status.ConsecutiveCount).To(Equal(int32(1)))
					g.Expect(p.Status.LastAutomatedRollbackTime).NotTo(BeNil())
					g.Expect(p.Status.WindowStartTime).NotTo(BeNil())
				}).Should(Succeed())
			})
		})

		Context("when policy has deploymentOptions", func() {
			BeforeEach(func() {
				By("Creating policy with deploymentOptions")
				policy.Spec.DeploymentOptions = map[string]apiextv1.JSON{
					"skip_canary": {Raw: []byte(`true`)},
					"timeout":     {Raw: []byte(`300`)},
				}
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())

				By("Waiting for policy to be reconciled")
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
				}).Should(Succeed())

				By("Creating active release with trigger condition")
				createActiveReleaseWithTriggerCondition(k8sClient, testNamespace, targetName)
			})

			It("should pass deploymentOptions from policy to rollback", func() {
				By("Waiting for Rollback to be created")
				rollback := expectRollbackCreated(k8sClient, testNamespace)

				By("Verifying deploymentOptions are passed to rollback")
				Expect(rollback.Spec.DeploymentOptions).To(HaveKey("skip_canary"))
				Expect(rollback.Spec.DeploymentOptions).To(HaveKey("timeout"))
			})
		})

		Context("when release already has a rollback", func() {
			BeforeEach(func() {
				By("Creating enabled policy")
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())

				By("Waiting for policy to be reconciled")
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
				}).Should(Succeed())

				By("Creating active release without trigger condition first")
				release := createRelease(ctx, testNamespace, targetName, map[string]string{
					deployv1alpha1.AnnotationKeyReleaseActivate: deployv1alpha1.AnnotationValueReleaseActivateTrue,
				})

				By("Waiting for release to be active")
				Eventually(func() bool {
					r := &deployv1alpha1.Release{}
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(release), r); err != nil {
						return false
					}
					return r.IsConditionActiveTrue()
				}).Should(BeTrue())

				By("Creating existing rollback with owner reference to release")
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(release), release)).To(Succeed())
				existingRollback := &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-rollback",
						Namespace: testNamespace,
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: deployv1alpha1.GroupVersion.String(),
							Kind:       "Release",
							Name:       release.Name,
							UID:        release.UID,
							Controller: ptr.To(true),
						}},
					},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{
							Target: targetName,
						},
						Reason: "Pre-existing rollback for testing",
					},
				}
				Expect(k8sClient.Create(ctx, existingRollback)).To(Succeed())

				By("Setting RollbackRequired=True condition on the release to trigger reconciliation")
				meta.SetStatusCondition(&release.Status.Conditions, metav1.Condition{
					Type:    deployv1alpha1.ReleaseConditionRollbackRequired,
					Status:  metav1.ConditionTrue,
					Reason:  deployv1alpha1.ReasonAnalysisFailed,
					Message: "Health check failed",
				})
				Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())
			})

			It("should not create another Rollback", func() {
				By("Verifying no additional Rollback is created")
				Consistently(func() int {
					rollbackList := &deployv1alpha1.RollbackList{}
					Expect(k8sClient.List(ctx, rollbackList, client.InNamespace(testNamespace))).To(Succeed())
					return len(rollbackList.Items)
				}, "2s", "200ms").Should(Equal(1))
			})
		})

		Context("when no active release exists", func() {
			BeforeEach(func() {
				By("Creating enabled policy without any release")
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())
			})

			It("should not create a Rollback", func() {
				expectNoRollbackCreated(k8sClient, testNamespace)
			})
		})

		Context("when release does not meet trigger condition", func() {
			BeforeEach(func() {
				By("Creating enabled policy")
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())

				By("Waiting for policy to be reconciled")
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
				}).Should(Succeed())

				By("Creating active release without trigger condition")
				release := createRelease(ctx, testNamespace, targetName, map[string]string{
					deployv1alpha1.AnnotationKeyReleaseActivate: deployv1alpha1.AnnotationValueReleaseActivateTrue,
				})

				By("Waiting for release to be active")
				Eventually(func() bool {
					r := &deployv1alpha1.Release{}
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(release), r); err != nil {
						return false
					}
					return r.IsConditionActiveTrue()
				}).Should(BeTrue())
			})

			It("should not create a Rollback", func() {
				expectNoRollbackCreated(k8sClient, testNamespace)
			})
		})
	})
})

// Helper functions

// createActiveReleaseWithTriggerCondition creates an active release and sets the RollbackRequired=True condition.
// It waits for the release to be active and for the condition to be set before returning.
func createActiveReleaseWithTriggerCondition(k8sClient client.Client, namespace, targetName string) *deployv1alpha1.Release {
	By("Creating an active release")
	release := createRelease(ctx, namespace, targetName, map[string]string{
		deployv1alpha1.AnnotationKeyReleaseActivate: deployv1alpha1.AnnotationValueReleaseActivateTrue,
	})

	By("Waiting for release to be active")
	Eventually(func() bool {
		r := &deployv1alpha1.Release{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(release), r); err != nil {
			return false
		}
		return r.IsConditionActiveTrue()
	}).Should(BeTrue())

	By("Setting RollbackRequired=True condition on the release")
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(release), release)).To(Succeed())
	meta.SetStatusCondition(&release.Status.Conditions, metav1.Condition{
		Type:    deployv1alpha1.ReleaseConditionRollbackRequired,
		Status:  metav1.ConditionTrue,
		Reason:  deployv1alpha1.ReasonAnalysisFailed,
		Message: "Health check failed",
	})
	Expect(k8sClient.Status().Update(ctx, release)).To(Succeed())

	By("Verifying the RollbackRequired condition was set")
	Eventually(func() bool {
		r := &deployv1alpha1.Release{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(release), r); err != nil {
			return false
		}
		return meta.IsStatusConditionTrue(r.Status.Conditions, deployv1alpha1.ReleaseConditionRollbackRequired)
	}).Should(BeTrue())

	return release
}

// expectRollbackCreated waits for a Rollback to be created in the namespace and returns it.
func expectRollbackCreated(k8sClient client.Client, namespace string) *deployv1alpha1.Rollback {
	var rollback *deployv1alpha1.Rollback
	Eventually(func() bool {
		rollbackList := &deployv1alpha1.RollbackList{}
		if err := k8sClient.List(ctx, rollbackList, client.InNamespace(namespace)); err != nil {
			return false
		}
		if len(rollbackList.Items) > 0 {
			rollback = &rollbackList.Items[0]
			return true
		}
		return false
	}).Should(BeTrue())
	return rollback
}

// expectNoRollbackCreated verifies that no Rollback resources are created in the namespace.
func expectNoRollbackCreated(k8sClient client.Client, namespace string) {
	By("Verifying no Rollback is created")
	Consistently(func() int {
		rollbackList := &deployv1alpha1.RollbackList{}
		Expect(k8sClient.List(ctx, rollbackList, client.InNamespace(namespace))).To(Succeed())
		return len(rollbackList.Items)
	}, "2s", "200ms").Should(Equal(0))
}

func createPolicy(namespace, targetName string, opts map[string]apiextv1.JSON) *deployv1alpha1.AutomatedRollbackPolicy {
	policy := &deployv1alpha1.AutomatedRollbackPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetName,
			Namespace: namespace,
		},
		Spec: deployv1alpha1.AutomatedRollbackPolicySpec{
			TargetName: targetName,
			Enabled:    true,
			Trigger: deployv1alpha1.RollbackTrigger{
				ConditionType:   deployv1alpha1.ReleaseConditionRollbackRequired,
				ConditionStatus: metav1.ConditionTrue,
			},
		},
	}

	if opts != nil {
		policy.Spec.DeploymentOptions = opts
	}

	return policy
}
