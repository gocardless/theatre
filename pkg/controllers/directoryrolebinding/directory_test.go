package directoryrolebinding

import (
	"context"
	"net/http"

	directoryv1 "google.golang.org/api/admin/directory/v1"
	gock "gopkg.in/h2non/gock.v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewGoogleDirectory", func() {
	var (
		directory       Directory
		membersResponse directoryv1.Members
	)

	BeforeEach(func() {
		client := &http.Client{Transport: http.DefaultTransport}

		// Enable HTTP interception
		gock.InterceptClient(client)
		gock.DisableNetworking()
		gock.New("") // this shouldn't be necessary, but is

		service, err := directoryv1.New(client)
		Expect(err).NotTo(HaveOccurred())

		directory = NewGoogleDirectory(service.Members)
	})

	JustBeforeEach(func() {
		gock.New("https://www.googleapis.com/admin/directory/v1/groups/platform%40gocardless.com/members").
			Reply(200).
			JSON(membersResponse)
	})

	AfterEach(func() {
		gock.Off()
	})

	Describe("MembersOf", func() {
		Context("With two members of platform@gocardless.com", func() {
			BeforeEach(func() {
				membersResponse = directoryv1.Members{
					Members: []*directoryv1.Member{
						&directoryv1.Member{Email: "lawrence@gocardless.com"},
						&directoryv1.Member{Email: "chris@gocardless.com"},
					},
				}
			})

			It("Retrieves both members", func() {
				members, err := directory.MembersOf(context.TODO(), "platform@gocardless.com")

				Expect(err).NotTo(HaveOccurred())
				Expect(members).To(
					ConsistOf(
						"lawrence@gocardless.com",
						"chris@gocardless.com",
					),
				)
			})
		})
	})
})
