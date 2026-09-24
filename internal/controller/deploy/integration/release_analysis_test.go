package integration

import (
	"context"
	"fmt"
	"maps"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/deploy"
)

var _ = Describe("ReleaseController analysis", func() {
	var (
		testNamespace string
		releaseLabels map[string]string
		// templateLabels are applied on top of releaseLabels, so the template
		// matches the selector built from the release's labels.
		templateLabels map[string]string
		templateName   string

		release *v1alpha1.Release
		// releaseActive controls whether the release is created with the activate
		// annotation, which gates creation of new AnalysisRuns.
		releaseActive bool
		// releaseAnnotations are added to the release on creation, on top of the
		// deployment start time and activate annotations.
		releaseAnnotations map[string]string
	)

	BeforeEach(func() {
		testNamespace = setupTestNamespace(ctx)
		releaseLabels = map[string]string{"app": "analysis-" + randomHex(4)}
		templateLabels = map[string]string{"health": "true", "rollback": "true"}
		templateName = "template-" + randomHex(4)
		releaseActive = true
		releaseAnnotations = nil
	})

	JustBeforeEach(func() {
		createAnalysisTemplate(ctx, testNamespace, templateName, releaseLabels, templateLabels)
		release = createAnalysisRelease(ctx, testNamespace, "analysis-target", releaseLabels, releaseAnnotations, releaseActive)
	})

	Describe("AnalysisRun creation", func() {
		It("creates an AnalysisRun owned by the release for each matching template", func() {
			runs := awaitAnalysisRuns(testNamespace, release, 1)
			run := runs[0]

			By("naming it after the release and template")
			Expect(run.Name).To(Equal(deploy.GenerateAnalysisRunName(release.Name, templateName)))
			Expect(run.Namespace).To(Equal(testNamespace))

			By("setting the release as controller owner")
			owner := metav1.GetControllerOf(&run)
			Expect(owner).NotTo(BeNil())
			Expect(owner.Name).To(Equal(release.Name))
			Expect(owner.Kind).To(Equal("Release"))
			Expect(owner.APIVersion).To(Equal(v1alpha1.GroupVersion.String()))

			By("copying the release labels and the template's health/rollback labels")
			for key, value := range releaseLabels {
				Expect(run.Labels).To(HaveKeyWithValue(key, value))
			}
			Expect(run.Labels).To(HaveKeyWithValue("health", "true"))
			Expect(run.Labels).To(HaveKeyWithValue("rollback", "true"))

			By("copying the template's metrics")
			Expect(run.Spec.Metrics).To(HaveLen(1))
			Expect(run.Spec.Metrics[0].Name).To(Equal("test-metric"))
		})

		It("does not create duplicate AnalysisRuns on subsequent reconciles", func() {
			runs := awaitAnalysisRuns(testNamespace, release, 1)
			existing := runs[0].Name

			By("triggering another reconcile of the release")
			Eventually(func() error {
				fetched := &v1alpha1.Release{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(release), fetched); err != nil {
					return err
				}
				metav1.SetMetaDataAnnotation(&fetched.ObjectMeta, v1alpha1.AnnotationKeyReleasePreviousRelease, "some-previous-release")
				return k8sClient.Update(ctx, fetched)
			}).Should(Succeed())

			Eventually(func() string {
				fetched := &v1alpha1.Release{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(release), fetched)).To(Succeed())
				return fetched.Status.PreviousRelease.ReleaseRef
			}).Should(Equal("some-previous-release"))

			Consistently(func() []string {
				return analysisRunNames(testNamespace, release)
			}).Should(ConsistOf(existing))
		})

		It("ignores templates that do not match the release labels", func() {
			awaitAnalysisRuns(testNamespace, release, 1)

			By("creating a template with unrelated labels")
			createAnalysisTemplate(ctx, testNamespace, "unmatched-"+randomHex(4),
				map[string]string{"app": "someone-else"}, templateLabels)

			Consistently(func() int {
				return len(analysisRunNames(testNamespace, release))
			}).Should(Equal(1))
		})
	})

	Describe("Release conditions", func() {
		var run analysisv1alpha1.AnalysisRun

		JustBeforeEach(func() {
			run = awaitAnalysisRuns(testNamespace, release, 1)[0]
		})

		It("reports analysis as in progress while the AnalysisRun has not completed", func() {
			expectReleaseCondition(release, v1alpha1.ReleaseConditionHealthy, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisInProgress)
			expectReleaseCondition(release, v1alpha1.ReleaseConditionRollbackRequired, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisInProgress)

			setAnalysisRunPhase(ctx, &run, analysisv1alpha1.AnalysisPhaseRunning)

			expectReleaseCondition(release, v1alpha1.ReleaseConditionHealthy, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisInProgress)
			expectReleaseCondition(release, v1alpha1.ReleaseConditionRollbackRequired, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisInProgress)
		})

		It("reports the release as healthy when the AnalysisRun succeeds", func() {
			setAnalysisRunPhase(ctx, &run, analysisv1alpha1.AnalysisPhaseSuccessful)

			expectReleaseCondition(release, v1alpha1.ReleaseConditionHealthy, metav1.ConditionTrue, v1alpha1.ReasonAnalysisSucceeded)
			expectReleaseCondition(release, v1alpha1.ReleaseConditionRollbackRequired, metav1.ConditionFalse, v1alpha1.ReasonAnalysisSucceeded)
		})

		It("reports the release as unhealthy and requiring rollback when the AnalysisRun fails", func() {
			setAnalysisRunPhase(ctx, &run, analysisv1alpha1.AnalysisPhaseFailed)

			expectReleaseCondition(release, v1alpha1.ReleaseConditionHealthy, metav1.ConditionFalse, v1alpha1.ReasonAnalysisFailed)
			expectReleaseCondition(release, v1alpha1.ReleaseConditionRollbackRequired, metav1.ConditionTrue, v1alpha1.ReasonAnalysisFailed)
		})

		Context("when only the health template matches", func() {
			BeforeEach(func() {
				templateLabels = map[string]string{"health": "true"}
			})

			It("leaves the rollback condition unknown with no AnalysisRuns found", func() {
				setAnalysisRunPhase(ctx, &run, analysisv1alpha1.AnalysisPhaseSuccessful)

				expectReleaseCondition(release, v1alpha1.ReleaseConditionHealthy, metav1.ConditionTrue, v1alpha1.ReasonAnalysisSucceeded)
				expectReleaseCondition(release, v1alpha1.ReleaseConditionRollbackRequired, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisMissing)
			})
		})
	})

	Describe("Inactive releases", func() {
		Context("when the release was never activated", func() {
			BeforeEach(func() {
				releaseActive = false
			})

			It("does not create AnalysisRuns and reports analysis as missing", func() {
				expectReleaseCondition(release, v1alpha1.ReleaseConditionHealthy, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisMissing)

				Consistently(func() int {
					return len(analysisRunNames(testNamespace, release))
				}).Should(Equal(0))
			})
		})

		Context("when the release is deactivated after AnalysisRuns exist", func() {
			var run analysisv1alpha1.AnalysisRun

			JustBeforeEach(func() {
				run = awaitAnalysisRuns(testNamespace, release, 1)[0]
				deactivateRelease(ctx, release)
			})

			It("still parses results of existing AnalysisRuns", func() {
				setAnalysisRunPhase(ctx, &run, analysisv1alpha1.AnalysisPhaseFailed)

				expectReleaseCondition(release, v1alpha1.ReleaseConditionHealthy, metav1.ConditionFalse, v1alpha1.ReasonAnalysisFailed)
				expectReleaseCondition(release, v1alpha1.ReleaseConditionRollbackRequired, metav1.ConditionTrue, v1alpha1.ReasonAnalysisFailed)
			})

			It("does not create AnalysisRuns for newly matching templates", func() {
				createAnalysisTemplate(ctx, testNamespace, "late-"+randomHex(4), releaseLabels, templateLabels)

				Consistently(func() int {
					return len(analysisRunNames(testNamespace, release))
				}).Should(Equal(1))
			})
		})
	})

	Describe("Template selection", func() {
		// Global ClusterAnalysisTemplates match every release in the cluster, so
		// these specs must not overlap with any other spec creating releases.
		Context("with a global ClusterAnalysisTemplate", Serial, func() {
			var globalTemplateName string

			BeforeEach(func() {
				globalTemplateName = "global-" + randomHex(4)
				createClusterAnalysisTemplate(ctx, globalTemplateName,
					map[string]string{"global": "true", "health": "true"})
			})

			It("creates an AnalysisRun from the global template alongside the namespaced one", func() {
				Eventually(func() []string {
					return analysisRunNames(testNamespace, release)
				}).Should(ConsistOf(
					deploy.GenerateAnalysisRunName(release.Name, templateName),
					deploy.GenerateAnalysisRunName(release.Name, globalTemplateName),
				))
			})

			Context("when the release opts out of global analysis", func() {
				BeforeEach(func() {
					releaseAnnotations = map[string]string{
						v1alpha1.AnnotationKeyReleaseNoGlobalAnalysis: "true",
					}
				})

				It("does not create an AnalysisRun from the global template", func() {
					awaitAnalysisRuns(testNamespace, release, 1)

					Consistently(func() []string {
						return analysisRunNames(testNamespace, release)
					}).Should(ConsistOf(deploy.GenerateAnalysisRunName(release.Name, templateName)))
				})
			})

			Context("when the release has no labels", func() {
				BeforeEach(func() {
					releaseLabels = nil
				})

				// Regression: copying labels from an unlabelled release onto the
				// AnalysisRun used to panic on a nil map.
				It("creates an AnalysisRun carrying the template's health label", func() {
					var globalRun analysisv1alpha1.AnalysisRun
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(ctx, client.ObjectKey{
							Namespace: testNamespace,
							Name:      deploy.GenerateAnalysisRunName(release.Name, globalTemplateName),
						}, &globalRun)).To(Succeed())
					}).Should(Succeed())

					Expect(globalRun.Labels).To(HaveKeyWithValue("health", "true"))
				})
			})
		})

		Context("with a custom template selector annotation", func() {
			var (
				selectorLabels              map[string]string
				selectedTemplateName        string
				selectedClusterTemplateName string
			)

			BeforeEach(func() {
				// Unique per spec, so the cluster scoped template is never selected
				// by releases from other specs.
				selectorLabels = map[string]string{"analysis-suite": randomHex(4)}
				selectedTemplateName = "selected-" + randomHex(4)
				selectedClusterTemplateName = "selected-cluster-" + randomHex(4)

				releaseAnnotations = map[string]string{
					v1alpha1.AnnotationKeyReleaseAnalysisTemplateSelector: "analysis-suite=" + selectorLabels["analysis-suite"],
				}

				createAnalysisTemplate(ctx, testNamespace, selectedTemplateName, selectorLabels,
					map[string]string{"rollback": "true"})
				createClusterAnalysisTemplate(ctx, selectedClusterTemplateName,
					map[string]string{"analysis-suite": selectorLabels["analysis-suite"], "rollback": "true"})
			})

			It("creates AnalysisRuns from matching namespaced and cluster templates", func() {
				Eventually(func() []string {
					return analysisRunNames(testNamespace, release)
				}).Should(ConsistOf(
					deploy.GenerateAnalysisRunName(release.Name, templateName),
					deploy.GenerateAnalysisRunName(release.Name, selectedTemplateName),
					deploy.GenerateAnalysisRunName(release.Name, selectedClusterTemplateName),
				))
			})

			It("ignores templates that do not match the selector", func() {
				createAnalysisTemplate(ctx, testNamespace, "unselected-"+randomHex(4),
					map[string]string{"analysis-suite": "someone-else"}, map[string]string{"rollback": "true"})

				awaitAnalysisRuns(testNamespace, release, 3)
				Consistently(func() int {
					return len(analysisRunNames(testNamespace, release))
				}).Should(Equal(3))
			})

			Context("when the selector is invalid", func() {
				BeforeEach(func() {
					releaseAnnotations[v1alpha1.AnnotationKeyReleaseAnalysisTemplateSelector] = "not a valid selector!"
				})

				It("falls back to matching templates by release labels only", func() {
					awaitAnalysisRuns(testNamespace, release, 1)

					Consistently(func() []string {
						return analysisRunNames(testNamespace, release)
					}).Should(ConsistOf(deploy.GenerateAnalysisRunName(release.Name, templateName)))
				})
			})
		})
	})
})

