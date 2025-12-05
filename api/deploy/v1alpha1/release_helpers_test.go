package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Release Type Methods", func() {
	Context("Equals", func() {
		var a, b ReleaseConfig

		BeforeEach(func() {
			a = ReleaseConfig{
				TargetName: "test-target",
				Revisions: []Revision{
					{Name: "application", ID: "abc123"},
					{Name: "infrastructure", ID: "xyz789"},
				},
			}
			b = ReleaseConfig{
				TargetName: "test-target",
				Revisions: []Revision{
					{Name: "application", ID: "abc123"},
					{Name: "infrastructure", ID: "xyz789"},
				},
			}
		})

		It("Should be equal for identical configs", func() {
			Expect(a.Equals(&b)).To(BeTrue())
		})

		It("Should not be equal if targetNames are different", func() {
			b.TargetName = "different-target"
			Expect(a.Equals(&b)).To(BeFalse())
		})

		It("Should not be equal if revisions are different", func() {
			b.Revisions[0].ID = "different-id"
			Expect(a.Equals(&b)).To(BeFalse())
		})

		It("Should not be equal if number of revisions are different", func() {
			b.Revisions = append(b.Revisions, Revision{Name: "additional", ID: "extra123"})
			Expect(a.Equals(&b)).To(BeFalse())
		})

		It("Should not be equal if revision names are different", func() {
			b.Revisions[0].Name = "different-app"
			Expect(a.Equals(&b)).To(BeFalse())
		})
	})
})
