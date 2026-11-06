package infrastructure

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/geoip"
)

const securityUnreadTTL = 30 * time.Second

type securityUnreadEntry struct {
	count             int64
	expires, accessed time.Time
	done              chan struct{}
	err               error
}

type securityUnreadCache struct {
	mu       sync.Mutex
	entries  map[domain.SecurityEventReadScope]*securityUnreadEntry
	capacity int
	now      func() time.Time
}

// NewCachedSecurityEventRepository coalesces notification polling while keeping
// event details live. A mark-read commit invalidates the local scoped count.
// Other replicas and newly published warnings become visible within 30 seconds.
func NewCachedSecurityEventRepository(database *gorm.DB, resolver ...*geoip.Resolver) *SecurityEventRepository {
	repository := NewSecurityEventRepository(database, resolver...)
	repository.unreadCache = &securityUnreadCache{
		entries: make(map[domain.SecurityEventReadScope]*securityUnreadEntry), capacity: 512, now: time.Now,
	}
	return repository
}

func (cache *securityUnreadCache) get(ctx context.Context, scope domain.SecurityEventReadScope, load func(context.Context) (int64, error)) (int64, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		entry, done, owner := cache.acquire(scope)
		if entry == nil {
			return load(ctx)
		}
		if !owner {
			if done == nil {
				return entry.count, nil
			}
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-done:
				if entry.err != nil {
					return 0, entry.err
				}
				continue
			}
		}
		count, err := load(ctx)
		if current := cache.finish(scope, entry, count, err); current || err != nil {
			return count, err
		}
		// A concurrent acknowledgement invalidated this in-flight snapshot.
		// Reload instead of returning or republishing its pre-commit count.
	}
}

func (cache *securityUnreadCache) acquire(scope domain.SecurityEventReadScope) (*securityUnreadEntry, <-chan struct{}, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	if entry := cache.entries[scope]; entry != nil {
		entry.accessed = now
		if entry.done != nil || now.Before(entry.expires) {
			return entry, entry.done, false
		}
		delete(cache.entries, scope)
	}
	if !cache.makeRoom() {
		return nil, nil, true
	}
	entry := &securityUnreadEntry{done: make(chan struct{}), accessed: now, expires: now.Add(securityUnreadTTL)}
	cache.entries[scope] = entry
	return entry, entry.done, true
}

func (cache *securityUnreadCache) finish(scope domain.SecurityEventReadScope, entry *securityUnreadEntry, count int64, err error) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	current := cache.entries[scope] == entry
	entry.count, entry.err = count, err
	if current && err != nil {
		delete(cache.entries, scope)
	}
	close(entry.done)
	entry.done = nil
	return current
}

// makeRoom evicts only completed entries; active readers retain their rendezvous.
// The caller holds mu, and a fully busy cache falls back to an uncached read.
func (cache *securityUnreadCache) makeRoom() bool {
	if len(cache.entries) < cache.capacity {
		return true
	}
	var oldest domain.SecurityEventReadScope
	var accessed time.Time
	for scope, entry := range cache.entries {
		if entry.done == nil && (accessed.IsZero() || entry.accessed.Before(accessed)) {
			oldest, accessed = scope, entry.accessed
		}
	}
	if accessed.IsZero() {
		return false
	}
	delete(cache.entries, oldest)
	return true
}

func (cache *securityUnreadCache) invalidate(scope domain.SecurityEventReadScope) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	delete(cache.entries, scope)
	cache.mu.Unlock()
}