// The helpers below are shared with the automated rollback specs, which drive
// release conditions through real AnalysisRuns.

// createAnalysisRelease creates a release with a deployment start time,
// optionally activated. Namespaced analysis templates are matched by a
// selector built from the release's labels, so an unlabelled release matches
// every template in its namespace.
func createAnalysisRelease(ctx context.Context, namespace, target string, labels, annotations map[string]string, active bool) *v1alpha1.Release {
	release := generateRelease(namespace, target)
	release.Labels = maps.Clone(labels)
	release.Annotations = map[string]string{
		v1alpha1.AnnotationKeyReleaseDeploymentStartTime: time.Now().UTC().Format(time.RFC3339),
	}
	maps.Copy(release.Annotations, annotations)
	if active {
		release.Annotations[v1alpha1.AnnotationKeyReleaseActivate] = v1alpha1.AnnotationValueReleaseActivateTrue
	}
	Expect(k8sClient.Create(ctx, release)).To(Succeed())

	Eventually(func() bool {
		fetched := &v1alpha1.Release{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(release), fetched); err != nil {
			return false
		}
		return fetched.IsStatusInitialised() && fetched.IsConditionActiveTrue() == active
	}).Should(BeTrue(), "release did not reach its initial state")

	return release
}

// deactivateRelease removes the activate annotation and waits for the Active
// condition to flip to False.
func deactivateRelease(ctx context.Context, release *v1alpha1.Release) {
	Eventually(func() error {
		fetched := &v1alpha1.Release{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(release), fetched); err != nil {
			return err
		}
		delete(fetched.Annotations, v1alpha1.AnnotationKeyReleaseActivate)
		return k8sClient.Update(ctx, fetched)
	}).Should(Succeed())

	Eventually(func() bool {
		fetched := &v1alpha1.Release{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(release), fetched)).To(Succeed())
		return meta.IsStatusConditionFalse(fetched.Status.Conditions, v1alpha1.ReleaseConditionActive)
	}).Should(BeTrue())
}

