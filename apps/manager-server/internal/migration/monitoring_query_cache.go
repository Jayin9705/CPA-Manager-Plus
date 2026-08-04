package migration

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// monitoringQueryCacheKey is private so callers can only opt into a cache by
// using WithMonitoringQueryCache. A cache is intentionally request-scoped;
// unlike the Redis response cache it must never retain a large event snapshot
// between HTTP requests.
type monitoringQueryCacheKey struct{}

type monitoringEventsCache struct {
	mu      sync.Mutex
	entries map[string]*monitoringEventsCacheEntry
}

type monitoringEventsCacheEntry struct {
	done   chan struct{}
	events []postgresAnalyticsEvent
	err    error
}

// WithMonitoringQueryCache enables single-flight raw-event reads for one
// analytics request. A full analytics response can ask for many dimensions
// over the exact same filter; all of those readers should share one PG
// snapshot instead of independently scanning and decoding usage_events.
func WithMonitoringQueryCache(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, monitoringQueryCacheKey{}, &monitoringEventsCache{
		entries: make(map[string]*monitoringEventsCacheEntry),
	})
}

func monitoringEventsCacheFromContext(ctx context.Context) *monitoringEventsCache {
	if ctx == nil {
		return nil
	}
	cache, _ := ctx.Value(monitoringQueryCacheKey{}).(*monitoringEventsCache)
	return cache
}

func (c *monitoringEventsCache) load(
	ctx context.Context,
	key string,
	loader func(context.Context) ([]postgresAnalyticsEvent, error),
) ([]postgresAnalyticsEvent, error) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok {
		entry = &monitoringEventsCacheEntry{done: make(chan struct{})}
		c.entries[key] = entry
		c.mu.Unlock()

		events, err := loader(ctx)
		c.mu.Lock()
		entry.events = events
		entry.err = err
		close(entry.done)
		c.mu.Unlock()
		return events, err
	}
	c.mu.Unlock()

	select {
	case <-entry.done:
		return entry.events, entry.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func monitoringAnalyticsFilterKey(filter store.AnalyticsFilter) string {
	payload, err := json.Marshal(filter)
	if err != nil {
		// AnalyticsFilter contains only JSON-safe scalar and string-slice
		// fields. Keep a deterministic fallback in case that contract changes.
		return "analytics-filter:unmarshalable"
	}
	return string(payload)
}
