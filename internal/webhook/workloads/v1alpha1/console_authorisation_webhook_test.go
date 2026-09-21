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

		Context("Adding an authoriser who has already authorised the console", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_duplicate.yaml"
			})

			JustBeforeEach(func() {
				update.user = "user1"
				err = update.Validate()
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("spec.authorisations field can only be appended to")))
			})
		})

		// The theatre-consoles CLI sets the console's namespace on the subject,
		// and a client may set the apiGroup that the API server would default.
		// A legitimate self-append that also smuggles in a second copy of an
		// existing approver, inflating the total without producing an extra
		// entry in the diff.
		Context("Adding themselves while duplicating an existing authoriser", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_smuggled_duplicate.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("spec.authorisations field can only be appended to")))
			})
		})

		Context("Adding a single authoriser with an explicit apiGroup and namespace", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_canonical.yaml"
			})

			It("Returns no errors", func() {
				Expect(err).To(BeNil())
			})
		})

		Context("Adding an authoriser as a non-User subject kind", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_group_kind.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("an authoriser must be a User subject")))
			})
		})

		Context("Adding an authoriser belonging to a foreign apiGroup", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_foreign_apigroup.yaml"
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("an authoriser must belong to the \"rbac.authorization.k8s.io\" apiGroup")))
			})
		})

		Context("Adding an authoriser who has already authorised the console, using a different namespace on the Subject", func() {
			BeforeEach(func() {
				updateFixture = "./testdata/console_authorisation_update_add_namespace_variant.yaml"
			})

			JustBeforeEach(func() {
				update.user = "user1"
				err = update.Validate()
			})

			It("Returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("this user has already authorised the console")))
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
