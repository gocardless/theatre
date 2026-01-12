package v1alpha1

import (
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	workloadsv1alpha1 "github.com/gocardless/theatre/v5/api/workloads/v1alpha1"
)

func mustConsoleAuthorisationFixture(path string) *workloadsv1alpha1.ConsoleAuthorisation {
	consoleAuthorisation := &workloadsv1alpha1.ConsoleAuthorisation{}

	consoleAuthorisationFixtureYAML, _ := os.ReadFile(path)

	decoder := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
	if err := runtime.DecodeInto(decoder, consoleAuthorisationFixtureYAML, consoleAuthorisation); err != nil {
		admission.Errored(http.StatusBadRequest, err)
	}

	return consoleAuthorisation
}

var _ = Describe("Authorisation webhook", func() {
	Describe("Validate", func() {
		var (
			updateFixture string
			update        *ConsoleAuthorisationUpdate
			err           error
		)

		existingAuth := mustConsoleAuthorisationFixture("./testdata/console_authorisation_existing.yaml")

		JustBeforeEach(func() {
			updatedAuth := mustConsoleAuthorisationFixture(updateFixture)
			update = &ConsoleAuthorisationUpdate{
				existingAuth: existingAuth,
				updatedAuth:  updatedAuth,
				user:         "current-user",
				owner:        "user",
			}

			err = update.Validate()
		})

		Context("Adding a single authoriser", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add.yaml"
			})

			It("Returns no errors", func() {
				Expect(err).To(BeNil())
			})
		})

		// We don't want to prevent an update if there's changes to parts of the
		// object that do not affect functionality, e.g. annotations and labels.
		Context("Update to non-spec fields only", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_annotations.yaml"
			})

			It("Returns no errors", func() {
				Expect(err).To(BeNil())
			})
		})

		Context("Adding multiple authorisers", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_multiple.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("spec.authorisations field can only be appended to")))
			})
		})

		Context("Adding an authoriser who is another user", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_another_user.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("only the current user can be added as an authoriser")))
			})
		})

		Context("Adding an authoriser who is the console owner", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_owner.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("authoriser cannot authorise their own console")))
			})
		})

		Context("Changing immutable fields", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_immutables.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("field is immutable")))
			})
		})

		Context("Removing an existing authoriser", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_remove.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("spec.authorisations field can only be appended to")))
			})
		})
	})
})