// createAnalysisTemplate creates an AnalysisTemplate carrying both the given
// selector labels (so it matches a release with those labels) and the extra
// labels controlling which condition it feeds.
func createAnalysisTemplate(ctx context.Context, namespace, name string, selectorLabels, extraLabels map[string]string) *analysisv1alpha1.AnalysisTemplate {
	labels := maps.Clone(selectorLabels)
	if labels == nil {
		labels = map[string]string{}
	}
	maps.Copy(labels, extraLabels)

	template := &analysisv1alpha1.AnalysisTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: testAnalysisTemplateSpec(),
	}
	Expect(k8sClient.Create(ctx, template)).To(Succeed())
	return template
}

// createClusterAnalysisTemplate creates a ClusterAnalysisTemplate and deletes
// it when the spec finishes. Unlike namespaced templates, which are isolated
// by each spec's leaked namespace, cluster templates would otherwise be seen
// by every later spec.
func createClusterAnalysisTemplate(ctx context.Context, name string, labels map[string]string) *analysisv1alpha1.ClusterAnalysisTemplate {
	template := &analysisv1alpha1.ClusterAnalysisTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: testAnalysisTemplateSpec(),
	}
	Expect(k8sClient.Create(ctx, template)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, template))).To(Succeed())
	})
	return template
}

