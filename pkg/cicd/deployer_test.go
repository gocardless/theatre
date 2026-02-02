package cicd

import (
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Deployer", func() {
	It("should skip non-jsonpath values", func() {
		options := map[string]string{"skip_queue": "true"}
		release := deployv1alpha1.Release{}

		ParseDeploymentOptions(options, &release)
		Expect(options).To(Equal(map[string]string{
			"skip_queue": "true",
		}))
	})

	It("should parse jsonpath values", func() {
		options := map[string]string{
			"revision": "{.config.revisions[?(@.name==\"infrastructure\")].id}",
			"name":     " {.metadata.name}",
		}
		release := deployv1alpha1.Release{
			ObjectMeta: v1.ObjectMeta{
				Name: "test-release",
			},
			ReleaseConfig: deployv1alpha1.ReleaseConfig{
				Revisions: []deployv1alpha1.Revision{
					{
						Name: "infrastructure",
						ID:   "abc123",
					},
				},
			},
		}

		ParseDeploymentOptions(options, &release)
		Expect(options).To(Equal(map[string]string{
			"revision": "abc123",
			"name":     "test-release",
		}))
	})

	It("should parse any empty values in the object as empty", func() {
		options := map[string]string{
			"name": " {.metadata.name}",
		}
		release := deployv1alpha1.Release{}

		ParseDeploymentOptions(options, &release)
		Expect(options).To(Equal(map[string]string{
			"name": "",
		}))
	})

	It("should ignore invalid deployment options", func() {
		options := map[string]string{
			"revision": "{.config.revisions[?(@.name==\"infrastructure\")].",
		}
		release := deployv1alpha1.Release{}

		ParseDeploymentOptions(options, &release)
		Expect(options).To(Equal(map[string]string{
			"revision": "{.config.revisions[?(@.name==\"infrastructure\")].",
		}))
	})
})
