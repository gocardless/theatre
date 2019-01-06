package directoryrolebinding

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"

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
var _ Directory = &cachedDirectory{}
var _ Directory = &googleDirectory{}
var _ Directory = &fakeDirectory{}

// NewCachedDirectory wraps the given directory so that we cache member lists for the
// given TTL. This is useful when we want to reason about the maximum number of calls to a
// directory API our controllers might make, which helps us avoid API rate limits.
func NewCachedDirectory(logger kitlog.Logger, directory Directory, ttl time.Duration) *cachedDirectory {
	return &cachedDirectory{
		logger:    logger,
		directory: directory,
		ttl:       ttl,
		cache:     map[string]cacheEntry{},
		now:       time.Now,
	}
}

type cachedDirectory struct {
	logger    kitlog.Logger
	directory Directory
	ttl       time.Duration
	cache     map[string]cacheEntry
	now       func() time.Time
}

type cacheEntry struct {
	members  []string
	cachedAt time.Time
}

func (d *cachedDirectory) MembersOf(ctx context.Context, group string) (members []string, err error) {
	if entry, ok := d.cache[group]; ok {
		if d.now().Sub(entry.cachedAt) < d.ttl { // within ttl
			return entry.members, nil
		}

		d.logger.Log("event", "cache.expire", "group", group)
		delete(d.cache, group) // expired
	}

	members, err = d.directory.MembersOf(ctx, group)
	if err == nil {
		d.logger.Log("event", "cache.add", "group", group)
		d.cache[group] = cacheEntry{members: members, cachedAt: d.now()}
	}

	return
}

// NewGoogleDirectory wraps a Google admin directory service to match our interface
func NewGoogleDirectory(service *directoryv1.MembersService) *googleDirectory {
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
