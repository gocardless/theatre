package integration

import (
	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
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
		// releaseLabels select the AnalysisTemplate below. Automated rollbacks
		// are only supported with analysis enabled, so every release here is
		// analysed, and its RollbackRequired condition comes from a mocked
		// AnalysisRun.
		releaseLabels map[string]string
	)

	BeforeEach(func() {
		testNamespace = setupTestNamespace(ctx)
		targetName = generateTargetName()
		policy = generatePolicy(testNamespace, targetName, nil)
		releaseLabels = map[string]string{"app": targetName}

		createAnalysisTemplate(ctx, testNamespace, "rollback-template", releaseLabels,
			map[string]string{"health": "true", "rollback": "true"})
	})

	Describe("Policy evaluation", func() {
		Context("when policy is disabled", func() {
			BeforeEach(func() {
				policy.Spec.Enabled = false
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())
			})

			It("should not trigger rollback even if release meets trigger condition", func() {
				createActiveReleaseWithRollbackRequired(testNamespace, targetName, releaseLabels)
				expectNoRollbackCreated(testNamespace)
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
				release = createActiveReleaseWithRollbackRequired(testNamespace, targetName, releaseLabels)
			})

			It("should create a Rollback with correct spec and initiatedBy", func() {
				By("Waiting for Rollback to be created")
				rollback := expectRollbackCreated(testNamespace)

				By("Verifying Rollback spec")
				Expect(rollback.Spec.ToReleaseRef.Target).To(Equal(targetName))
				Expect(rollback.Spec.Reason).To(ContainSubstring(release.Name))
				Expect(rollback.Spec.Reason).To(ContainSubstring(deployv1alpha1.ReleaseConditionRollbackRequired))
				Expect(rollback.Spec.InitiatedBy.Principal).To(Equal("automated-rollback-controller"))
				Expect(rollback.Spec.InitiatedBy.Type).To(Equal("system"))
			})

			It("should update policy status with lastAutomatedRollbackTime", func() {
				By("Waiting for Rollback to be created")
				expectRollbackCreated(testNamespace)

				By("Verifying policy status is updated")
				Eventually(func(g Gomega) {
					p := &deployv1alpha1.AutomatedRollbackPolicy{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p)).To(Succeed())
					g.Expect(p.Status.LastAutomatedRollbackTime).NotTo(BeNil())
					g.Expect(p.Status.Conditions).NotTo(BeEmpty())
					cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
					g.Expect(cond).NotTo(BeNil())
					g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(cond.Reason).To(Equal(deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
				}).Should(Succeed())
			})

			It("should re-enable automation, if a new release recovers from rollback", func() {
				By("Waiting for policy to be disabled")
				Eventually(func() bool {
					p := &deployv1alpha1.AutomatedRollbackPolicy{}
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p); err != nil {
						return false
					}
					cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
					return cond != nil && cond.Status == metav1.ConditionFalse
				}).Should(BeTrue())

				By("Deactivating the old release")
				oldRelease := &deployv1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(release), oldRelease)).To(Succeed())
				oldRelease.Annotations[deployv1alpha1.AnnotationKeyReleaseActivate] = "false"
				Expect(k8sClient.Update(ctx, oldRelease)).To(Succeed())

				By("Waiting for old release to be deactivated")
				Eventually(func() bool {
					r := &deployv1alpha1.Release{}
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(oldRelease), r); err != nil {
						return false
					}
					return !r.IsConditionActiveTrue()
				}).Should(BeTrue())

				By("Creating a new active release")
				newRelease := createAnalysisRelease(ctx, testNamespace, targetName, releaseLabels, true)

				By("Letting the new release's analysis succeed")
				completeReleaseAnalysis(testNamespace, newRelease, analysisv1alpha1.AnalysisPhaseSuccessful)

				By("Waiting for RollbackRequired=False on the new release")
				expectReleaseCondition(newRelease, deployv1alpha1.ReleaseConditionRollbackRequired,
					metav1.ConditionFalse, deployv1alpha1.ReasonAnalysisSucceeded)

				By("Verifying policy is re-enabled")
				Eventually(func() bool {
					p := &deployv1alpha1.AutomatedRollbackPolicy{}
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), p); err != nil {
						return false
					}
					cond := meta.FindStatusCondition(p.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
					return cond != nil && cond.Status == metav1.ConditionTrue
				}).Should(BeTrue())
			})
		})

		Context("when policy has a rollback template", func() {
			BeforeEach(func() {
				By("Creating policy with deploymentOptions")
				policy.Spec.RollbackTemplate = deployv1alpha1.RollbackTemplate{
					Metadata: deployv1alpha1.RollbackTemplateMetadata{
						Labels: map[string]string{
							"test": "label",
						},
						Annotations: map[string]string{
							"test": "annotation",
						},
					},
					Spec: deployv1alpha1.RollbackTemplateSpec{
						DeploymentOptions: map[string]apiextv1.JSON{
							"skip_canary": {Raw: []byte(`true`)},
							"timeout":     {Raw: []byte(`300`)},
						},
					},
				}
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())

				By("Waiting for policy to be reconciled")
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), &deployv1alpha1.AutomatedRollbackPolicy{})
				}).Should(Succeed())

				By("Creating active release with trigger condition")
				createActiveReleaseWithRollbackRequired(testNamespace, targetName, releaseLabels)
			})

			It("should pass deploymentOptions from policy to rollback", func() {
				By("Waiting for Rollback to be created")
				rollback := expectRollbackCreated(testNamespace)

				By("Verifying deploymentOptions are passed to rollback")
				Expect(rollback.Spec.DeploymentOptions).To(HaveKey("skip_canary"))
				Expect(rollback.Spec.DeploymentOptions).To(HaveKey("timeout"))

				By("Verifying metadata is passed to rollback")
				Expect(rollback.Labels).To(HaveKey("test"))
				Expect(rollback.Annotations).To(HaveKey("test"))
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
				release := createAnalysisRelease(ctx, testNamespace, targetName, releaseLabels, true)

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

				By("Failing the release's analysis, setting RollbackRequired=True")
				completeReleaseAnalysis(testNamespace, release, analysisv1alpha1.AnalysisPhaseFailed)
				expectReleaseCondition(release, deployv1alpha1.ReleaseConditionRollbackRequired,
					metav1.ConditionTrue, deployv1alpha1.ReasonAnalysisFailed)
			})

			It("should not create another Rollback", func() {
				By("Verifying no additional Rollback is created")
				Consistently(func() int {
					rollbackList := &deployv1alpha1.RollbackList{}
					Expect(k8sClient.List(ctx, rollbackList, client.InNamespace(testNamespace))).To(Succeed())
					return len(rollbackList.Items)
				}).Should(Equal(1))
			})
		})

		Context("when no active release exists", func() {
			BeforeEach(func() {
				By("Creating enabled policy without any release")
				Expect(k8sClient.Create(ctx, policy)).To(Succeed())
			})

			It("should not create a Rollback", func() {
				expectNoRollbackCreated(testNamespace)
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
				expectNoRollbackCreated(testNamespace)
			})
		})
	})
})

