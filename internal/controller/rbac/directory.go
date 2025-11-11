package rbac

import (
	"context"
)

// DirectoryProvider understands what directory service to use for different subject kinds
type DirectoryProvider map[string]Directory

func (p DirectoryProvider) Register(kind string, directory Directory) {
	p[kind] = directory
}

func (p DirectoryProvider) Get(kind string) Directory {
	return p[kind]
}

// Directory is the interface we expect to be exposed by a directory system.
type Directory interface {
	MembersOf(ctx context.Context, group string) ([]string, error)
}

// Ensure each directory implements the interface
var _ Directory = &cachedDirectory{}
var _ Directory = &googleDirectory{}
var _ Directory = &fakeDirectory{}
