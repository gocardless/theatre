package deploy

import (
	"fmt"
	"strconv"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
)

const (
	DefaultNamespace = "default"
)

var _ = Describe("ReleaseAnalysis", func() {
	var (
		obj v1alpha1.Release
	)

	BeforeEach(func() {
		obj = v1alpha1.Release{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-release",
				Namespace:   DefaultNamespace,
				Annotations: map[string]string{},
				Labels: map[string]string{
					"service": "test-service",
				},
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

	Describe("generateSelectors", func() {
		var (
			expectedSelectors map[string]labels.Selector
		)

		selectorKeyGlobal := "global"
		selectorKeyRelease := "releaseLabels"
		selectorKeyCustom := "custom"

		BeforeEach(func() {
			expectedSelectors = map[string]labels.Selector{
				selectorKeyRelease: labels.SelectorFromSet(obj.GetLabels()),
				selectorKeyGlobal:  labels.SelectorFromValidatedSet(labels.Set{"global": "true"}),
			}
		})

		AssertGlobalSelectorPresent := func() {
			It("generates a global selector for cluster resource, but not namespaced resource", func() {
				namespacedSelectors, clusterSelectors := generateSelectors(&obj, logr.Discard())
				Expect(clusterSelectors).To(ContainElement(expectedSelectors[selectorKeyGlobal]))
				Expect(namespacedSelectors).ToNot(ContainElement(expectedSelectors[selectorKeyGlobal]))
			})
		}

		AssertGlobalSelectorAbsent := func() {
			It("does not generate a global selector for any resource", func() {
				namespacedSelectors, clusterSelectors := generateSelectors(&obj, logr.Discard())
				Expect(clusterSelectors).ToNot(ContainElement(expectedSelectors[selectorKeyGlobal]))
				Expect(namespacedSelectors).ToNot(ContainElement(expectedSelectors[selectorKeyGlobal]))
			})
		}

		AssertSelector := func(key string) {
			It(fmt.Sprintf("generates %s selector for namespaced and cluster resources", key), func() {
				namespacedSelectors, clusterSelectors := generateSelectors(&obj, logr.Discard())
				Expect(clusterSelectors).To(ContainElement(expectedSelectors[key]))
				Expect(namespacedSelectors).To(ContainElement(expectedSelectors[key]))
			})
		}

		AssertSelectorNamespacedOnly := func(key string) {
			It(fmt.Sprintf("generates %s selector for namespaced resource only", key), func() {
				namespacedSelectors, clusterSelectors := generateSelectors(&obj, logr.Discard())
				Expect(clusterSelectors).ToNot(ContainElement(expectedSelectors[key]))
				Expect(namespacedSelectors).To(ContainElement(expectedSelectors[key]))
			})
		}

		// AssertSelectorClusterOnly := func(selector labels.Selector) {
		// 	It("generates a custom selector for namespaced and cluster resource", func() {
		// 		namespacedSelectors, clusterSelectors := generateSelectors(&obj)
		// 		Expect(clusterSelectors).To(ContainElement(selector))
		// 		Expect(namespacedSelectors).ToNot(ContainElement(selector))
		// 	})
		// }

		AssertGlobalSelectorPresent()
		AssertSelectorNamespacedOnly(selectorKeyRelease)

		When("global templates are disabled", func() {
			BeforeEach(func() {
				obj.Annotations[v1alpha1.ReleaseLabelNoGlobalAnalysis] = "true"
			})

			AssertGlobalSelectorAbsent()
			AssertSelectorNamespacedOnly(selectorKeyRelease)
		})

		When("valid custom selectors are set", func() {
			selectorStr := "testlabel in (foo, bar), testequiv == baz"
			var (
				selectorParsed labels.Selector
				err            error
			)

			BeforeEach(func() {
				obj.Annotations[v1alpha1.AnnotationKeyReleaseAnalysisTemplateSelector] = selectorStr
				selectorParsed, err = labels.Parse(selectorStr)
				Expect(err).NotTo(HaveOccurred())
				expectedSelectors[selectorKeyCustom] = selectorParsed
			})

			AssertGlobalSelectorPresent()
			AssertSelector(selectorKeyCustom)
			AssertSelectorNamespacedOnly(selectorKeyRelease)

			When("global templates are disabled", func() {
				BeforeEach(func() {
					obj.Annotations[v1alpha1.ReleaseLabelNoGlobalAnalysis] = "true"
				})

				AssertGlobalSelectorAbsent()
				AssertSelector(selectorKeyCustom)
				AssertSelectorNamespacedOnly(selectorKeyRelease)
			})
		})

		When("invalid custom selector is defined in annotation", func() {
			selectorStr := "in in in in in in"

			BeforeEach(func() {
				obj.Annotations[v1alpha1.AnnotationKeyReleaseAnalysisTemplateSelector] = selectorStr
			})

			AssertGlobalSelectorPresent()
			AssertSelectorNamespacedOnly(selectorKeyRelease)

			It("does not returns custom selectors", func() {
				namespacedSelectors, clusterSelectors := generateSelectors(&obj, logr.Discard())
				// global only
				Expect(clusterSelectors).To(HaveLen(1))
				// release labels only
				Expect(namespacedSelectors).To(HaveLen(1))
			})
		})
	})

	Describe("splitHealthRollback", func() {
		var (
			analysisList analysisv1alpha1.AnalysisRunList
		)

		BeforeEach(func() {
			analysisList = analysisv1alpha1.AnalysisRunList{
				Items: []analysisv1alpha1.AnalysisRun{},
			}
		})

		AssertHealthEmpty := func() {
			It("returns empty health list", func() {
				health, _ := splitHealthRollback(analysisList)
				Expect(health).To(BeEmpty())
			})
		}

		AssertRollbackEmpty := func() {
			It("returns empty rollback list", func() {
				_, rollback := splitHealthRollback(analysisList)
				Expect(rollback).To(BeEmpty())
			})
		}

		AssertRollbackEmpty()
		AssertHealthEmpty()

		When("health analysis is in the list", func() {
			healthAnalysis := genAnalysisRun("health-1", analysisv1alpha1.AnalysisPhaseSuccessful, true, false)

			AssertHealthReturned := func() {
				It("contains health analysis only in health list", func() {
					health, rollback := splitHealthRollback(analysisList)
					Expect(health).To(ContainElement(healthAnalysis))
					Expect(rollback).ToNot(ContainElement(healthAnalysis))
				})
			}

			BeforeEach(func() {
				analysisList.Items = append(analysisList.Items, healthAnalysis)
			})

			AssertHealthReturned()
			AssertRollbackEmpty()

			When("rollback analysis is in the list", func() {
				rollbackAnalysis := genAnalysisRun("rollback-1", analysisv1alpha1.AnalysisPhaseSuccessful, false, true)

				AssertRollbackReturned := func() {
					It("contains rollback analysis only in rollback list", func() {
						health, rollback := splitHealthRollback(analysisList)
						Expect(health).ToNot(ContainElement(rollbackAnalysis))
						Expect(rollback).To(ContainElement(rollbackAnalysis))
					})
				}

				BeforeEach(func() {
					analysisList.Items = append(analysisList.Items, rollbackAnalysis)
				})

				AssertRollbackReturned()
				AssertHealthReturned()

				When("shared (health/rollback) analysis is in the list", func() {
					sharedAnalysis := genAnalysisRun("shared-1", analysisv1alpha1.AnalysisPhaseSuccessful, true, true)

					BeforeEach(func() {
						analysisList.Items = append(analysisList.Items, sharedAnalysis)
					})

					AssertRollbackReturned()
					AssertHealthReturned()

					It("contains shared analysis in both lists", func() {
						health, rollback := splitHealthRollback(analysisList)
						Expect(health).To(ContainElement(sharedAnalysis))
						Expect(rollback).To(ContainElement(sharedAnalysis))
					})
				})
			})
		})
	})

	Describe("parseAnalysisResult", func() {
		list := []analysisv1alpha1.AnalysisRun{}
		phases := []analysisv1alpha1.AnalysisPhase{
			analysisv1alpha1.AnalysisPhasePending,
			analysisv1alpha1.AnalysisPhaseRunning,
			analysisv1alpha1.AnalysisPhaseSuccessful,
			analysisv1alpha1.AnalysisPhaseFailed,
			analysisv1alpha1.AnalysisPhaseError,
			analysisv1alpha1.AnalysisPhaseInconclusive,
		}

		BeforeEach(func() {
			for i := range 10 {
				for _, p := range phases {
					list = append(list, genAnalysisRun(fmt.Sprintf("%s-%d", p, i), p, false, false))
				}
			}
		})

		It("returns appropriate count for each phase", func() {
			result := parseAnalysisResults(list)
			for _, v := range phases {
				Expect(result[v]).To(HaveLen(10))
			}
		})
	})

	Describe("concatTemplateLists", func() {
		templateList := analysisv1alpha1.AnalysisTemplateList{}
		clusterTemplateList := analysisv1alpha1.ClusterAnalysisTemplateList{}
		templateListSecond := analysisv1alpha1.AnalysisTemplateList{}
		for i := range 10 {
			templateList.Items = append(templateList.Items, genAnalysisTemplate(fmt.Sprintf("template-%d", i)))
			templateListSecond.Items = append(templateListSecond.Items, genAnalysisTemplate(fmt.Sprintf("second-template-%d", i)))
			clusterTemplateList.Items = append(clusterTemplateList.Items, genClusterAnalysisTemplate(fmt.Sprintf("cluster-template-%d", i)))
		}

		var listOfLists []runtime.Object

		BeforeEach(func() {
			listOfLists = []runtime.Object{&templateList, &clusterTemplateList, &templateListSecond}
		})

		convertTemplateList := func(t []analysisv1alpha1.AnalysisTemplate) []runtime.Object {
			ret := make([]runtime.Object, len(t))
			for i, v := range t {
				ret[i] = &v
			}
			return ret
		}

		convertClusterTemplateList := func(t []analysisv1alpha1.ClusterAnalysisTemplate) []runtime.Object {
			ret := make([]runtime.Object, len(t))
			for i, v := range t {
				ret[i] = &v
			}
			return ret
		}

		It("returns all elements from primary template list", func() {
			ret, err := concatTemplateLists(listOfLists)
			Expect(err).ToNot(HaveOccurred())
			Expect(ret).To(ContainElements(convertTemplateList(templateList.Items)))
		})

		It("returns all elements from secondary template list", func() {
			ret, err := concatTemplateLists(listOfLists)
			Expect(err).ToNot(HaveOccurred())
			Expect(ret).To(ContainElements(convertTemplateList(templateListSecond.Items)))
		})

		It("returns all elements from cluster template list", func() {
			ret, err := concatTemplateLists(listOfLists)
			Expect(err).ToNot(HaveOccurred())
			Expect(ret).To(ContainElements(convertClusterTemplateList(clusterTemplateList.Items)))
		})

		When("list contains invalid object", func() {
			BeforeEach(func() {
				listOfLists = append(listOfLists, &obj)
			})
			It("returns error", func() {
				_, err := concatTemplateLists(listOfLists)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("createAnalysisRun", func() {
		var (
			analysisTemplate        analysisv1alpha1.AnalysisTemplate
			clusterAnalysisTemplate analysisv1alpha1.ClusterAnalysisTemplate
			template                runtime.Object

			selectCluster bool // if true, use clusterAnalysisTemplate, else use analysisTemplate
		)

		BeforeEach(func() {
			analysisTemplate = genAnalysisTemplate("namespaced")
			clusterAnalysisTemplate = genClusterAnalysisTemplate("clustered")
			selectCluster = false
		})

		JustBeforeEach(func() {
			if selectCluster {
				template = &clusterAnalysisTemplate
			} else {
				template = &analysisTemplate
			}
		})

		AssertAnalysisRunReturned := func() {
			It("returns an analysis run", func() {
				analysisRun, err := createAnalysisRun(&obj, template)
				Expect(err).ToNot(HaveOccurred())
				Expect(analysisRun).ToNot(BeNil())
			})
		}

		AssertAnalysisReleaseLabelsEqual := func() {
			It("returns an analysis run with release labels", func() {
				analysisRun, _ := createAnalysisRun(&obj, template)
				Expect(analysisRun.GetLabels()).To(Equal(obj.GetLabels()))
			})
		}

		AssertAnalysisRunError := func() {
			It("returns an error", func() {
				_, err := createAnalysisRun(&obj, template)
				Expect(err).To(HaveOccurred())
			})
		}

		AssertAnalysisRunReturned()
		AssertAnalysisReleaseLabelsEqual()

		When("clusterAnalysisTemplate is provided", func() {
			BeforeEach(func() {
				selectCluster = true
			})
			AssertAnalysisRunReturned()
			AssertAnalysisReleaseLabelsEqual()
		})

		When("pre-deploy timestamp arg is requested", func() {
			BeforeEach(func() {
				analysisTemplate.Spec.Args = append(analysisTemplate.Spec.Args, analysisv1alpha1.Argument{
					Name: AnalysisArgNameBeforeDeploymentTimestamp,
				})
			})

			AssertTimestampArg := func() {
				It("sets the pre-deploy timestamp", func() {
					analysisRun, _ := createAnalysisRun(&obj, template)
					Expect(analysisRun.Spec.Args).To(ContainElement(And(
						HaveField("Name", Equal(AnalysisArgNameBeforeDeploymentTimestamp)),
						HaveField("Value", HaveValue(Not(BeEmpty()))),
					)))
				})
			}

			AssertAnalysisReleaseLabelsEqual()
			AssertAnalysisRunReturned()
			AssertTimestampArg()

			When("present release label arg is requested", func() {
				BeforeEach(func() {
					analysisTemplate.Spec.Args = append(analysisTemplate.Spec.Args, analysisv1alpha1.Argument{
						Name: AnalysisArgLabelPrefix + "service",
					})
				})

				AssertAnalysisReleaseLabelsEqual()
				AssertAnalysisRunReturned()
				AssertTimestampArg()

				It("sets the argument to the corret value", func() {
					analysisRun, _ := createAnalysisRun(&obj, template)
					Expect(analysisRun.Spec.Args).To(ContainElement(And(
						HaveField("Name", Equal(AnalysisArgLabelPrefix+"service")),
						HaveField("Value", HaveValue(Equal(obj.GetLabels()["service"]))),
					)))
				})
			})

			When("missing release label arg is requested", func() {
				BeforeEach(func() {
					analysisTemplate.Spec.Args = append(analysisTemplate.Spec.Args, analysisv1alpha1.Argument{
						Name: AnalysisArgLabelPrefix + "missing",
					})
				})
				AssertAnalysisRunError()
			})

			When("unknown arg is requested", func() {
				BeforeEach(func() {
					analysisTemplate.Spec.Args = append(analysisTemplate.Spec.Args, analysisv1alpha1.Argument{
						Name: "unknown",
					})
				})
				AssertAnalysisRunError()
			})

			When("unknown arg is requested with default value", func() {
				argValue := "default-value"
				arg := analysisv1alpha1.Argument{
					Name:  "unknown",
					Value: &argValue,
				}
				BeforeEach(func() {
					analysisTemplate.Spec.Args = append(analysisTemplate.Spec.Args, arg)
				})
				AssertAnalysisRunReturned()
				AssertAnalysisReleaseLabelsEqual()
				AssertTimestampArg()

				It("returns the argument with the pre-set value unchanged", func() {
					analysisRun, _ := createAnalysisRun(&obj, template)
					Expect(analysisRun.Spec.Args).To(ContainElement(arg))
				})
			})

			When("unknown arg is requested with valueFrom", func() {
				arg := analysisv1alpha1.Argument{
					Name: "unknown",
					ValueFrom: &analysisv1alpha1.ValueFrom{
						SecretKeyRef: &analysisv1alpha1.SecretKeyRef{
							Name: "foo",
							Key:  "bar",
						},
						FieldRef: &analysisv1alpha1.FieldRef{
							FieldPath: "baz",
						},
					},
				}

				BeforeEach(func() {
					analysisTemplate.Spec.Args = append(analysisTemplate.Spec.Args, arg)
				})

				AssertAnalysisRunReturned()
				AssertAnalysisReleaseLabelsEqual()
				AssertTimestampArg()

				It("returns the argument with valueFrom unchanged", func() {
					analysisRun, _ := createAnalysisRun(&obj, template)
					Expect(analysisRun.Spec.Args).To(ContainElement(arg))
				})
			})
		})
	})
})

func genAnalysisRun(name string, phase analysisv1alpha1.AnalysisPhase, health bool, rollback bool) analysisv1alpha1.AnalysisRun {
	return analysisv1alpha1.AnalysisRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"health":   strconv.FormatBool(health),
				"rollback": strconv.FormatBool(rollback),
			},
		},
		Status: analysisv1alpha1.AnalysisRunStatus{
			Phase: phase,
		},
	}
}

func genAnalysisTemplate(name string) analysisv1alpha1.AnalysisTemplate {
	return analysisv1alpha1.AnalysisTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func genClusterAnalysisTemplate(name string) analysisv1alpha1.ClusterAnalysisTemplate {
	return analysisv1alpha1.ClusterAnalysisTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}