// Helper functions

// createActiveReleaseWithRollbackRequired creates an active, analysed release
// and fails its AnalysisRun, which is what sets RollbackRequired=True in
// production. It returns once the condition is observed.
func createActiveReleaseWithRollbackRequired(namespace, targetName string, labels map[string]string) *deployv1alpha1.Release {
	By("Creating an active release")
	release := createAnalysisRelease(ctx, namespace, targetName, labels, true)

	By("Failing the release's analysis")
	completeReleaseAnalysis(namespace, release, analysisv1alpha1.AnalysisPhaseFailed)

	By("Verifying the RollbackRequired condition was set")
	expectReleaseCondition(release, deployv1alpha1.ReleaseConditionRollbackRequired,
		metav1.ConditionTrue, deployv1alpha1.ReasonAnalysisFailed)

	return release
}

// completeReleaseAnalysis waits for the release's AnalysisRun to be created by
// the release controller, then drives it to a terminal phase.
func completeReleaseAnalysis(namespace string, release *deployv1alpha1.Release, phase analysisv1alpha1.AnalysisPhase) {
	run := awaitAnalysisRuns(namespace, release, 1)[0]
	setAnalysisRunPhase(ctx, &run, phase)
}

// expectRollbackCreated waits for a Rollback to be created in the namespace and returns it.
func expectRollbackCreated(namespace string) *deployv1alpha1.Rollback {
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
func expectNoRollbackCreated(namespace string) {
	By("Verifying no Rollback is created")
	Consistently(func() int {
		rollbackList := &deployv1alpha1.RollbackList{}
		Expect(k8sClient.List(ctx, rollbackList, client.InNamespace(namespace))).To(Succeed())
		return len(rollbackList.Items)
	}).Should(Equal(0))
}

func generatePolicy(namespace, targetName string, opts map[string]apiextv1.JSON) *deployv1alpha1.AutomatedRollbackPolicy {
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
		policy.Spec.RollbackTemplate = deployv1alpha1.RollbackTemplate{
			Spec: deployv1alpha1.RollbackTemplateSpec{
				DeploymentOptions: opts,
			},
		}
	}

	return policy
}
