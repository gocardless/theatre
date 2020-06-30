package controllers

import (
	"context"
	"time"

	"github.com/go-logr/logr"
)

// NewCachedDirectory wraps the given directory so that we cache member lists for the
// given TTL. This is useful when we want to reason about the maximum number of calls to a
// directory API our controllers might make, which helps us avoid API rate limits.
func NewCachedDirectory(logger logr.Logger, directory Directory, ttl time.Duration) *cachedDirectory {
	return &cachedDirectory{
		logger:    logger,
		directory: directory,
		ttl:       ttl,
		cache:     map[string]cacheEntry{},
		now:       time.Now,
	}
}

type cachedDirectory struct {
	logger    logr.Logger
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

		d.logger.Info("event", "cache.expire", "group", group)
		delete(d.cache, group) // expired
	}

	members, err = d.directory.MembersOf(ctx, group)
	if err == nil {
		d.logger.Info("event", "cache.add", "group", group)
		d.cache[group] = cacheEntry{members: members, cachedAt: d.now()}
	}

	return
}
