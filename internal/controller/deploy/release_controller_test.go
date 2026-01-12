package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
)

const (
	DefaultNamespace = "releases"
)

var _ = Describe("ReleaseController", func() {

	var (
		obj v1alpha1.Release
	)

	BeforeEach(func() {
		obj = v1alpha1.Release{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-release",
				Namespace: DefaultNamespace,
			},
			ReleaseConfig: v1alpha1.ReleaseConfig{
				TargetName: "test-target",
				Revisions: []v1alpha1.Revision{
					{Name: "application-revision", ID: "test-app-revision"},
					{Name: "infrastructure-revision", ID: "test-infra-revision"},
				},
			},
		}
	})

	JustBeforeEach(func() {
		err := k8sClient.Create(ctx, &obj)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		err := k8sClient.DeleteAllOf(ctx, &v1alpha1.Release{}, client.InNamespace("releases"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("Should pass", func() {
		Expect(true).To(BeTrue())
	})

	DescribeTable("analysisRunName",
		func(releaseName, templateName, expected string) {
			Expect(analysisRunName(releaseName, templateName)).To(Equal(expected))
		},
		Entry("short names", "release", "template", "release-template"),
		Entry("short names 2", "foo", "bar", "foo-bar"),
		Entry("long but acceptable release name", "release-name-is-very-long-but-still-fits-in-the-maxx", "template12", "release-name-is-very-long-but-still-fits-in-the-maxx-template12"),
		Entry("long but acceptable template name", "releasefoo", "template-name-is-very-long-but-still-fits-in-the-max", "releasefoo-template-name-is-very-long-but-still-fits-in-the-max"),
		Entry("release name too long", "release-name-is-very-long-and-does-not-fit-in-the-max", "template12", "release-name-is-very-long-and-do-template12-1a2c4b1"),
		Entry("template name too long", "releasefoo", "template-name-is-very-long-and-does-not-fit-in-the-max", "releasefoo-template-name-is-very-long-and-d-60ca895"),
		Entry("both names too long", "release-name-is-very-long-too-longx", "template-name-is-very-long-too-long", "release-name-is-very-long-too-lo-template-name-is-very-long-too-l-4ea7350"),
	)

	Describe("generateSelectors", func() {
		globalSelector := labels.SelectorFromValidatedSet(labels.Set{"global": "true"})
		It("returns global selectors", func() {
			releaseLabelsSelector := labels.SelectorFromSet(obj.GetLabels())
			namespacedSelectors, clusterSelectors := generateSelectors(&obj)
			Expect(clusterSelectors).To(ContainElement(globalSelector))
			Expect(namespacedSelectors).ToNot(ContainElement(globalSelector))
			Expect(namespacedSelectors).To(ContainElement(releaseLabelsSelector))
		})

		When("global templates are disabled", func() {
			BeforeEach(func() {
				obj.SetAnnotations(map[string]string{"placeholder-no-global": "true"})
			})
			It("does not return global selectors", func() {

				namespacedSelectors, clusterSelectors := generateSelectors(&obj)
				Expect(clusterSelectors).ToNot(ContainElement(globalSelector))
				Expect(namespacedSelectors).ToNot(ContainElement(globalSelector))
			})
		})

		When("valid custom selector is defined in annotation", func() {
			selectorStr := "testlabel in (foo, bar), testequiv == baz"
			selectorParsed, err := labels.Parse(selectorStr)
			Expect(err).NotTo(HaveOccurred())

			BeforeEach(func() {
				obj.SetAnnotations(map[string]string{"placeholder-selector": selectorStr})
			})

			It("returns custom selectors", func() {
				namespacedSelectors, clusterSelectors := generateSelectors(&obj)
				Expect(clusterSelectors).To(ContainElement(selectorParsed))
				Expect(namespacedSelectors).To(ContainElement(selectorParsed))
				// global and custom
				Expect(clusterSelectors).To(HaveLen(2))
				// release labels and custom
				Expect(namespacedSelectors).To(HaveLen(2))
			})
		})

		When("invalid custom selector is defined in annotation", func() {
			selectorStr := "in in in in in in"

			BeforeEach(func() {
				obj.SetAnnotations(map[string]string{"placeholder-selector": selectorStr})
			})

			It("does not returns custom selectors", func() {
				namespacedSelectors, clusterSelectors := generateSelectors(&obj)
				// global only
				Expect(clusterSelectors).To(HaveLen(1))
				// release labels only
				Expect(namespacedSelectors).To(HaveLen(1))
			})
		})
	})

	Describe("concatTemplateLists", func() {
		ClusterAnalysisTemplateItem := analysisv1alpha1.ClusterAnalysisTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example",
			},
		}
		AnalysisTemplateItem := analysisv1alpha1.AnalysisTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example",
			},
		}
		templateList := analysisv1alpha1.AnalysisTemplateList{
			Items: []analysisv1alpha1.AnalysisTemplate{AnalysisTemplateItem},
		}
		clusterTemplateList := analysisv1alpha1.ClusterAnalysisTemplateList{
			Items: []analysisv1alpha1.ClusterAnalysisTemplate{ClusterAnalysisTemplateItem},
		}
		releaseList := v1alpha1.ReleaseList{}
		completeList := []runtime.Object{&AnalysisTemplateItem, &ClusterAnalysisTemplateItem}

		It("concatenates two valid lists", func() {
			lists, err := concatTemplateLists([]runtime.Object{&templateList, &clusterTemplateList})
			Expect(err).ToNot(HaveOccurred())
			Expect(lists).To(Equal(completeList))
		})

		It("errors when unsupported object is passed", func() {
			_, err := concatTemplateLists([]runtime.Object{&templateList, &clusterTemplateList, &releaseList})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("analysisCreate", func() {
		var (
			testTemplate        analysisv1alpha1.AnalysisTemplate
			testClusterTemplate analysisv1alpha1.ClusterAnalysisTemplate
			testOther           v1alpha1.Release
		)

		BeforeEach(func() {
			testTemplate = analysisv1alpha1.AnalysisTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "example-namespaced",
				},
			}
			testClusterTemplate = analysisv1alpha1.ClusterAnalysisTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "example-clustered",
				},
			}
			testOther = v1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name: "example-other",
				},
			}
		})

		It("creates AnalysisRun for ClusterAnalysisTemplate", func() {
			run, err := reconciler.analysisCreate(&obj, &testClusterTemplate)
			Expect(err).ToNot(HaveOccurred())
			Expect(run).ToNot(BeNil())
		})

		It("creates AnalysisRun for AnalysisTemplate", func() {
			run, err := reconciler.analysisCreate(&obj, &testTemplate)
			Expect(err).ToNot(HaveOccurred())
			Expect(run).ToNot(BeNil())
		})

		It("errors when unsupported object is passed", func() {
			run, err := reconciler.analysisCreate(&obj, &testOther)
			Expect(err).To(HaveOccurred())
			Expect(run).To(BeNil())
		})
	})

	Describe("existing analysis parsing", func() {
		lookup := []analysisv1alpha1.AnalysisPhase{
			analysisv1alpha1.AnalysisPhasePending,
			analysisv1alpha1.AnalysisPhaseRunning,
			analysisv1alpha1.AnalysisPhaseSuccessful,
			analysisv1alpha1.AnalysisPhaseFailed,
			analysisv1alpha1.AnalysisPhaseError,
			analysisv1alpha1.AnalysisPhaseInconclusive,
		}

		healthConditionGen := conditionGen{
			conditionType:       v1alpha1.ReleaseConditionHealthy,
			conditionStatusGood: metav1.ConditionTrue,
			conditionStatusBad:  metav1.ConditionFalse,
		}

		rollbackConditionGen := conditionGen{
			conditionType:       v1alpha1.ReleaseConditionRollbackRequired,
			conditionStatusGood: metav1.ConditionFalse,
			conditionStatusBad:  metav1.ConditionTrue,
		}

		// ensures output from parseAnalysisResults produces correct phase counts as
		// expected
		var countParsedMap = func(runMap map[analysisv1alpha1.AnalysisPhase][]string, counts []int) bool {
			Expect(len(counts)).To(Equal(6))
			for i := range 6 {
				if len(runMap[lookup[i]]) != counts[i] {
					return false
				}
			}
			return true
		}

		// creates AnalysisRun object with given phase
		var runWithPhase = func(name string, phase analysisv1alpha1.AnalysisPhase) analysisv1alpha1.AnalysisRun {
			return analysisv1alpha1.AnalysisRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Status: analysisv1alpha1.AnalysisRunStatus{
					Phase: phase,
				},
			}
		}

		// creates AnalysisRunList with number of phases as per the counts argument
		var genRunList = func(counts []int) analysisv1alpha1.AnalysisRunList {
			Expect(len(counts)).To(Equal(6))

			ret := analysisv1alpha1.AnalysisRunList{}

			for iPhase, v := range counts {
				phase := lookup[iPhase]
				for i := 0; i < v; i++ {
					name := string(phase) + "-" + strconv.Itoa(i)
					ret.Items = append(ret.Items, runWithPhase(name, phase))
				}
			}

			return ret
		}

		DescribeTable("returns correct condition", func(runCounts []int, expectedHealthStatus metav1.ConditionStatus, expectedRollbackStatus metav1.ConditionStatus, expectedReason string) {
			runList := genRunList(runCounts)
			parsedResults := parseAnalysisResults(runList.Items)
			Expect(countParsedMap(parsedResults, runCounts))
			healthCondition := healthConditionGen.conditionFromResults(parsedResults)
			rollbackCondition := rollbackConditionGen.conditionFromResults(parsedResults)
			Expect(healthCondition.Status).To(Equal(expectedHealthStatus))
			Expect(rollbackCondition.Status).To(Equal(expectedRollbackStatus))
			Expect(healthCondition.Reason).To(Equal(expectedReason))
			Expect(rollbackCondition.Reason).To(Equal(expectedReason))
		},
			Entry("no runs", []int{0, 0, 0, 0, 0, 0}, metav1.ConditionTrue, metav1.ConditionFalse, v1alpha1.ReasonAnalysisSucceeded),
			Entry("only pending runs", []int{3, 0, 0, 0, 0, 0}, metav1.ConditionUnknown, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisInProgress),
			Entry("only running runs", []int{0, 3, 0, 0, 0, 0}, metav1.ConditionUnknown, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisInProgress),
			Entry("only successful runs", []int{0, 0, 3, 0, 0, 0}, metav1.ConditionTrue, metav1.ConditionFalse, v1alpha1.ReasonAnalysisSucceeded),
			Entry("only failed runs", []int{0, 0, 0, 3, 0, 0}, metav1.ConditionFalse, metav1.ConditionTrue, v1alpha1.ReasonAnalysisFailed),
			Entry("only errored runs", []int{0, 0, 0, 0, 3, 0}, metav1.ConditionUnknown, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisError),
			Entry("only inconclusive runs", []int{0, 0, 0, 0, 0, 3}, metav1.ConditionUnknown, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisError),
			Entry("successful, pending and running", []int{3, 3, 3, 0, 0, 0}, metav1.ConditionUnknown, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisInProgress),
			Entry("successful, pending, running and inconclusive", []int{3, 3, 3, 0, 0, 3}, metav1.ConditionUnknown, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisError),
			Entry("successful, pending, running and error", []int{3, 3, 3, 0, 3, 0}, metav1.ConditionUnknown, metav1.ConditionUnknown, v1alpha1.ReasonAnalysisError),
			Entry("all phases", []int{3, 3, 3, 3, 3, 3}, metav1.ConditionFalse, metav1.ConditionTrue, v1alpha1.ReasonAnalysisFailed),
		)
	})

	Describe("statusKnown", func() {

		var (
			conditionTypesToCheck []string
		)

		BeforeEach(func() {
			conditionTypesToCheck = []string{}
		})

		conditionHealthyFalse := metav1.Condition{
			Type:   v1alpha1.ReleaseConditionHealthy,
			Status: metav1.ConditionFalse,
		}
		conditionHealthyTrue := metav1.Condition{
			Type:   v1alpha1.ReleaseConditionHealthy,
			Status: metav1.ConditionTrue,
		}
		conditionHealthyUnknown := metav1.Condition{
			Type:   v1alpha1.ReleaseConditionHealthy,
			Status: metav1.ConditionUnknown,
		}
		conditionActiveUnknown := metav1.Condition{
			Type:   v1alpha1.ReleaseConditionActive,
			Status: metav1.ConditionUnknown,
		}
		conditionActiveTrue := metav1.Condition{
			Type:   v1alpha1.ReleaseConditionActive,
			Status: metav1.ConditionTrue,
		}

		// function to be used in table tests
		tableConditionFunc := func(conditionsToSet []metav1.Condition, expected bool) {
			for _, v := range conditionsToSet {
				meta.SetStatusCondition(&obj.Status.Conditions, v)
			}
			Expect(statusKnown(&obj, conditionTypesToCheck)).To(Equal(expected))
		}

		DescribeTable("without checked conditions always returns true",
			tableConditionFunc,
			Entry("with no conditions", []metav1.Condition{}, true),
			Entry("with healthy=true condition", []metav1.Condition{conditionHealthyTrue}, true),
			Entry("with healthy=true and active=unknown condition", []metav1.Condition{conditionHealthyTrue, conditionActiveUnknown}, true),
		)

		Describe("with healthy condition checked", func() {
			BeforeEach(func() {
				conditionTypesToCheck = append(conditionTypesToCheck, v1alpha1.ReleaseConditionHealthy)
			})

			DescribeTable("returns based on healthy status",
				tableConditionFunc,
				Entry("with no conditions", []metav1.Condition{}, false),
				Entry("with healthy=true condition", []metav1.Condition{conditionHealthyTrue}, true),
				Entry("with healthy=false condition", []metav1.Condition{conditionHealthyFalse}, true),
				Entry("with healthy=false and active=unknown condition", []metav1.Condition{conditionHealthyFalse, conditionActiveUnknown}, true),
				Entry("with healthy=unknown condition", []metav1.Condition{conditionHealthyUnknown}, false),
				Entry("with healthy=unknown and active=true condition", []metav1.Condition{conditionHealthyUnknown, conditionActiveTrue}, false),
			)

			Describe("with 'active' condition checked", func() {
				BeforeEach(func() {
					conditionTypesToCheck = append(conditionTypesToCheck, v1alpha1.ReleaseConditionActive)
				})

				DescribeTable("returns based on healthy and active status",
					tableConditionFunc,
					Entry("with no conditions", []metav1.Condition{}, false),
					Entry("with healthy=true condition", []metav1.Condition{conditionHealthyTrue}, false),
					Entry("with healthy=false condition", []metav1.Condition{conditionHealthyFalse}, false),
					Entry("with healthy=false and active=unknown condition", []metav1.Condition{conditionHealthyFalse, conditionActiveUnknown}, false),
					Entry("with healthy=unknown condition", []metav1.Condition{conditionHealthyUnknown}, false),
					Entry("with healthy=unknown and active=true condition", []metav1.Condition{conditionHealthyUnknown, conditionActiveTrue}, false),
					Entry("with healthy=false and active=true condition", []metav1.Condition{conditionHealthyFalse, conditionActiveTrue}, true),
					Entry("with healthy=true and active=true condition", []metav1.Condition{conditionHealthyTrue, conditionActiveTrue}, true),
				)
			})
		})
	})
})

func generateRelease(target string) *v1alpha1.Release {
	appSHA := generateCommitSHA()
	infraSHA := generateCommitSHA()
	return &v1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name:      target + "-" + infraSHA[:7] + "-" + appSHA[:7],
			Namespace: DefaultNamespace,
		},
		ReleaseConfig: v1alpha1.ReleaseConfig{
			TargetName: target,
			Revisions: []v1alpha1.Revision{
				{
					Name: "application-revision",
					ID:   appSHA,
				},
				{
					Name: "infrastructure-revision",
					ID:   infraSHA,
				},
			},
		},
	}
}

func generateCommitSHA() string {
	bytes := make([]byte, 20)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}

func createRelease(ctx context.Context, target string) *v1alpha1.Release {
	release := generateRelease(target)
	err := k8sClient.Create(ctx, release)
	Expect(err).NotTo(HaveOccurred())
	return release
}

func getMetaV1Timestamp(ts string) metav1.Time {
	timestamp, err := time.Parse(time.RFC3339, ts)
	Expect(err).NotTo(HaveOccurred())
	return metav1.NewTime(timestamp)
}
