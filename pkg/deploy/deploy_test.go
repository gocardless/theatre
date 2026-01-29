package deploy

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
)

var _ = Describe("Helpers", func() {
	DescribeTable("validateTargetName",
		func(name string, expectedErrorMatchers ...types.GomegaMatcher) {
			Expect(validateTargetName(name)).To(And(expectedErrorMatchers...))
		},
		Entry("Should return error for empty target name", "", HaveOccurred()),
		Entry("Should return an error if not a valid K8s name", ".1-test-target", HaveOccurred()),
		Entry("Should return an error if too long", "a"+strings.Repeat("b", 245), HaveOccurred(), MatchError(ContainSubstring("target name too long"))),
		Entry("Should not return an error for a valid K8s name", "test-target-123", Succeed()),
	)

	DescribeTable("validateRevision",
		func(rev string, expectedErrorMatchers ...types.GomegaMatcher) {
			Expect(validateRevisionID(rev)).To(And(expectedErrorMatchers...))
		},
		Entry("Should return error for empty revision", "", HaveOccurred()),
		Entry("Should not return an error for a valid revision", "v1.0.0", Succeed()),
		Entry("Should not return an error for a semantic version", "1.2.3-alpha.1+build.1", Succeed()),
		Entry("Should not return an error for a commit hash", "e79564fbef044b63d560296cdc8e84c130175016", Succeed()),
	)

	DescribeTable("validateRevisions",
		func(revs []deployv1alpha1.Revision, expectedErrorMatchers ...types.GomegaMatcher) {
			Expect(validateRevisions(revs)).To(And(expectedErrorMatchers...))
		},
		Entry("Should return error for empty revisions", []deployv1alpha1.Revision{}, HaveOccurred()),
		Entry("Should return error for duplicate revision names", []deployv1alpha1.Revision{
			{Name: "application", ID: "123"},
			{Name: "application", ID: "456"},
		}, HaveOccurred()),
		Entry("Should not return error for valid revisions", []deployv1alpha1.Revision{
			{Name: "application", ID: "123"},
			{Name: "data-contracts", ID: "456"},
		}, Succeed()),
		Entry("Should return error for empty or invalid revision ID", []deployv1alpha1.Revision{
			{Name: "application", ID: "123"},
			{Name: "data-contracts", ID: ""},
		}, HaveOccurred()),
	)

	Context("hashString", func() {
		It("Should hash a string and return hex representation", func() {
			result := hashString([]byte("test"))
			Expect(result).To(HaveLen(7))
			Expect(result).To(MatchRegexp("^[0-9a-f]+$"))
		})

		It("Should produce consistent hashes for the same input", func() {
			result1 := hashString([]byte("test"))
			result2 := hashString([]byte("test"))
			Expect(result1).To(Equal(result2))
		})

		It("Should produce different hashes for different inputs", func() {
			result1 := hashString([]byte("test"))
			result2 := hashString([]byte("different"))
			Expect(result1).NotTo(Equal(result2))
		})
	})

	Context("generateReleaseName", func() {
		var release deployv1alpha1.Release

		BeforeEach(func() {
			release = deployv1alpha1.Release{
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "test-target",
					Revisions: []deployv1alpha1.Revision{
						{Name: "application", ID: "abc123"},
					},
				},
			}
		})

		It("Should generate a release name", func() {
			name, err := GenerateReleaseName(release)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).NotTo(BeEmpty())
			Expect(name).To(Equal("test-target-3978d50"))
		})

		It("Should generate consistent release names for the same input", func() {
			releaseCopy := release.DeepCopy()

			name1, err1 := GenerateReleaseName(release)
			name2, err2 := GenerateReleaseName(*releaseCopy)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(name1).To(Equal(name2))
		})

		It("Should generate consistent release names for the same input if sorted differently", func() {
			// Create two releases with the same revisions but in different orders
			release.Revisions = append(release.Revisions, deployv1alpha1.Revision{Name: "database", ID: "def456"})

			release1 := release.DeepCopy()
			release2 := release.DeepCopy()

			release2.Revisions[0], release2.Revisions[1] = release2.Revisions[1], release2.Revisions[0]

			name1, err1 := GenerateReleaseName(*release1)
			name2, err2 := GenerateReleaseName(*release2)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(name1).To(Equal(name2))
		})

		It("Should error when invalid a revision is provided", func() {
			release.Revisions = append(release.Revisions, deployv1alpha1.Revision{Name: "", ID: ""})
			_, err := GenerateReleaseName(release)
			Expect(err).To(HaveOccurred())
		})

		It("Should error when invalid targetName is provided", func() {
			release.TargetName = ""
			_, err := GenerateReleaseName(release)
			Expect(err).To(HaveOccurred())
		})

	})

	Context("GenerateAnalysisRunName", func() {
		DescribeTable("GenerateAnalysisRunName", func(releaseName, templateName, expected string) {
			result := GenerateAnalysisRunName(releaseName, templateName)
			Expect(len(result)).To(BeNumerically("<=", 64))

			var releaseNameTrim string
			var templateNameTrim string

			if len(releaseName) > 27 {
				releaseNameTrim = releaseName[:27]
			} else {
				releaseNameTrim = releaseName
			}

			if len(templateName) > 27 {
				templateNameTrim = templateName[:27]
			} else {
				templateNameTrim = templateName
			}

			// we should always have AT LEAST the first 27 characters of each part.
			Expect(result).To(HavePrefix(releaseNameTrim))
			Expect(result).To(ContainSubstring(templateNameTrim))

			// only check expected value if it is not empty.
			// if names are truncated, we don't know what the hash will be
			if expected != "" {
				Expect(result).To(Equal(expected))
			}
		},
			Entry("short names", "release", "template", "release-template"),
			Entry("short names 2", "foo", "bar", "foo-bar"),
			Entry("long but acceptable release name", "release-name-is-very-long-but-still-fits-in-the-maxx", "template12", "release-name-is-very-long-but-still-fits-in-the-maxx-template12"),
			Entry("long but acceptable template name", "releasefoo", "template-name-is-very-long-but-still-fits-in-the-max", "releasefoo-template-name-is-very-long-but-still-fits-in-the-max"),
			Entry("release name too long", "release-name-is-very-long-and-does-not-fit-in-the-max", "template12", ""),
			Entry("template name too long", "releasefoo", "template-name-is-very-long-and-does-not-fit-in-the-max", ""),
			Entry("both names too long", "release-name-is-very-long-too-longx", "template-name-is-very-long-too-long", ""),
		)
	})
})
