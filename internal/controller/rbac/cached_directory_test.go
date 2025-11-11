package rbac

import (
	"context"
	"net/http"
	"time"

	directoryv1 "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"
	gock "gopkg.in/h2non/gock.v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewCachedDirectory", func() {
	var (
		directory *cachedDirectory
		groups    map[string][]string
		now       time.Time
		ttl       time.Duration
	)

	JustBeforeEach(func() {
		directory = NewCachedDirectory(
			zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)),
			NewFakeDirectory(groups),
			ttl,
		)

		directory.now = func() time.Time { return now }
	})

	BeforeEach(func() {
		ttl = time.Second
		now = time.Now()
		groups = map[string][]string{
			"fellowship@lo.tr": {
				"frodo@lo.tr",
				"sam@lo.tr",
				"boromir@lo.tr",
			},
		}
	})

	Describe("MembersOf", func() {
		var (
			members []string
			err     error
		)

		JustBeforeEach(func() {
			members, err = directory.MembersOf(context.TODO(), "fellowship@lo.tr")
			Expect(err).NotTo(HaveOccurred())
		})

		It("Returns members from underlying directory", func() {
			Expect(members).To(
				ConsistOf(
					Equal("frodo@lo.tr"),
					Equal("sam@lo.tr"),
					Equal("boromir@lo.tr"),
				),
			)
		})

		Context("When called again after directory changed", func() {
			var (
				membersAgain []string
			)

			JustBeforeEach(func() {
				groups["fellowship@lo.tr"] = []string{
					"frodo@lo.tr",
					"sam@lo.tr",
					// "boromir@lo.tr", // for Gondor!
				}

				membersAgain, err = directory.MembersOf(context.TODO(), "fellowship@lo.tr")
				Expect(err).NotTo(HaveOccurred())
			})

			It("Returns cached results", func() {
				Expect(membersAgain).To(
					ConsistOf(
						Equal("frodo@lo.tr"),
						Equal("sam@lo.tr"),
						Equal("boromir@lo.tr"),
					),
				)
			})

			Context("Beyond TTL", func() {
				JustBeforeEach(func() {
					now = now.Add(time.Duration(2) * ttl)
				})

				It("Returns fresh results", func() {
					Expect(membersAgain).NotTo(
						ConsistOf(
							Equal("boromir@lo.tr"),
						),
					)
				})
			})
		})
	})
})

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

		service, err := directoryv1.NewService(context.TODO(), option.WithHTTPClient(client))
		Expect(err).NotTo(HaveOccurred())

		directory = NewGoogleDirectory(service.Members)
	})

	JustBeforeEach(func() {
		gock.New("https://admin.googleapis.com/admin/directory/v1/groups/platform%40gocardless.com/members").
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
						{Email: "lawrence@gocardless.com"},
						{Email: "chris@gocardless.com"},
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
