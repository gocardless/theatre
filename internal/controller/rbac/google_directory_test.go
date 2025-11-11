package rbac

import (
	"context"
	"net/http"

	directoryv1 "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"
	gock "gopkg.in/h2non/gock.v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewGoogleDirectory", func() {
	var (
		directory        *googleDirectory
		pageOne, pageTwo directoryv1.Members
	)

	BeforeEach(func() {
		client := &http.Client{Transport: http.DefaultTransport}

		// Enable HTTP interception
		gock.InterceptClient(client)
		gock.DisableNetworking()
		gock.New("") // this shouldn't be necessary, but is

		service, err := directoryv1.NewService(context.TODO(), option.WithHTTPClient(client))
		Expect(err).NotTo(HaveOccurred())

		directory = NewGoogleDirectory(service.Members)
		directory.perPage = 2 // ensure we request another page
	})

	JustBeforeEach(func() {
		url := "https://admin.googleapis.com/admin/directory/v1/groups/platform%40gocardless.com/members"

		gock.New(url).Times(1).Reply(200).JSON(pageOne)
		gock.New(url).Times(1).MatchParam("pageToken", pageOne.NextPageToken).Reply(200).JSON(pageTwo)
	})

	AfterEach(func() {
		gock.Off()
	})

	Describe("MembersOf", func() {
		var (
			members []string
			err     error
		)

		JustBeforeEach(func() {
			members, err = directory.MembersOf(context.TODO(), "platform@gocardless.com")
			Expect(err).NotTo(HaveOccurred())
		})

		Context("With (perPage + 1) members of platform@gocardless.com", func() {
			BeforeEach(func() {
				pageOne = directoryv1.Members{
					NextPageToken: "next-page-please",
					Members: []*directoryv1.Member{
						{Email: "lawrence@gocardless.com"},
						{Email: "chris@gocardless.com"},
					},
				}

				pageTwo = directoryv1.Members{
					NextPageToken: "",
					Members: []*directoryv1.Member{
						{Email: "natalie@gocardless.com"},
					},
				}
			})

			It("Includes correct number of members", func() {
				Expect(len(members)).To(Equal(3))
			})

			It("Includes members from first page", func() {
				Expect(members).To(ContainElement("lawrence@gocardless.com"))
				Expect(members).To(ContainElement("chris@gocardless.com"))
			})

			It("Includes members from second page", func() {
				Expect(members).To(ContainElement("natalie@gocardless.com"))
			})
		})
	})
})
