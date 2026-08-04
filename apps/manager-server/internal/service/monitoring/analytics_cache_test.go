package monitoring

import (
	"strconv"
	"testing"
	"time"
)

func TestAnalyticsMemoryCacheFreshAndStaleStates(t *testing.T) {
	cache := newAnalyticsMemoryCache(5*time.Second, 30*time.Second)
	key := "analytics:test"
	response := Response{GeneratedAtMS: 42}
	storedAt := time.Now().Add(-6 * time.Second)
	cache.setAt(key, response, storedAt)

	got, state, gotStoredAt := cache.get(key)
	if state != monitoringCacheStale {
		t.Fatalf("state = %d, want stale", state)
	}
	if got.GeneratedAtMS != response.GeneratedAtMS {
		t.Fatalf("response = %#v, want %#v", got, response)
	}
	if gotStoredAt.IsZero() || !gotStoredAt.Equal(storedAt) {
		t.Fatalf("stored_at = %v, want %v", gotStoredAt, storedAt)
	}

	cache.setAt(key, response, time.Now())
	_, state, _ = cache.get(key)
	if state != monitoringCacheFresh {
		t.Fatalf("fresh state = %d, want fresh", state)
	}

	cache.setAt(key, response, time.Now().Add(-31*time.Second))
	_, state, _ = cache.get(key)
	if state != monitoringCacheMiss {
		t.Fatalf("expired state = %d, want miss", state)
	}
}

func TestAnalyticsMemoryCacheIsBounded(t *testing.T) {
	cache := newAnalyticsMemoryCache(time.Minute, 2*time.Minute)
	for index := 0; index < analyticsCacheMaxEntries+8; index++ {
		cache.set("analytics:"+strconv.Itoa(index), Response{GeneratedAtMS: int64(index)})
	}
	cache.mu.Lock()
	entries := len(cache.entries)
	cache.mu.Unlock()
	if entries != analyticsCacheMaxEntries {
		t.Fatalf("entries = %d, want %d", entries, analyticsCacheMaxEntries)
	}
}

func TestAnalyticsCacheKeyNormalizesRollingWindow(t *testing.T) {
	base := Request{
		FromMS:      1_700_000_000_000,
		ToMS:        1_700_000_123_456,
		NowMS:       1_700_000_123_400,
		SearchQuery: "  GPT-4  ",
		Filters: Filters{
			CacheStatus: " HIT ",
		},
	}
	nearby := base
	nearby.FromMS++
	nearby.ToMS++
	nearby.NowMS++
	nearby.SearchQuery = "gpt-4"
	nearby.Filters.CacheStatus = "hit"
	if got, want := analyticsCacheKey(nearby), analyticsCacheKey(base); got != want {
		t.Fatalf("nearby rolling key = %q, want %q", got, want)
	}

	fixed := base
	fixed.NowMS = 0
	if got, want := analyticsCacheKey(fixed), analyticsCacheKey(base); got == want {
		t.Fatalf("fixed and rolling keys unexpectedly match: %q", got)
	}
}
