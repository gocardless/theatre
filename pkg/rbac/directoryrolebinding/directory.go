package directoryrolebinding

import (
	"context"
)

// Directory is the interface we expect to be exposed by a directory system.
type Directory interface {
	MembersOf(ctx context.Context, group string) ([]string, error)
}

// Ensure each directory implements the interface
var _ Directory = &cachedDirectory{}
var _ Directory = &googleDirectory{}
var _ Directory = &fakeDirectory{}
