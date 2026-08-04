package monitoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// v2 stores an envelope rather than a bare response. The envelope lets the
	// reader distinguish a fresh response from a stale response and serve the
	// latter while a background refresh is in flight.
	monitoringCacheKeyPrefix = "cpamp:monitoring:v2:"
	monitoringCacheOpTimeout = 80 * time.Millisecond
	monitoringCacheStaleTTL  = 30 * time.Second
	analyticsCacheBucketMS   = int64(5 * time.Second / time.Millisecond)
)

type monitoringCacheState uint8

const (
	monitoringCacheMiss monitoringCacheState = iota
	monitoringCacheFresh
	monitoringCacheStale
)

type monitoringCacheEnvelope struct {
	StoredAtMS int64           `json:"stored_at_ms"`
	Payload    json.RawMessage `json:"payload"`
}

// RedisCache is deliberately best-effort. A Redis outage must never make the
// management API unavailable; the request simply falls through to the source
// query and the next successful response can repopulate the cache.
type RedisCache struct {
	client   *redis.Client
	freshTTL time.Duration
	staleTTL time.Duration
}

func NewRedisCache(address, password string, database int, ttl time.Duration) *RedisCache {
	if address == "" || ttl <= 0 {
		return nil
	}
	return &RedisCache{
		client: redis.NewClient(&redis.Options{
			Addr:         address,
			Password:     password,
			DB:           database,
			DialTimeout:  75 * time.Millisecond,
			ReadTimeout:  monitoringCacheOpTimeout,
			WriteTimeout: monitoringCacheOpTimeout,
			PoolSize:     4,
			MinIdleConns: 0,
		}),
		freshTTL: ttl,
		staleTTL: maxDuration(monitoringCacheStaleTTL, ttl*6),
	}
}

func maxDuration(left, right time.Duration) time.Duration {
	if right > left {
		return right
	}
	return left
}

func (c *RedisCache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *RedisCache) get(ctx context.Context, key string, destination any) bool {
	state, _ := c.getState(ctx, key, destination)
	return state != monitoringCacheMiss
}

func (c *RedisCache) getState(ctx context.Context, key string, destination any) (monitoringCacheState, time.Time) {
	if c == nil || c.client == nil {
		return monitoringCacheMiss, time.Time{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operationCtx, cancel := context.WithTimeout(ctx, monitoringCacheOpTimeout)
	defer cancel()
	payload, err := c.client.Get(operationCtx, key).Bytes()
	if err != nil {
		return monitoringCacheMiss, time.Time{}
	}
	var envelope monitoringCacheEnvelope
	if json.Unmarshal(payload, &envelope) != nil || envelope.StoredAtMS <= 0 || len(envelope.Payload) == 0 {
		return monitoringCacheMiss, time.Time{}
	}
	if json.Unmarshal(envelope.Payload, destination) != nil {
		return monitoringCacheMiss, time.Time{}
	}
	storedAt := time.UnixMilli(envelope.StoredAtMS)
	age := time.Since(storedAt)
	if age < 0 {
		age = 0
	}
	if age <= c.freshTTL {
		return monitoringCacheFresh, storedAt
	}
	if age <= c.staleTTL {
		return monitoringCacheStale, storedAt
	}
	return monitoringCacheMiss, time.Time{}
}

// set is retained for the small non-analytics monitoring endpoints. Analytics
// uses setAsync so a JSON encode or Redis write never extends the HTTP request.
func (c *RedisCache) set(ctx context.Context, key string, value any) {
	if c == nil || c.client == nil {
		return
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	envelope, err := json.Marshal(monitoringCacheEnvelope{
		StoredAtMS: time.Now().UnixMilli(),
		Payload:    payload,
	})
	if err != nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), monitoringCacheOpTimeout)
	defer cancel()
	_ = c.client.Set(operationCtx, key, envelope, c.staleTTL).Err()
}

func (c *RedisCache) setAsync(key string, value any) {
	if c == nil || c.client == nil {
		return
	}
	go c.set(context.Background(), key, value)
}

func monitoringCacheKey(kind string, value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		payload = []byte("null")
	}
	digest := sha256.Sum256(payload)
	return monitoringCacheKeyPrefix + kind + ":" + hex.EncodeToString(digest[:])
}

func analyticsCacheKey(req Request) string {
	// The UI commonly sends fresh now_ms/to_ms values for the same selected
	// rolling range. Exclude now_ms and quantize a live end point so a 5-second
	// refresh does not create a new Redis key on every millisecond tick. The
	// response is deliberately bounded by the fresh/stale TTLs below, so this
	// small window is safe for monitoring data.
	keyRequest := req
	keyRequest.NowMS = 0
	if analyticsRequestIsRolling(req) {
		if keyRequest.FromMS > 0 {
			keyRequest.FromMS = (keyRequest.FromMS / analyticsCacheBucketMS) * analyticsCacheBucketMS
		}
		if keyRequest.ToMS > 0 {
			keyRequest.ToMS = (keyRequest.ToMS / analyticsCacheBucketMS) * analyticsCacheBucketMS
		}
	}
	// These values are semantically case/whitespace insensitive in both the
	// SQLite and PostgreSQL readers. Canonicalizing them improves hit rate when
	// different UI paths construct equivalent requests.
	keyRequest.SearchQuery = strings.ToLower(strings.TrimSpace(keyRequest.SearchQuery))
	keyRequest.SearchAPIKeyHash = strings.ToLower(strings.TrimSpace(keyRequest.SearchAPIKeyHash))
	keyRequest.Filters.CacheStatus = strings.ToLower(strings.TrimSpace(keyRequest.Filters.CacheStatus))
	return monitoringCacheKey("analytics", keyRequest)
}

func analyticsRequestIsRolling(req Request) bool {
	if req.NowMS <= 0 || req.ToMS <= 0 {
		return false
	}
	delta := req.ToMS - req.NowMS
	if delta < 0 {
		delta = -delta
	}
	return delta <= 2*analyticsCacheBucketMS
}

func headerSnapshotsCacheKey(req HeaderSnapshotsRequest) string {
	return monitoringCacheKey("header-snapshots", req)
}

func accountHistoryCacheKey(req AccountHistoryRequest) string {
	return monitoringCacheKey("account-history", req)
}
