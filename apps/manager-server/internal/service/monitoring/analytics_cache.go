package monitoring

import (
	"sync"
	"time"
)

const (
	defaultAnalyticsCacheFreshTTL = 5 * time.Second
	defaultAnalyticsCacheStaleTTL = 30 * time.Second
	analyticsCacheMaxEntries      = 64
)

// analyticsMemoryCache is a small bounded L1 cache. It intentionally stores
// only completed responses; raw usage events never live here. Keeping this
// cache in the manager process removes a Redis round trip from the normal
// refresh path and gives the stale-while-revalidate path a deterministic
// response time when Redis or PostgreSQL is briefly busy.
type analyticsMemoryCache struct {
	mu       sync.Mutex
	entries  map[string]analyticsMemoryCacheEntry
	sequence uint64
	max      int
	freshTTL time.Duration
	staleTTL time.Duration
}

type analyticsMemoryCacheEntry struct {
	response Response
	storedAt time.Time
	lastUsed uint64
}

func newAnalyticsMemoryCache(freshTTL, staleTTL time.Duration) *analyticsMemoryCache {
	if freshTTL <= 0 {
		freshTTL = defaultAnalyticsCacheFreshTTL
	}
	if staleTTL < freshTTL {
		staleTTL = defaultAnalyticsCacheStaleTTL
		if staleTTL < freshTTL {
			staleTTL = freshTTL
		}
	}
	return &analyticsMemoryCache{
		entries:  make(map[string]analyticsMemoryCacheEntry),
		max:      analyticsCacheMaxEntries,
		freshTTL: freshTTL,
		staleTTL: staleTTL,
	}
}

func (c *analyticsMemoryCache) get(key string) (Response, monitoringCacheState, time.Time) {
	if c == nil {
		return Response{}, monitoringCacheMiss, time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return Response{}, monitoringCacheMiss, time.Time{}
	}
	age := time.Since(entry.storedAt)
	if age < 0 {
		age = 0
	}
	if age > c.staleTTL {
		delete(c.entries, key)
		return Response{}, monitoringCacheMiss, time.Time{}
	}
	c.sequence++
	entry.lastUsed = c.sequence
	c.entries[key] = entry
	state := monitoringCacheFresh
	if age > c.freshTTL {
		state = monitoringCacheStale
	}
	return entry.response, state, entry.storedAt
}

func (c *analyticsMemoryCache) set(key string, response Response) {
	c.setAt(key, response, time.Now())
}

func (c *analyticsMemoryCache) setAt(key string, response Response, storedAt time.Time) {
	if c == nil || key == "" {
		return
	}
	if storedAt.IsZero() {
		storedAt = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	c.entries[key] = analyticsMemoryCacheEntry{
		response: response,
		storedAt: storedAt,
		lastUsed: c.sequence,
	}
	for len(c.entries) > c.max {
		var oldestKey string
		var oldestSequence uint64
		for candidateKey, candidate := range c.entries {
			if oldestKey == "" || candidate.lastUsed < oldestSequence {
				oldestKey = candidateKey
				oldestSequence = candidate.lastUsed
			}
		}
		if oldestKey == "" {
			break
		}
		delete(c.entries, oldestKey)
	}
}
