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

	Context("InitialiseStatus", func() {
		var release Release

		BeforeEach(func() {
			release = Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "application", ID: "abc123"},
					},
				},
			}
		})

		It("should set the signature when initialised", func() {
			Expect(release.Status.Signature).To(BeEmpty())

			release.InitialiseStatus("test message")

			Expect(release.Status.Signature).NotTo(BeEmpty())
			Expect(release.Status.Signature).To(HaveLen(SignatureLength))
		})

		It("should set conditions when initialised", func() {
			Expect(release.Status.Conditions).To(BeEmpty())

			release.InitialiseStatus("test message")

			Expect(release.Status.Conditions).To(HaveLen(2))
		})

		It("should set the message when initialised", func() {
			release.InitialiseStatus("custom message")

			Expect(release.Status.Message).To(Equal("custom message"))
		})

		It("should use default message when empty string provided", func() {
			release.InitialiseStatus("")

			Expect(release.Status.Message).To(Equal("Release initialised successfully"))
		})
	})

	Context("Signature", func() {
		It("should produce different signatures for releases with different target names", func() {
			releaseA := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "target-a",
					Revisions: []Revision{
						{Name: "app", ID: "abc123"},
					},
				},
			}
			releaseB := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "target-b",
					Revisions: []Revision{
						{Name: "app", ID: "abc123"},
					},
				},
			}

			releaseA.InitialiseStatus("init")
			releaseB.InitialiseStatus("init")

			Expect(releaseA.Status.Signature).NotTo(Equal(releaseB.Status.Signature))
		})

		It("should produce different signatures for releases with different revision IDs", func() {
			releaseA := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "app", ID: "abc123"},
					},
				},
			}
			releaseB := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "app", ID: "xyz789"},
					},
				},
			}

			releaseA.InitialiseStatus("init")
			releaseB.InitialiseStatus("init")

			Expect(releaseA.Status.Signature).NotTo(Equal(releaseB.Status.Signature))
		})

		It("should produce different signatures for releases with different revision names", func() {
			releaseA := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "app-a", ID: "abc123"},
					},
				},
			}
			releaseB := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "app-b", ID: "abc123"},
					},
				},
			}

			releaseA.InitialiseStatus("init")
			releaseB.InitialiseStatus("init")

			Expect(releaseA.Status.Signature).NotTo(Equal(releaseB.Status.Signature))
		})

		It("should produce identical signatures for identical release configs", func() {
			releaseA := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "app", ID: "abc123"},
					},
				},
			}
			releaseB := Release{
				ReleaseConfig: ReleaseConfig{
					TargetName: "test-target",
					Revisions: []Revision{
						{Name: "app", ID: "abc123"},
					},
				},
			}

			releaseA.InitialiseStatus("init")
			releaseB.InitialiseStatus("init")

			Expect(releaseA.Status.Signature).To(Equal(releaseB.Status.Signature))
		})
	})

})
