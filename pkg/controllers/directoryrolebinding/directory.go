package directoryrolebinding

import (
	"context"

	directoryv1 "google.golang.org/api/admin/directory/v1"
)

// GoogleMaxMembers denotes the maximum number of members we attempt to retrieve from
// Google's directory service. In future we should cache our Directory source and rely on
// pagination to ensure we're getting all the members.
const GoogleMaxMembers = 500

// Directory is the interface we expect to be exposed by a directory system.
type Directory interface {
	MembersOf(ctx context.Context, group string) ([]string, error)
}

// Ensure each directory implements the interface
var _ Directory = &googleDirectory{}
var _ Directory = &fakeDirectory{}

// NewGoogleDirectory wraps a Google admin directory service to match our interface
func NewGoogleDirectory(service *directoryv1.MembersService) Directory {
	return &googleDirectory{service}
}

type googleDirectory struct {
	*directoryv1.MembersService
}

func (d *googleDirectory) MembersOf(ctx context.Context, group string) ([]string, error) {
	members := []string{}
	resp, err := d.List(group).Context(ctx).Do()
	if err != nil {
		return members, err
	}

	for _, member := range resp.Members {
		members = append(members, member.Email)
	}

	return members, nil
}

// NewFakeDirectory provides the directory service from a map of members
func NewFakeDirectory(members map[string][]string) Directory {
	return &fakeDirectory{members}
}

type fakeDirectory struct {
	members map[string][]string
}

func (d *fakeDirectory) MembersOf(_ context.Context, group string) ([]string, error) {
	if members, ok := d.members[group]; ok {
		return members, nil
	}

	return []string{}, nil
}