func testAnalysisTemplateSpec() analysisv1alpha1.AnalysisTemplateSpec {
	return analysisv1alpha1.AnalysisTemplateSpec{
		Metrics: []analysisv1alpha1.Metric{
			{
				Name:             "test-metric",
				SuccessCondition: "result[0] >= 0.95",
				Provider: analysisv1alpha1.MetricProvider{
					Prometheus: &analysisv1alpha1.PrometheusMetric{
						Address: "http://prometheus.example.com",
						Query:   "sum(rate(requests_total[5m]))",
					},
				},
			},
		},
	}
}

// setAnalysisRunPhase writes a phase into the AnalysisRun status. The
// AnalysisRun CRD has no status subresource, so this is a plain update.
func setAnalysisRunPhase(ctx context.Context, run *analysisv1alpha1.AnalysisRun, phase analysisv1alpha1.AnalysisPhase) {
	Eventually(func() error {
		fetched := &analysisv1alpha1.AnalysisRun{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(run), fetched); err != nil {
			return err
		}
		fetched.Status.Phase = phase
		return k8sClient.Update(ctx, fetched)
	}).Should(Succeed())
}

// awaitAnalysisRuns waits until exactly count AnalysisRuns owned by the release
// exist, and returns them.
func awaitAnalysisRuns(namespace string, release *v1alpha1.Release, count int) []analysisv1alpha1.AnalysisRun {
	var runs []analysisv1alpha1.AnalysisRun
	Eventually(func() int {
		runs = ownedAnalysisRuns(namespace, release)
		return len(runs)
	}).Should(Equal(count), fmt.Sprintf("expected %d AnalysisRun(s) owned by %s", count, release.Name))
	return runs
}

// ownedAnalysisRuns lists the AnalysisRuns owned by the release. The test
// client is uncached, so it cannot use the controller's owner field index and
// filters client side instead.
func ownedAnalysisRuns(namespace string, release *v1alpha1.Release) []analysisv1alpha1.AnalysisRun {
	list := &analysisv1alpha1.AnalysisRunList{}
	Expect(k8sClient.List(ctx, list, client.InNamespace(namespace))).To(Succeed())

	var owned []analysisv1alpha1.AnalysisRun
	for _, run := range list.Items {
		if owner := metav1.GetControllerOf(&run); owner != nil && owner.Name == release.Name {
			owned = append(owned, run)
		}
	}
	return owned
}

func analysisRunNames(namespace string, release *v1alpha1.Release) []string {
	names := []string{}
	for _, run := range ownedAnalysisRuns(namespace, release) {
		names = append(names, run.Name)
	}
	return names
}

func expectReleaseCondition(release *v1alpha1.Release, conditionType string, status metav1.ConditionStatus, reason string) {
	GinkgoHelper()

	Eventually(func() metav1.Condition {
		fetched := &v1alpha1.Release{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(release), fetched)).To(Succeed())
		condition := meta.FindStatusCondition(fetched.Status.Conditions, conditionType)
		if condition == nil {
			return metav1.Condition{Type: conditionType}
		}
		return metav1.Condition{Type: conditionType, Status: condition.Status, Reason: condition.Reason}
	}).Should(Equal(metav1.Condition{Type: conditionType, Status: status, Reason: reason}))
}
