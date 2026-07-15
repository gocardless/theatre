package acceptance

import (
	"context"
	"fmt"
	"sync/atomic"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	kitlog "github.com/go-kit/kit/log"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	namespace           = "theatre-deploy"
	acceptanceTestLabel = "theatre-acceptance-test"
)

var (
	scheme      = runtime.NewScheme()
	testCounter atomic.Int64
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = deployv1alpha1.AddToScheme(scheme)
	_ = analysisv1alpha1.AddToScheme(scheme)
}

type Runner struct{}

func (r *Runner) Name() string {
	return "cmd/rollback-manager/acceptance"
}

func (r *Runner) Prepare(logger kitlog.Logger, config *rest.Config) error {
	return nil
}

func (r *Runner) Run(logger kitlog.Logger, config *rest.Config) {
	Describe("Automated Rollback", Ordered, func() {
		var (
			kubeClient         client.Client
			targetName         string
			previousTargetName string
		)

		BeforeAll(func(ctx context.Context) {
			kubeClient = newClient(config)
			waitForRollbackWebhook(ctx, kubeClient, logger)
		})

		AfterEach(func(ctx context.Context) {
			cleanupRollbackTestResources(ctx, kubeClient, targetName)
		})

		Specify("Happy Path", func(ctx context.Context) {
			By("Create a automated rollback policy")
			targetName = generateName("target")
			createPolicy(ctx, kubeClient, targetName, true)

			By("Create rollback analysis")
			previousTargetName = generateName("previous-target")
			createAnalysisTemplate(ctx, kubeClient, targetName, previousTargetName, "health")
			createAnalysisTemplate(ctx, kubeClient, targetName, targetName, "rollback")

			By("Create releases")
			previousRelease := createActiveReleaseWithLabels(ctx, kubeClient, targetName, map[string]string{"target-name": previousTargetName})
			previousAnalysisRun := expectAnalysisRunCreated(ctx, kubeClient, previousTargetName, "health", targetName)
			completeAnalysisRun(ctx, kubeClient, previousAnalysisRun.Name, analysisv1alpha1.AnalysisPhaseSuccessful)
			expectReleaseHealthy(ctx, kubeClient, previousRelease.Name)
			activeRelease := createActiveReleaseWithLabels(ctx, kubeClient, targetName, map[string]string{"target-name": targetName})
			deactivateRelease(ctx, kubeClient, previousRelease.Name)
			setPreviousRelease(ctx, kubeClient, activeRelease.Name, previousRelease.Name)

			By("Fail release rollback analysis")
			analysisRun := expectAnalysisRunCreated(ctx, kubeClient, targetName, "rollback", targetName)
			completeAnalysisRun(ctx, kubeClient, analysisRun.Name, analysisv1alpha1.AnalysisPhaseFailed)
			expectRollbackRequired(ctx, kubeClient, activeRelease.Name)

			By("Expect rollback to be created")
			var rollback deployv1alpha1.Rollback
			Eventually(func(g Gomega) {
				rollbacks := listRollbacks(ctx, kubeClient, targetName)
				g.Expect(rollbacks).To(HaveLen(1))
				rollback = rollbacks[0]
				g.Expect(rollback.Spec.InitiatedBy.Principal).To(Equal("automated-rollback-controller"))
				g.Expect(rollback.Spec.ToReleaseRef.Name).To(Equal(previousRelease.Name))
			}).WithContext(ctx).Should(Succeed())

			By("Expect rollback to succeed")
			expectRollbackSucceeded(ctx, kubeClient, rollback.Name)

			By("Expect automated rollback policy to be disabled following rollback")
			Eventually(func(g Gomega) {
				policy := getPolicy(ctx, kubeClient, targetName)
				condition := meta.FindStatusCondition(policy.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal(deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
			}).WithContext(ctx).Should(Succeed())
		})
	})
}

func newClient(config *rest.Config) client.Client {
	kubeClient, err := client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")
	return kubeClient
}

func waitForRollbackWebhook(ctx context.Context, kubeClient client.Client, logger kitlog.Logger) {
	Eventually(func() bool {
		config := &admissionregistrationv1.MutatingWebhookConfiguration{}
		err := kubeClient.Get(ctx, client.ObjectKey{Name: "theatre-rollback-mutate"}, config)
		if err != nil {
			logger.Log("error", err)
			return false
		}
		return true
	}).WithContext(ctx).Should(Equal(true))
}

func cleanupRollbackTestResources(ctx context.Context, kubeClient client.Client, targetName string) {
	if targetName == "" {
		return
	}

	By("Cleaning up rollback acceptance test resources")

	foreground := metav1.DeletePropagationForeground
	labelSelector := client.MatchingLabels{acceptanceTestLabel: targetName}

	for _, obj := range []client.Object{
		&deployv1alpha1.AutomatedRollbackPolicy{},
		&deployv1alpha1.Release{},
		&analysisv1alpha1.AnalysisTemplate{},
	} {
		_ = kubeClient.DeleteAllOf(ctx, obj,
			client.InNamespace(namespace),
			labelSelector,
			client.PropagationPolicy(foreground),
		)
	}

	Eventually(func(g Gomega) {
		policyList := &deployv1alpha1.AutomatedRollbackPolicyList{}
		g.Expect(kubeClient.List(ctx, policyList, client.InNamespace(namespace), labelSelector)).To(Succeed())
		g.Expect(policyList.Items).To(BeEmpty())
	}).WithContext(ctx).Should(Succeed())
}

func generateName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, testCounter.Add(1))
}

