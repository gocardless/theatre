package integration

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/cicd"
)

var _ = Describe("RollbackReconciler", func() {
	var (
		testNamespace string
		release       *deployv1alpha1.Release
		rollback      *deployv1alpha1.Rollback
		k8sClient     client.Client
	)

	BeforeEach(func() {
		testNamespace = setupTestNamespace(ctx)
		release = createRelease(ctx, testNamespace, "default-target", nil)
		k8sClient = rollbackMgr.GetClient()
	})

	Describe("Basic rollback flow", func() {
		It("triggers deployment and sets InProgress condition", func() {
			rollback = newRollback(testNamespace, "test-rollback", release.Name, "Testing rollback")

			By("Creating rollback")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Verifying InProgress condition is set")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				cond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)
				// Check for either InProgress=True or InProgress=False with Completed reason (fast completion)
				return cond != nil && (cond.Status == metav1.ConditionTrue ||
					(cond.Status == metav1.ConditionFalse && cond.Reason == "Completed"))
			}).Should(BeTrue())
		})

		It("completes successfully when deployment succeeds", func() {
			// Configure deployer for this rollback
			deployer.SetTriggerResult(testNamespace, "success-rollback", TriggerResult{
				Result: &cicd.DeploymentResult{
					ID:      "deployment-123",
					URL:     "https://example.com/deployments/123",
					Status:  cicd.DeploymentStatusPending,
					Message: "Deployment created",
				},
			})
			deployer.SetStatusResult("deployment-123", StatusResult{
				Result: &cicd.DeploymentResult{
					ID:      "deployment-123",
					Status:  cicd.DeploymentStatusSucceeded,
					Message: "Deployment completed successfully",
				},
			})

			rollback = newRollback(testNamespace, "success-rollback", release.Name, "Testing success")

			By("Creating rollback")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Verifying rollback succeeds")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				cond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionSucceeded)
				return cond != nil && cond.Status == metav1.ConditionTrue
			}).Should(BeTrue())

			rb := &deployv1alpha1.Rollback{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb)).NotTo(HaveOccurred())

			// Verify deployment ID is set
			Expect(rb.Status.DeploymentID).To(Equal("deployment-123"))

			// Verify completion time is set
			Expect(rb.Status.CompletionTime).NotTo(BeNil())

			// Verify InProgress condition is cleared
			cond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("Completed"))
		})

		It("fails after max retries when deployment keeps failing", func() {
			// Use a unique deployment ID for this test to avoid collisions
			deploymentID := "deployment-" + testNamespace[:8]

			// Configure deployer for this rollback
			deployer.SetTriggerResult(testNamespace, "failing-rollback", TriggerResult{
				Result: &cicd.DeploymentResult{
					ID:      deploymentID,
					URL:     "https://example.com/deployments/" + deploymentID,
					Status:  cicd.DeploymentStatusPending,
					Message: "Deployment created",
				},
			})
			deployer.SetStatusResult(deploymentID, StatusResult{
				Result: &cicd.DeploymentResult{
					ID:      deploymentID,
					Status:  cicd.DeploymentStatusFailed,
					Message: "Deployment failed: container crashed",
				},
			})

			rollback = newRollback(testNamespace, "failing-rollback", release.Name, "Testing failure")

			By("Creating rollback")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Verifying rollback is terminally failed after max retries")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				cond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionSucceeded)
				inProgressCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)
				// Wait for terminal failure: Succeeded=False, InProgress=False, AttemptCount >= 3
				return cond != nil && cond.Status == metav1.ConditionFalse &&
					inProgressCond != nil && inProgressCond.Status == metav1.ConditionFalse &&
					rb.Status.AttemptCount >= 3 && rb.Status.CompletionTime != nil
			}, "5s", "100ms").Should(BeTrue())
		})

		It("fails immediately on non-retryable error", func() {
			// Configure deployer to return non-retryable error
			deployer.SetTriggerResult(testNamespace, "nonretryable-rollback", TriggerResult{
				Err: &cicd.DeployerError{
					Deployer:  "fake",
					Operation: "TriggerDeployment",
					Retryable: false,
					Err:       fmt.Errorf("authentication failed"),
				},
			})

			rollback = newRollback(testNamespace, "nonretryable-rollback", release.Name, "Testing non-retryable")

			By("Creating rollback")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Verifying rollback fails immediately")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				cond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionSucceeded)
				inProgressCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)
				// Wait for terminal failure: Succeeded=False, AttemptCount=1, InProgress=False, Message contains error
				return cond != nil && cond.Status == metav1.ConditionFalse && rb.Status.AttemptCount == 1 &&
					inProgressCond != nil && inProgressCond.Status == metav1.ConditionFalse &&
					rb.Status.CompletionTime != nil && strings.Contains(rb.Status.Message, "authentication failed")
			}).Should(BeTrue())
		})

		It("passes deployment options to deployer", func() {
			rollback = &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "options-rollback",
					Namespace: testNamespace,
				},
				Spec: deployv1alpha1.RollbackSpec{
					ToReleaseRef: deployv1alpha1.ReleaseReference{
						Target: release.ReleaseConfig.TargetName,
						Name:   release.Name,
					},
					Reason: "Testing options",
					DeploymentOptions: map[string]apiextv1.JSON{
						"skip_canary": {Raw: []byte("true")},
						"timeout":     {Raw: []byte("300")},
					},
				},
			}

			By("Creating rollback with options")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Verifying deployment URL contains options as parameters")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				// Options should be encoded in the deployment URL
				url := rb.Status.DeploymentURL
				return strings.Contains(url, "skip_canary=true") && strings.Contains(url, "timeout=300")
			}).Should(BeTrue())
		})

		It("fails when target release does not exist", func() {
			rollback = newRollback(testNamespace, "missing-target", "nonexistent-release", "Testing missing release")

			By("Creating rollback")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Verifying rollback does not succeed (release not found)")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				cond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionSucceeded)
				inProgressCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)
				// Should have failed with release not found
				return cond != nil && cond.Status == metav1.ConditionFalse &&
					inProgressCond != nil && inProgressCond.Status == metav1.ConditionFalse &&
					rb.Status.CompletionTime != nil && strings.Contains(rb.Status.Message, "not found")
			}, "1s", "100ms").Should(BeTrue())
		})

		It("exits early when dryRun is enabled", func() {
			rollback = newRollback(testNamespace, "dry-run-test", release.Name, "Testing dry run")
			rollback.Spec.DryRun = true

			By("Creating rollback with dryRun enabled")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Verifying rollback completes immediately with dryRun message")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				dryRunCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionDryRun)
				successCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionSucceeded)
				inProgressCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)

				return dryRunCond != nil && dryRunCond.Status == metav1.ConditionTrue &&
					successCond == nil && inProgressCond == nil &&
					strings.Contains(rb.Status.Message, "Dry-run mode enabled")
			}, "1s", "100ms").Should(BeTrue())
		})

		It("should run rollback as normal when dryRun transition from true to false", func() {
			rollback = newRollback(testNamespace, "dry-run-transition-test", release.Name, "Testing dry run transition")
			rollback.Spec.DryRun = true

			By("Creating rollback with dryRun enabled")
			Expect(k8sClient.Create(ctx, rollback)).NotTo(HaveOccurred())

			By("Updating rollback to disable dryRun")
			Eventually(func() error {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return err
				}
				rb.Spec.DryRun = false
				return k8sClient.Update(ctx, rb)
			}, "1s", "100ms").Should(Succeed())

			By("Verifying rollback completes with success")
			Eventually(func() bool {
				rb := &deployv1alpha1.Rollback{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rollback), rb); err != nil {
					return false
				}
				successCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionSucceeded)
				inProgressCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)
				dryRunCond := meta.FindStatusCondition(rb.Status.Conditions, deployv1alpha1.RollbackConditionDryRun)

				return successCond != nil && successCond.Status == metav1.ConditionTrue &&
					inProgressCond != nil && inProgressCond.Status == metav1.ConditionFalse &&
					dryRunCond == nil &&
					rb.Status.CompletionTime != nil
			}, "1s", "100ms").Should(BeTrue())
		})
	})
})

func newRollback(namespace, name, toRelease, reason string) *deployv1alpha1.Rollback {
	return &deployv1alpha1.Rollback{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: deployv1alpha1.RollbackSpec{
			ToReleaseRef: deployv1alpha1.ReleaseReference{
				Target: "default-target",
				Name:   toRelease,
			},
			Reason: reason,
		},
	}
}
