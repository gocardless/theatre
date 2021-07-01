package v1alpha1

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Helpers", func() {

	Describe("ConsoleTemplate GetAuthorisationRuleForCommand", func() {
		var (
			// Inputs
			command  []string
			template ConsoleTemplate

			// Outputs
			err    error
			result ConsoleAuthorisationRule
		)

		defaultRuleAuths := 3

		BeforeEach(func() {
			// Reset to empty defaults each time, to avoid pollution between specs
			template = ConsoleTemplate{}
			command = []string{}

			// Always set a default authorisation rule, because we require one if any
			// rules are set.
			template.Spec.DefaultAuthorisationRule = &ConsoleAuthorisers{
				AuthorisationsRequired: defaultRuleAuths,
			}
		})

		JustBeforeEach(func() {
			result, err = template.GetAuthorisationRuleForCommand(command)
		})

		Context("with a default rule only", func() {
			It("returns the default rule", func() {
				Expect(result.AuthorisationsRequired).To(Equal(defaultRuleAuths))
			})
		})

		// Generally we'll never reach this case in real usage, as we should only
		// be calling GetAuthorisationRuleForCommand if HasAuthorisationRules
		// returns true.
		Context("with no rules defined", func() {
			BeforeEach(func() {
				template.Spec.DefaultAuthorisationRule = nil
				command = []string{"bash"}
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(BeEquivalentTo("no rules matched the command")))
			})
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

			It("returns the default rule", func() {
				Expect(result.AuthorisationsRequired).To(Equal(defaultRuleAuths))
			})
		})

		Context("with a basic match pattern that is shorter than the command", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"echo"},
					},
				}
				command = []string{"echo", "hello"}
			})

			It("returns the default rule", func() {
				Expect(result.AuthorisationsRequired).To(Equal(defaultRuleAuths))
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
			It("does not return the default rule", func() {
				Expect(result.AuthorisationsRequired).NotTo(Equal(defaultRuleAuths))
			})
		})

		Context("with a single wildcard match pattern that is longer than the command", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"echo", "*"},
					},
				}
				command = []string{"echo"}
			})

			It("returns the default rule", func() {
				Expect(result.AuthorisationsRequired).To(Equal(defaultRuleAuths))
			})
		})

		Context("with a match pattern that contains double wildcards", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"rails", "runner", "**"},
					},
				}
				command = []string{"rails", "runner", "thing"}
			})

			It("matches successfully", func() {
				Expect(err).NotTo(HaveOccurred())
			})
			It("does not return the default rule", func() {
				Expect(result.AuthorisationsRequired).NotTo(Equal(defaultRuleAuths))
			})

			Context("with a command that has no additional arguments", func() {
				BeforeEach(func() {
					command = []string{"rails", "runner"}
				})

				It("matches successfully", func() {
					Expect(err).NotTo(HaveOccurred())
				})
				It("does not return the default rule", func() {
					Expect(result.AuthorisationsRequired).NotTo(Equal(defaultRuleAuths))
				})
			})

			Context("with a command that is shorter than the the pre-** matchers", func() {
				BeforeEach(func() {
					command = []string{"rails"}
				})

				It("returns the default rule", func() {
					Expect(result.AuthorisationsRequired).To(Equal(defaultRuleAuths))
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
				command = []string{"python"}
			})

			It("matches successfully", func() {
				Expect(err).NotTo(HaveOccurred())
			})
			It("returns the default rule", func() {
				Expect(result.AuthorisationsRequired).To(Equal(defaultRuleAuths))
			})
		})

		Context("with a matching rule that isn't the first or last match", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						Name:                 "python",
						MatchCommandElements: []string{"python"},
					},
					{
						Name:                 "perl",
						MatchCommandElements: []string{"perl"},
						ConsoleAuthorisers: ConsoleAuthorisers{
							AuthorisationsRequired: 7,
						},
					},
					{
						Name:                 "php",
						MatchCommandElements: []string{"php"},
					},
				}
				command = []string{"perl"}
			})

			It("matches successfully", func() {
				Expect(err).NotTo(HaveOccurred())
			})
			It("returns the name of the matching rule", func() {
				Expect(result.Name).To(Equal("perl"))
			})
			It("has the correct authorisations required", func() {
				Expect(result.AuthorisationsRequired).To(Equal(7))
			})
		})
	})

	Describe("ConsoleTemplate Validate", func() {
		var (
			template ConsoleTemplate
			err      error
		)

		BeforeEach(func() {
			// Reset to empty defaults each time, to avoid pollution between specs
			template = ConsoleTemplate{}
		})

		JustBeforeEach(func() {
			err = template.Validate()
		})

		Context("with an invalid rule", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{""},
					},
				}
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring(".spec.authorisationRules[0].matchCommandElements[0]: an empty matcher is invalid")))
			})
		})

		Context("with a rule that contains double wildcards in the middle of a pattern", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"bash"},
					},
					{
						MatchCommandElements: []string{"rails", "**", "other-stuff"},
					},
				}
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring(".spec.authorisationRules[1].matchCommandElements[1]: a double wildcard is only valid at the end of the pattern")))
			})
		})

		Context("with authorisation rules but no default rule", func() {
			BeforeEach(func() {
				template.Spec.AuthorisationRules = []ConsoleAuthorisationRule{
					{
						MatchCommandElements: []string{"bash"},
					},
				}
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring(".spec.defaultAuthorisationRule must be set if authorisation rules are defined")))
			})
		})
	})
})
