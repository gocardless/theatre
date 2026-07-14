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
	Describe("Automated Rollback", func() {
		var (
			kubeClient         client.Client
			targetName         string
			previousTargetName string
		)

		BeforeEach(func() {
			kubeClient = newClient(config)
			waitForRollbackWebhook(kubeClient, logger)
		})

		AfterEach(func() {
			cleanupRollbackTestResources(kubeClient, targetName)
		})

		Specify("Happy Path", func() {
			By("Create a automated rollback policy")
			targetName = generateTargetName()
			createPolicy(kubeClient, targetName, true)

			By("Create rollback analysis")
			previousTargetName = generateName("previous-target")
			createAnalysisTemplate(kubeClient, targetName, previousTargetName, "health", "true")
			createAnalysisTemplate(kubeClient, targetName, targetName, "rollback", "true")

			By("Create releases")
			previousRelease := createActiveReleaseWithLabels(kubeClient, targetName, map[string]string{"target-name": previousTargetName})
			previousAnalysisRun := expectAnalysisRunCreated(kubeClient, previousTargetName, "health", "true", targetName)
			completeAnalysisRun(kubeClient, previousAnalysisRun.Name, analysisv1alpha1.AnalysisPhaseSuccessful)
			expectReleaseHealthy(kubeClient, previousRelease.Name)
			activeRelease := createActiveReleaseWithLabels(kubeClient, targetName, map[string]string{"target-name": targetName})
			deactivateRelease(kubeClient, previousRelease.Name)
			setPreviousRelease(kubeClient, activeRelease.Name, previousRelease.Name)

			By("Fail release rollback analysis")
			analysisRun := expectAnalysisRunCreated(kubeClient, targetName, "rollback", "true", targetName)
			completeAnalysisRun(kubeClient, analysisRun.Name, analysisv1alpha1.AnalysisPhaseFailed)
			expectRollbackRequired(kubeClient, activeRelease.Name)

			By("Expect rollback to be created")
			var rollback deployv1alpha1.Rollback
			Eventually(func(g Gomega) {
				rollbacks := listRollbacks(kubeClient, targetName)
				g.Expect(rollbacks).To(HaveLen(1))
				rollback = rollbacks[0]
				g.Expect(rollback.Spec.InitiatedBy.Principal).To(Equal("automated-rollback-controller"))
				g.Expect(rollback.Spec.ToReleaseRef.Name).To(Equal(previousRelease.Name))
			}).Should(Succeed())

			By("Expect rollback to succeed")
			expectRollbackSucceeded(kubeClient, rollback.Name)

			By("Expect automated rollback policy to be disabled following rollback")
			Eventually(func(g Gomega) {
				policy := getPolicy(kubeClient, targetName)
				condition := meta.FindStatusCondition(policy.Status.Conditions, deployv1alpha1.AutomatedRollbackPolicyConditionActive)
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal(deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController))
			}).Should(Succeed())
		})
	})
}

func newClient(config *rest.Config) client.Client {
	kubeClient, err := client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")
	return kubeClient
}

func waitForRollbackWebhook(kubeClient client.Client, logger kitlog.Logger) {
	Eventually(func() bool {
		config := &admissionregistrationv1.MutatingWebhookConfiguration{}
		err := kubeClient.Get(context.TODO(), client.ObjectKey{Name: "theatre-rollback-mutate"}, config)
		if err != nil {
			logger.Log("error", err)
			return false
		}
		return true
	}).Should(Equal(true))
}

func cleanupRollbackTestResources(kubeClient client.Client, targetName string) {
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
		_ = kubeClient.DeleteAllOf(context.TODO(), obj,
			client.InNamespace(namespace),
			labelSelector,
			client.PropagationPolicy(foreground),
		)
	}

	Eventually(func(g Gomega) {
		policyList := &deployv1alpha1.AutomatedRollbackPolicyList{}
		g.Expect(kubeClient.List(context.TODO(), policyList, client.InNamespace(namespace), labelSelector)).To(Succeed())
		g.Expect(policyList.Items).To(BeEmpty())
	}).Should(Succeed())
}

func generateName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, testCounter.Add(1))
}

func generateTargetName() string {
	return generateName("target")
}

func createReleaseWithLabels(kubeClient client.Client, targetName string, annotations, labels map[string]string) *deployv1alpha1.Release {
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
	Expect(kubeClient.Create(context.TODO(), release)).To(Succeed())
	waitReleaseInitialised(kubeClient, release.Name)
	return release
}

func createActiveReleaseWithLabels(kubeClient client.Client, targetName string, labels map[string]string) *deployv1alpha1.Release {
	release := createReleaseWithLabels(kubeClient, targetName, map[string]string{
		deployv1alpha1.AnnotationKeyReleaseActivate: deployv1alpha1.AnnotationValueReleaseActivateTrue,
	}, labels)
	Eventually(func() bool {
		return meta.IsStatusConditionTrue(getRelease(kubeClient, release.Name).Status.Conditions, deployv1alpha1.ReleaseConditionActive)
	}).Should(BeTrue())
	return release
}

func waitReleaseInitialised(kubeClient client.Client, name string) {
	Eventually(func() bool {
		return getRelease(kubeClient, name).IsStatusInitialised()
	}).Should(BeTrue())
}

func getRelease(kubeClient client.Client, name string) *deployv1alpha1.Release {
	release := &deployv1alpha1.Release{}
	Expect(kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: name}, release)).To(Succeed())
	return release
}

