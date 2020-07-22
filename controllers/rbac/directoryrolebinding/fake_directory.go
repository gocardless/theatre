package directoryrolebinding

import (
	"context"
)

// NewFakeDirectory provides the directory service from a map of members
func NewFakeDirectory(groups map[string][]string) *fakeDirectory {
	return &fakeDirectory{groups}
}

type fakeDirectory struct {
	groups map[string][]string
}

func (d *fakeDirectory) MembersOf(_ context.Context, group string) ([]string, error) {
	if members, ok := d.groups[group]; ok {
		return members, nil
	}

	return []string{}, nil
}