func createReleaseWithLabels(ctx context.Context, kubeClient client.Client, targetName string, annotations, labels map[string]string) *deployv1alpha1.Release {
	if labels == nil {
		labels = map[string]string{}
	}
	labels[acceptanceTestLabel] = targetName

	release := &deployv1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name:        generateName("release"),
			Namespace:   namespace,
			Annotations: annotations,
			Labels:      labels,
		},
		ReleaseConfig: deployv1alpha1.ReleaseConfig{
			TargetName: targetName,
			Revisions: []deployv1alpha1.Revision{
				{Name: "application-revision", ID: generateName("app")},
				{Name: "infrastructure-revision", ID: generateName("infra")},
			},
		},
	}
	Expect(kubeClient.Create(ctx, release)).To(Succeed())
	waitReleaseInitialised(ctx, kubeClient, release.Name)
	return release
}

func createActiveReleaseWithLabels(ctx context.Context, kubeClient client.Client, targetName string, labels map[string]string) *deployv1alpha1.Release {
	release := createReleaseWithLabels(ctx, kubeClient, targetName, map[string]string{
		deployv1alpha1.AnnotationKeyReleaseActivate: deployv1alpha1.AnnotationValueReleaseActivateTrue,
	}, labels)
	Eventually(func() bool {
		return meta.IsStatusConditionTrue(getRelease(ctx, kubeClient, release.Name).Status.Conditions, deployv1alpha1.ReleaseConditionActive)
	}).WithContext(ctx).Should(BeTrue())
	return release
}

func waitReleaseInitialised(ctx context.Context, kubeClient client.Client, name string) {
	Eventually(func() bool {
		return getRelease(ctx, kubeClient, name).IsStatusInitialised()
	}).WithContext(ctx).Should(BeTrue())
}

func getRelease(ctx context.Context, kubeClient client.Client, name string) *deployv1alpha1.Release {
	release := &deployv1alpha1.Release{}
	Expect(kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, release)).To(Succeed())
	return release
}

func getRollback(ctx context.Context, kubeClient client.Client, name string) *deployv1alpha1.Rollback {
	rollback := &deployv1alpha1.Rollback{}
	Expect(kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, rollback)).To(Succeed())
	return rollback
}

func getPolicy(ctx context.Context, kubeClient client.Client, targetName string) *deployv1alpha1.AutomatedRollbackPolicy {
	policy := &deployv1alpha1.AutomatedRollbackPolicy{}
	Expect(kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: targetName}, policy)).To(Succeed())
	return policy
}

func createAnalysisTemplate(ctx context.Context, kubeClient client.Client, testName, targetName, analysisType string) *analysisv1alpha1.AnalysisTemplate {
	template := &analysisv1alpha1.AnalysisTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(analysisType + "-analysis"),
			Namespace: namespace,
			Labels: map[string]string{
				"target-name":       targetName,
				acceptanceTestLabel: testName,
				analysisType:        "true",
			},
		},
		Spec: analysisv1alpha1.AnalysisTemplateSpec{
			Metrics: []analysisv1alpha1.Metric{
				{
					Name: analysisType + "-check",
					Provider: analysisv1alpha1.MetricProvider{
						Prometheus: &analysisv1alpha1.PrometheusMetric{
							Address: "http://prometheus.invalid",
							Query:   "vector(1)",
						},
					},
				},
			},
		},
	}
	Expect(kubeClient.Create(ctx, template)).To(Succeed())
	return template
}

func expectAnalysisRunCreated(ctx context.Context, kubeClient client.Client, targetName, analysisType, testName string) analysisv1alpha1.AnalysisRun {
	var analysisRun analysisv1alpha1.AnalysisRun
	Eventually(func(g Gomega) {
		analysisRunList := &analysisv1alpha1.AnalysisRunList{}
		g.Expect(kubeClient.List(ctx, analysisRunList, client.InNamespace(namespace))).To(Succeed())

		var matching []analysisv1alpha1.AnalysisRun
		for _, item := range analysisRunList.Items {
			if item.Labels[acceptanceTestLabel] == testName &&
				item.Labels["target-name"] == targetName &&
				item.Labels[analysisType] == "true" {
				matching = append(matching, item)
			}
		}

		g.Expect(matching).To(HaveLen(1))
		analysisRun = matching[0]
	}).WithContext(ctx).Should(Succeed())
	return analysisRun
}

