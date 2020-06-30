package controllers

import (
	"context"

	directoryv1 "google.golang.org/api/admin/directory/v1"
)

const (
	// GooglePerPage states how many members we retrive in each pagination call when talking
	// to the Google directory service
	GooglePerPage = 200
	// GoogleMaxPages limits the number of pages we iterate through when talking to the
	// Google directory service. In combination with the GooglePerPage constant, this
	// effectively limits the size of the group we can process.
	GoogleMaxPages = 10
)

// NewGoogleDirectory wraps a Google admin directory service to match our interface
func NewGoogleDirectory(service *directoryv1.MembersService) *googleDirectory {
	return &googleDirectory{
		MembersService: service,
		perPage:        GooglePerPage,
	}
}

type googleDirectory struct {
	*directoryv1.MembersService
	perPage int64
}

func (d *googleDirectory) MembersOf(ctx context.Context, group string) (members []string, err error) {
	var resp *directoryv1.Members

	members = []string{}
	call := d.List(group).MaxResults(d.perPage).Context(ctx)

	// Limit the number of pages both to restrict the maximum number of members we support,
	// but also to ensure any bug in Google's pagination won't result in us infinitely
	// looping
	for remainingPages := GoogleMaxPages; remainingPages > 0; remainingPages-- {
		resp, err = call.Do()
		if err != nil {
			return
		}

		for _, member := range resp.Members {
			members = append(members, member.Email)
		}

		if resp.NextPageToken == "" {
			return // we have no next page
		}

		call = call.PageToken(resp.NextPageToken)
	}

	return
}
