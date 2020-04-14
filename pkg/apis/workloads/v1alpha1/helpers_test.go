package v1alpha1

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Helpers", func() {

	Describe("GetAuthorisationRuleForCommand", func() {
		var (
			// Inputs
			command  []string
			template ConsoleTemplate

			// Outputs
			err    error
			result ConsoleAuthorisationRule
		)

		BeforeEach(func() {
			// Reset to empty defaults each time, to avoid pollution between specs
			template = ConsoleTemplate{}
			command = []string{}
		})

		JustBeforeEach(func() {
			result, err = template.GetAuthorisationRuleForCommand(command)
		})

		Context("with a basic match pattern", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						Name:                 "non-matching",
						MatchCommandElements: []string{"irb"},
					},
					{
						Name:                 "matching",
						MatchCommandElements: []string{"bash"},
					},
				}
				command = []string{"bash"}
			})

			It("matches successfully", func() {
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns the name of the matching rule", func() {
				Expect(result.Name).To(Equal("matching"))
			})
		})

		Context("with a basic match pattern that is longer than the command", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"echo", "hello"},
					},
				}
				command = []string{"echo"}
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("no rules matched the command")))
			})
		})

		Context("with a match pattern that contains single wildcards", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"rake", "*", "*"},
					},
				}
				command = []string{"rake", "task:do_thing", "some-args"}
			})

			It("matches successfully", func() {
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("with a match pattern that contains double wildcards", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"rails", "**"},
					},
				}
				command = []string{"rails", "runner", "thing"}
			})

			It("matches successfully", func() {
				Expect(err).NotTo(HaveOccurred())
			})

			Context("with a command that has no additional arguments", func() {
				BeforeEach(func() {
					command = []string{"rails"}
				})

				It("matches successfully", func() {
					Expect(err).NotTo(HaveOccurred())
				})
			})

			Context("with the double wildcards in the middle of the pattern", func() {
				BeforeEach(func() {
					template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
						{
							MatchCommandElements: []string{"rails", "**", "other-stuff"},
						},
					}
					command = []string{"rails", "runner", "thing"}
				})

				It("returns an error", func() {
					Expect(err).To(HaveOccurred())
					Expect(err).To(MatchError(ContainSubstring("double wildcard is only valid at the end")))
				})
			})
		})

		Context("with no matching rules", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"ruby"},
					},
				}
				command = []string{"perl"}
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("no rules matched the command")))
			})

			Context("with a default rule", func() {
				auths := 3

				BeforeEach(func() {
					template.Spec.DefaultAuthorisationRule = &ConsoleAuthorisers{
						AuthorisationsRequired: auths,
					}
					command = []string{"python"}
				})

				It("matches successfully", func() {
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns the default rule", func() {
					Expect(result.AuthorisationsRequired).To(Equal(auths))
				})
			})
		})

		Context("with the command empty", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"./start-console"},
					},
				}
				command = []string{}
			})

			It("matches no rules", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("no rules matched the command")))
			})
		})

		Context("with an invalid rule", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{""},
					},
				}
				command = []string{"true"}
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("empty match element is not valid")))
			})
		})
	})
})
