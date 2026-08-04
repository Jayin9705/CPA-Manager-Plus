package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

// UsageRedisPublisher is the producer used by the collector after the
// cutover. It converts the normalized in-process event to the same wire
// envelope used by the SQLite tail relay, so PostgreSQL persistence has one
// idempotent path during and after migration.
type UsageRedisPublisher struct {
	Client *redis.Client
	Stream string
}

func NewUsageRedisPublisher(ctx context.Context, options RedisOptions, stream, group string) (*UsageRedisPublisher, error) {
	client, err := OpenRedis(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := EnsureUsageStream(ctx, client, stream, group); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &UsageRedisPublisher{Client: client, Stream: stream}, nil
}

func (p *UsageRedisPublisher) Close() error {
	if p == nil || p.Client == nil {
		return nil
	}
	return p.Client.Close()
}

func (p *UsageRedisPublisher) Publish(ctx context.Context, events []usage.Event) error {
	if p == nil || p.Client == nil {
		return fmt.Errorf("usage redis publisher is not open")
	}
	for _, event := range events {
		wireEvent := SQLiteEventFromUsageEvent(event)
		payload, err := marshalStreamEvent(wireEvent)
		if err != nil {
			return err
		}
		if _, err := p.Client.XAdd(ctx, &redis.XAddArgs{
			Stream: p.Stream,
			Values: map[string]interface{}{
				"version":         "1",
				"source_event_id": wireEvent.ID,
				"event_hash":      wireEvent.EventHash,
				"event_json":      payload,
			},
		}).Result(); err != nil {
			return fmt.Errorf("publish usage event %d %s: %w", wireEvent.ID, wireEvent.EventHash, err)
		}
	}
	return nil
}

// SQLiteEventFromUsageEvent preserves nullability at the wire boundary while
// keeping the existing PostgreSQL mapper unchanged. The source ID is a
// producer-local monotonic timestamp/sequence surrogate; event_hash remains
// the authoritative idempotency key.
func SQLiteEventFromUsageEvent(event usage.Event) SQLiteEvent {
	createdAtMS := event.CreatedAtMS
	if createdAtMS <= 0 {
		createdAtMS = time.Now().UnixMilli()
	}
	responseMetadataJSON := strings.TrimSpace(event.ResponseMetadataJSON)
	if responseMetadataJSON == "" && event.ResponseMetadata != nil {
		if payload, err := json.Marshal(event.ResponseMetadata); err == nil {
			responseMetadataJSON = string(payload)
		}
	}
	return SQLiteEvent{
		ID:                      createdAtMS,
		RequestID:               nullString(event.RequestID),
		EventHash:               event.EventHash,
		TimestampMS:             event.TimestampMS,
		Timestamp:               event.Timestamp,
		Provider:                nullString(event.Provider),
		ExecutorType:            nullString(event.ExecutorType),
		Model:                   event.Model,
		Endpoint:                nullString(event.Endpoint),
		Method:                  nullString(event.Method),
		Path:                    nullString(event.Path),
		AuthType:                nullString(event.AuthType),
		AuthIndex:               nullString(event.AuthIndex),
		Source:                  nullString(event.Source),
		SourceHash:              nullString(event.SourceHash),
		APIKeyHash:              nullString(event.APIKeyHash),
		AccountSnapshot:         nullString(event.AccountSnapshot),
		AuthLabelSnapshot:       nullString(event.AuthLabelSnapshot),
		AuthFileSnapshot:        nullString(event.AuthFileSnapshot),
		AuthProviderSnapshot:    nullString(event.AuthProviderSnapshot),
		AuthProjectIDSnapshot:   nullString(event.AuthProjectIDSnapshot),
		AuthSnapshotAtMS:        nullPositiveInt64(event.AuthSnapshotAtMS),
		RequestedModel:          nullString(event.RequestedModel),
		ResolvedModel:           nullString(event.ResolvedModel),
		ReasoningEffort:         nullString(event.ReasoningEffort),
		ServiceTier:             nullString(event.ServiceTier),
		RequestServiceTier:      nullString(event.RequestServiceTier),
		ResponseServiceTier:     nullString(event.ResponseServiceTier),
		CacheInputMode:          nullString(event.CacheInputMode),
		InputTokens:             event.InputTokens,
		OutputTokens:            event.OutputTokens,
		ReasoningTokens:         event.ReasoningTokens,
		CachedTokens:            event.CachedTokens,
		CacheTokens:             event.CacheTokens,
		CacheReadTokens:         event.CacheReadTokens,
		CacheCreationTokens:     event.CacheCreationTokens,
		NormalizedUncachedInput: nullPositiveInt64(event.NormalizedUncachedInputTokens),
		NormalizedTotalInput:    nullPositiveInt64(event.NormalizedTotalInputTokens),
		NormalizedCacheRead:     nullPositiveInt64(event.NormalizedCacheReadTokens),
		NormalizedCacheCreation: nullPositiveInt64(event.NormalizedCacheCreationTokens),
		TotalTokens:             event.TotalTokens,
		LatencyMS:               eventLatency(event.LatencyMS),
		TTFTMS:                  eventLatency(event.TTFTMS),
		Failed:                  boolToInt64(event.Failed),
		FailStatusCode:          nullPositiveInt64(int64(event.FailStatusCode)),
		FailSummary:             nullString(event.FailSummary),
		ResponseMetadataJSON:    nullString(responseMetadataJSON),
		HeaderQuotaRecoverAtMS:  nullPositiveInt64(event.HeaderQuotaRecoverAtMS),
		HeaderQuotaUsedPercent:  nullFloat64(event.HeaderQuotaUsedPercent),
		HeaderQuotaPlanType:     nullString(event.HeaderQuotaPlanType),
		HeaderErrorKind:         nullString(event.HeaderErrorKind),
		HeaderErrorCode:         nullString(event.HeaderErrorCode),
		HeaderTraceID:           nullString(event.HeaderTraceID),
		FailBody:                nullString(event.FailBody),
		RawJSON:                 nullString(event.RawJSON),
		CreatedAtMS:             createdAtMS,
	}
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullPositiveInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: value > 0}
}

func nullFloat64(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func eventLatency(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
