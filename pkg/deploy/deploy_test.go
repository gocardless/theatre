package deploy

import (
	"strings"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Helpers", func() {
	Context("validateTargetName", func() {
		It("Should return error for empty target name", func() {
			err := validateTargetName("")
			Expect(err).To(HaveOccurred())
		})

		It("Should return an error if not a valid K8s name", func() {
			err := validateTargetName(".1-test-target")
			Expect(err).To(HaveOccurred())
		})

		It("Should return an error if too long", func() {
			longName := "a" + strings.Repeat("b", 244) // 246 characters, exceeding the limit
			err := validateTargetName(longName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("target name too long"))
		})

		It("Should not return an error for a valid K8s name", func() {
			err := validateTargetName("test-target-123")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("validateRevision", func() {
		It("Should return error for empty revision", func() {
			err := validateRevisionID("")
			Expect(err).To(HaveOccurred())
		})

		It("Should not return an error for a valid revision", func() {
			err := validateRevisionID("v1.0.0")
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should not return an error for a semantic version", func() {
			err := validateRevisionID("1.2.3-alpha.1+build.1")
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should not return an error for a commit hash", func() {
			err := validateRevisionID("e79564fbef044b63d560296cdc8e84c130175016")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("validateRevisions", func() {
		It("Should return error for empty revisions", func() {
			err := validateRevisions([]deployv1alpha1.Revision{})
			Expect(err).To(HaveOccurred())
		})

		It("Should return error for duplicate revision names", func() {
			revisions := []deployv1alpha1.Revision{
				{Name: "application", ID: "123"},
				{Name: "application", ID: "456"},
			}
			err := validateRevisions(revisions)
			Expect(err).To(HaveOccurred())
		})

		It("Should not return error for valid revisions", func() {
			revisions := []deployv1alpha1.Revision{
				{Name: "application", ID: "123"},
				{Name: "data-contracts", ID: "456"},
			}
			err := validateRevisions(revisions)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return error for empty or invalid revision ID", func() {
			revisions := []deployv1alpha1.Revision{
				{Name: "application", ID: "123"},
				{Name: "data-contracts", ID: ""},
			}
			err := validateRevisions(revisions)
			Expect(err).To(HaveOccurred())
		})
	})

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
			release.ReleaseConfig.Revisions = append(release.ReleaseConfig.Revisions, deployv1alpha1.Revision{Name: "database", ID: "def456"})

			release1 := release.DeepCopy()
			release2 := release.DeepCopy()

			release2.ReleaseConfig.Revisions[0], release2.ReleaseConfig.Revisions[1] = release2.ReleaseConfig.Revisions[1], release2.ReleaseConfig.Revisions[0]

			name1, err1 := GenerateReleaseName(*release1)
			name2, err2 := GenerateReleaseName(*release2)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(name1).To(Equal(name2))
		})

		It("Should error when invalid a revision is provided", func() {
			release.ReleaseConfig.Revisions = append(release.ReleaseConfig.Revisions, deployv1alpha1.Revision{Name: "", ID: ""})
			_, err := GenerateReleaseName(release)
			Expect(err).To(HaveOccurred())
		})

		It("Should error when invalid targetName is provided", func() {
			release.ReleaseConfig.TargetName = ""
			_, err := GenerateReleaseName(release)
			Expect(err).To(HaveOccurred())
		})

	})
})