func completeAnalysisRun(ctx context.Context, kubeClient client.Client, name string, phase analysisv1alpha1.AnalysisPhase) {
	Eventually(func() error {
		analysisRun := &analysisv1alpha1.AnalysisRun{}
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, analysisRun); err != nil {
			return err
		}
		analysisRun.Status.Phase = phase
		return kubeClient.Update(ctx, analysisRun)
	}).WithContext(ctx).Should(Succeed())
}

func expectReleaseHealthy(ctx context.Context, kubeClient client.Client, releaseName string) {
	Eventually(func(g Gomega) {
		release := getRelease(ctx, kubeClient, releaseName)
		condition := meta.FindStatusCondition(release.Status.Conditions, deployv1alpha1.ReleaseConditionHealthy)
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(condition.Reason).To(Equal(deployv1alpha1.ReasonAnalysisSucceeded))
	}).WithContext(ctx).Should(Succeed())
}

func expectRollbackRequired(ctx context.Context, kubeClient client.Client, releaseName string) {
	Eventually(func(g Gomega) {
		release := getRelease(ctx, kubeClient, releaseName)
		condition := meta.FindStatusCondition(release.Status.Conditions, deployv1alpha1.ReleaseConditionRollbackRequired)
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(condition.Reason).To(Equal(deployv1alpha1.ReasonAnalysisFailed))
	}).WithContext(ctx).Should(Succeed())
}

func deactivateRelease(ctx context.Context, kubeClient client.Client, name string) {
	Eventually(func() error {
		release := getRelease(ctx, kubeClient, name)
		delete(release.Annotations, deployv1alpha1.AnnotationKeyReleaseActivate)
		return kubeClient.Update(ctx, release)
	}).WithContext(ctx).Should(Succeed())
	Eventually(func() bool {
		return meta.IsStatusConditionFalse(getRelease(ctx, kubeClient, name).Status.Conditions, deployv1alpha1.ReleaseConditionActive)
	}).WithContext(ctx).Should(BeTrue())
}

func setPreviousRelease(ctx context.Context, kubeClient client.Client, name, previousRelease string) {
	Eventually(func() error {
		release := getRelease(ctx, kubeClient, name)
		if release.Annotations == nil {
			release.Annotations = map[string]string{}
		}
		release.Annotations[deployv1alpha1.AnnotationKeyReleasePreviousRelease] = previousRelease
		return kubeClient.Update(ctx, release)
	}).WithContext(ctx).Should(Succeed())
	Eventually(func() string {
		return getRelease(ctx, kubeClient, name).Status.PreviousRelease.ReleaseRef
	}).WithContext(ctx).Should(Equal(previousRelease))
}

func createPolicy(ctx context.Context, kubeClient client.Client, targetName string, enabled bool) *deployv1alpha1.AutomatedRollbackPolicy {
	policy := &deployv1alpha1.AutomatedRollbackPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetName,
			Namespace: namespace,
			Labels: map[string]string{
				acceptanceTestLabel: targetName,
			},
		},
		Spec: deployv1alpha1.AutomatedRollbackPolicySpec{
			TargetName: targetName,
			Enabled:    enabled,
			Trigger: deployv1alpha1.RollbackTrigger{
				ConditionType:   deployv1alpha1.ReleaseConditionRollbackRequired,
				ConditionStatus: metav1.ConditionTrue,
			},
		},
	}
	Expect(kubeClient.Create(ctx, policy)).To(Succeed())
	return policy
}

func listRollbacks(ctx context.Context, kubeClient client.Client, targetName string) []deployv1alpha1.Rollback {
	rollbackList := &deployv1alpha1.RollbackList{}
	Expect(kubeClient.List(ctx, rollbackList, client.InNamespace(namespace))).To(Succeed())
	var ret []deployv1alpha1.Rollback
	for _, rollback := range rollbackList.Items {
		if rollback.Spec.ToReleaseRef.Target == targetName {
			ret = append(ret, rollback)
		}
	}
	return ret
}

func expectRollbackSucceeded(ctx context.Context, kubeClient client.Client, name string) {
	Eventually(func(g Gomega) {
		rollback := getRollback(ctx, kubeClient, name)
		condition := meta.FindStatusCondition(rollback.Status.Conditions, deployv1alpha1.RollbackConditionSucceded)
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(rollback.Status.CompletionTime).NotTo(BeNil())
	}).WithContext(ctx).Should(Succeed())
}