func getRollback(kubeClient client.Client, name string) *deployv1alpha1.Rollback {
	rollback := &deployv1alpha1.Rollback{}
	Expect(kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: name}, rollback)).To(Succeed())
	return rollback
}

func getPolicy(kubeClient client.Client, targetName string) *deployv1alpha1.AutomatedRollbackPolicy {
	policy := &deployv1alpha1.AutomatedRollbackPolicy{}
	Expect(kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: targetName}, policy)).To(Succeed())
	return policy
}

func createAnalysisTemplate(kubeClient client.Client, testName, targetName, analysisType, analysisValue string) *analysisv1alpha1.AnalysisTemplate {
	template := &analysisv1alpha1.AnalysisTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(analysisType + "-analysis"),
			Namespace: namespace,
			Labels: map[string]string{
				"target-name":       targetName,
				acceptanceTestLabel: testName,
				analysisType:        analysisValue,
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
	Expect(kubeClient.Create(context.TODO(), template)).To(Succeed())
	return template
}

func expectAnalysisRunCreated(kubeClient client.Client, targetName, analysisType, analysisValue, testName string) analysisv1alpha1.AnalysisRun {
	var analysisRun analysisv1alpha1.AnalysisRun
	Eventually(func(g Gomega) {
		analysisRunList := &analysisv1alpha1.AnalysisRunList{}
		g.Expect(kubeClient.List(context.TODO(), analysisRunList, client.InNamespace(namespace))).To(Succeed())

		var matching []analysisv1alpha1.AnalysisRun
		for _, item := range analysisRunList.Items {
			if item.Labels[acceptanceTestLabel] == testName &&
				item.Labels["target-name"] == targetName &&
				item.Labels[analysisType] == analysisValue {
				matching = append(matching, item)
			}
		}

		g.Expect(matching).To(HaveLen(1))
		analysisRun = matching[0]
	}).Should(Succeed())
	return analysisRun
}

func completeAnalysisRun(kubeClient client.Client, name string, phase analysisv1alpha1.AnalysisPhase) {
	Eventually(func() error {
		analysisRun := &analysisv1alpha1.AnalysisRun{}
		if err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: name}, analysisRun); err != nil {
			return err
		}
		analysisRun.Status.Phase = phase
		return kubeClient.Update(context.TODO(), analysisRun)
	}).Should(Succeed())
}

func expectReleaseHealthy(kubeClient client.Client, releaseName string) {
	Eventually(func(g Gomega) {
		release := getRelease(kubeClient, releaseName)
		condition := meta.FindStatusCondition(release.Status.Conditions, deployv1alpha1.ReleaseConditionHealthy)
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(condition.Reason).To(Equal(deployv1alpha1.ReasonAnalysisSucceeded))
	}).Should(Succeed())
}

func expectRollbackRequired(kubeClient client.Client, releaseName string) {
	Eventually(func(g Gomega) {
		release := getRelease(kubeClient, releaseName)
		condition := meta.FindStatusCondition(release.Status.Conditions, deployv1alpha1.ReleaseConditionRollbackRequired)
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(condition.Reason).To(Equal(deployv1alpha1.ReasonAnalysisFailed))
	}).Should(Succeed())
}

func deactivateRelease(kubeClient client.Client, name string) {
	Eventually(func() error {
		release := getRelease(kubeClient, name)
		delete(release.Annotations, deployv1alpha1.AnnotationKeyReleaseActivate)
		return kubeClient.Update(context.TODO(), release)
	}).Should(Succeed())
	Eventually(func() bool {
		return meta.IsStatusConditionFalse(getRelease(kubeClient, name).Status.Conditions, deployv1alpha1.ReleaseConditionActive)
	}).Should(BeTrue())
}

func setPreviousRelease(kubeClient client.Client, name, previousRelease string) {
	Eventually(func() error {
		release := getRelease(kubeClient, name)
		if release.Annotations == nil {
			release.Annotations = map[string]string{}
		}
		release.Annotations[deployv1alpha1.AnnotationKeyReleasePreviousRelease] = previousRelease
		return kubeClient.Update(context.TODO(), release)
	}).Should(Succeed())
	Eventually(func() string {
		return getRelease(kubeClient, name).Status.PreviousRelease.ReleaseRef
	}).Should(Equal(previousRelease))
}

func createPolicy(kubeClient client.Client, targetName string, enabled bool) *deployv1alpha1.AutomatedRollbackPolicy {
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
	Expect(kubeClient.Create(context.TODO(), policy)).To(Succeed())
	return policy
}

func listRollbacks(kubeClient client.Client, targetName string) []deployv1alpha1.Rollback {
	rollbackList := &deployv1alpha1.RollbackList{}
	Expect(kubeClient.List(context.TODO(), rollbackList, client.InNamespace(namespace))).To(Succeed())
	var ret []deployv1alpha1.Rollback
	for _, rollback := range rollbackList.Items {
		if rollback.Spec.ToReleaseRef.Target == targetName {
			ret = append(ret, rollback)
		}
	}
	return ret
}

func expectRollbackSucceeded(kubeClient client.Client, name string) {
	Eventually(func(g Gomega) {
		rollback := getRollback(kubeClient, name)
		condition := meta.FindStatusCondition(rollback.Status.Conditions, deployv1alpha1.RollbackConditionSucceded)
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(rollback.Status.CompletionTime).NotTo(BeNil())
	}).Should(Succeed())
}
