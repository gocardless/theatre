package cicd

import (
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Deployer", func() {
	It("should skip non-jsonpath values", func() {
		options := map[string]apiextv1.JSON{
			"skip_queue": {Raw: []byte("true")},
		}
		release := deployv1alpha1.Release{}

		parsed, err := ParseDeploymentOptions(options, &release)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(map[string]interface{}{
			"skip_queue": true,
		}))
	})

	It("should parse jsonpath values", func() {
		options := map[string]apiextv1.JSON{
			"revision": {Raw: []byte("{.config.revisions[?(@.name==\"infrastructure\")].id}")},
			"name":     {Raw: []byte("{.metadata.name}")},
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

		parsed, err := ParseDeploymentOptions(options, &release)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(map[string]interface{}{
			"revision": "abc123",
			"name":     "test-release",
		}))
	})

	It("should parse any empty values in the object as empty", func() {
		options := map[string]apiextv1.JSON{
			"name": {Raw: []byte("{.metadata.name}")},
		}
		release := deployv1alpha1.Release{}

		parsed, err := ParseDeploymentOptions(options, &release)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(map[string]interface{}{
			"name": "",
		}))
	})

	It("should ignore invalid deployment options", func() {
		options := map[string]apiextv1.JSON{
			"revision": {Raw: []byte("{.config.revisions[?(@.name==\"infrastructure\")].")},
		}
		release := deployv1alpha1.Release{}

		parsed, err := ParseDeploymentOptions(options, &release)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(map[string]interface{}{
			"revision": "{.config.revisions[?(@.name==\"infrastructure\")].",
		}))
	})
})
