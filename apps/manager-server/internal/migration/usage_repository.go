package migration

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const defaultPostgresUsageLimit = 50000

// InsertEvents is the synchronous PostgreSQL path used by usage imports. Live
// collector traffic continues through Redis Stream, while both paths converge
// on the same event_hash idempotency key and rollup ledger.
func (p *Postgres) InsertEvents(ctx context.Context, events []usage.Event) (store.InsertResult, error) {
	if p == nil || p.Pool == nil {
		return store.InsertResult{}, fmt.Errorf("postgres is not open")
	}
	if len(events) == 0 {
		return store.InsertResult{}, nil
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return store.InsertResult{}, fmt.Errorf("begin PostgreSQL usage insert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := store.InsertResult{}
	for _, input := range events {
		event := normalizePostgresUsageEvent(input)
		wire := SQLiteEventFromUsageEvent(event)
		command, execErr := tx.Exec(ctx, insertEventSQL, eventArgs(wire)...)
		if execErr != nil {
			return store.InsertResult{}, fmt.Errorf("insert PostgreSQL usage event %s: %w", event.EventHash, execErr)
		}
		if command.RowsAffected() == 0 {
			result.Skipped++
			continue
		}
		if _, execErr = tx.Exec(ctx, `insert into cpamp_usage.usage_event_identity_ledger (
			event_hash, raw_event_id, timestamp_ms, bucket_ms,
			aggregate_schema_version, daily_aggregate_schema_version,
			first_seen_at_ms, updated_at_ms
		) select event_hash, id, timestamp_ms, timestamp_ms - (timestamp_ms % 3600000),
			0, 0, created_at_ms, created_at_ms
		from cpamp_usage.usage_events where event_hash = $1
		on conflict (event_hash) do update set
			raw_event_id = excluded.raw_event_id,
			timestamp_ms = excluded.timestamp_ms,
			bucket_ms = excluded.bucket_ms,
			updated_at_ms = excluded.updated_at_ms`, event.EventHash); execErr != nil {
			return store.InsertResult{}, fmt.Errorf("attach PostgreSQL usage ledger %s: %w", event.EventHash, execErr)
		}
		result.Inserted++
		result.InsertedEventHashes = append(result.InsertedEventHashes, event.EventHash)
	}
	if err := tx.Commit(ctx); err != nil {
		return store.InsertResult{}, fmt.Errorf("commit PostgreSQL usage insert: %w", err)
	}
	return result, nil
}

func normalizePostgresUsageEvent(event usage.Event) usage.Event {
	accounting := usage.NormalizeCacheAccounting(usage.CacheInputContext{
		ExplicitMode:     event.CacheInputMode,
		ExecutorType:     event.ExecutorType,
		Provider:         event.Provider,
		ProviderSnapshot: event.AuthProviderSnapshot,
		ResolvedModel:    event.ResolvedModel,
		RequestedModel:   event.RequestedModel,
		DisplayModel:     event.Model,
	}, event.InputTokens, event.CachedTokens, event.CacheTokens, event.CacheReadTokens, event.CacheCreationTokens)
	event.CacheInputMode = accounting.Mode
	event.NormalizedUncachedInputTokens = accounting.UncachedInputTokens
	event.NormalizedTotalInputTokens = accounting.TotalInputTokens
	event.NormalizedCacheReadTokens = accounting.CacheReadTokens
	event.NormalizedCacheCreationTokens = accounting.CacheCreationTokens
	if event.TotalTokens <= 0 {
		event.TotalTokens = accounting.TotalInputTokens + max(event.OutputTokens, int64(0)) + max(event.ReasoningTokens, int64(0))
	}
	if event.RequestServiceTier == "" {
		event.RequestServiceTier = event.ServiceTier
	}
	event.ServiceTier = usage.EffectiveServiceTier(usage.CacheInputContext{
		ExecutorType:     event.ExecutorType,
		Provider:         event.Provider,
		ProviderSnapshot: event.AuthProviderSnapshot,
		AuthType:         event.AuthType,
	}, event.RequestServiceTier, event.ServiceTier, event.ResponseServiceTier)
	if event.CreatedAtMS <= 0 {
		event.CreatedAtMS = time.Now().UnixMilli()
	}
	return event
}

func (p *Postgres) RecentEvents(ctx context.Context, limit int) ([]usage.Event, error) {
	if p == nil || p.Pool == nil {
		return nil, fmt.Errorf("postgres is not open")
	}
	limit = normalizePostgresUsageLimit(limit)
	rows, err := p.Pool.Query(ctx, `select
		coalesce(request_id, ''), event_hash, timestamp_ms, timestamp,
		coalesce(provider, ''), coalesce(executor_type, ''), model,
		coalesce(endpoint, ''), coalesce(method, ''), coalesce(path, ''),
		coalesce(auth_type, ''), coalesce(auth_index, ''), coalesce(source, ''),
		coalesce(source_hash, ''), coalesce(api_key_hash, ''),
		coalesce(account_snapshot, ''), coalesce(auth_label_snapshot, ''),
		coalesce(auth_file_snapshot, ''), coalesce(auth_provider_snapshot, ''),
		coalesce(auth_project_id_snapshot, ''), auth_snapshot_at_ms,
		coalesce(requested_model, ''), coalesce(resolved_model, ''),
		coalesce(reasoning_effort, ''), coalesce(service_tier, ''),
		coalesce(request_service_tier, ''), coalesce(response_service_tier, ''),
		coalesce(cache_input_mode, ''), input_tokens, output_tokens, reasoning_tokens,
		cached_tokens, cache_tokens, cache_read_tokens, cache_creation_tokens,
		normalized_uncached_input, normalized_total_input, normalized_cache_read,
		normalized_cache_creation, total_tokens, latency_ms, ttft_ms, failed,
		fail_status_code, coalesce(fail_summary, ''),
		coalesce(response_metadata_json::text, ''), header_quota_recover_at_ms,
		header_quota_used_percent, coalesce(header_quota_plan_type, ''),
		coalesce(header_error_kind, ''), coalesce(header_error_code, ''),
		coalesce(header_trace_id, ''), coalesce(raw_json::text, ''), created_at_ms
	from cpamp_usage.usage_events
	order by timestamp_ms desc, id desc limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL usage events: %w", err)
	}
	defer rows.Close()

	result := make([]usage.Event, 0)
	for rows.Next() {
		event, scanErr := scanPostgresUsageEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL usage events: %w", err)
	}
	return result, nil
}

type postgresUsageScanner interface {
	Scan(dest ...any) error
}

func scanPostgresUsageEvent(row postgresUsageScanner) (usage.Event, error) {
	var event usage.Event
	var authSnapshotAt, normalizedUncached, normalizedTotal, normalizedRead, normalizedCreation sql.NullInt64
	var latency, ttft, failStatus, quotaRecover sql.NullInt64
	var quotaUsed sql.NullFloat64
	var metadataJSON, rawJSON string
	if err := row.Scan(
		&event.RequestID, &event.EventHash, &event.TimestampMS, &event.Timestamp,
		&event.Provider, &event.ExecutorType, &event.Model, &event.Endpoint, &event.Method,
		&event.Path, &event.AuthType, &event.AuthIndex, &event.Source, &event.SourceHash,
		&event.APIKeyHash, &event.AccountSnapshot, &event.AuthLabelSnapshot,
		&event.AuthFileSnapshot, &event.AuthProviderSnapshot, &event.AuthProjectIDSnapshot,
		&authSnapshotAt, &event.RequestedModel, &event.ResolvedModel, &event.ReasoningEffort,
		&event.ServiceTier, &event.RequestServiceTier, &event.ResponseServiceTier,
		&event.CacheInputMode, &event.InputTokens, &event.OutputTokens, &event.ReasoningTokens,
		&event.CachedTokens, &event.CacheTokens, &event.CacheReadTokens,
		&event.CacheCreationTokens, &normalizedUncached, &normalizedTotal, &normalizedRead,
		&normalizedCreation, &event.TotalTokens, &latency, &ttft, &event.Failed, &failStatus,
		&event.FailSummary, &metadataJSON, &quotaRecover, &quotaUsed,
		&event.HeaderQuotaPlanType, &event.HeaderErrorKind, &event.HeaderErrorCode,
		&event.HeaderTraceID, &rawJSON, &event.CreatedAtMS,
	); err != nil {
		return usage.Event{}, fmt.Errorf("scan PostgreSQL usage event: %w", err)
	}
	if authSnapshotAt.Valid {
		event.AuthSnapshotAtMS = authSnapshotAt.Int64
	}
	if normalizedUncached.Valid {
		event.NormalizedUncachedInputTokens = normalizedUncached.Int64
	}
	if normalizedTotal.Valid {
		value := normalizedTotal.Int64
		event.NormalizedTotalInputTokens = value
	}
	if normalizedRead.Valid {
		event.NormalizedCacheReadTokens = normalizedRead.Int64
	}
	if normalizedCreation.Valid {
		value := normalizedCreation.Int64
		event.NormalizedCacheCreationTokens = value
	}
	if latency.Valid {
		value := latency.Int64
		event.LatencyMS = &value
	}
	if ttft.Valid {
		value := ttft.Int64
		event.TTFTMS = &value
	}
	if failStatus.Valid {
		event.FailStatusCode = int(failStatus.Int64)
	}
	if quotaRecover.Valid {
		event.HeaderQuotaRecoverAtMS = quotaRecover.Int64
	}
	if quotaUsed.Valid {
		value := quotaUsed.Float64
		event.HeaderQuotaUsedPercent = &value
	}
	event.ResponseMetadataJSON = metadataJSON
	event.ResponseMetadata = usage.ResponseHeaderMetadataFromJSON(metadataJSON)
	event.RawJSON = rawJSON
	return event, nil
}

func (p *Postgres) ModelUsageSummary(ctx context.Context, limit int) (store.ModelUsageSummary, error) {
	if p == nil || p.Pool == nil {
		return store.ModelUsageSummary{}, fmt.Errorf("postgres is not open")
	}
	limit = normalizePostgresUsageLimit(limit)
	rows, err := p.Pool.Query(ctx, `with recent as (
		select model, coalesce(resolved_model, '') as resolved_model
		from cpamp_usage.usage_events order by timestamp_ms desc, id desc limit $1
	), model_counts as (
		select model, count(*) as requested_calls, 0::bigint as resolved_calls
		from recent where model <> '' group by model
		union all
		select resolved_model, 0::bigint, count(*)
		from recent where resolved_model <> '' and resolved_model <> model group by resolved_model
	), aggregated as (
		select model,
			sum(requested_calls) as requested_calls,
			sum(resolved_calls) as resolved_calls
		from model_counts
		group by model
	)
	select model, requested_calls, resolved_calls
	from aggregated order by requested_calls + resolved_calls desc, model asc`, limit)
	if err != nil {
		return store.ModelUsageSummary{}, fmt.Errorf("read PostgreSQL model usage: %w", err)
	}
	defer rows.Close()
	models := make([]store.ModelUsageStat, 0)
	for rows.Next() {
		var stat store.ModelUsageStat
		if err := rows.Scan(&stat.Model, &stat.RequestedCalls, &stat.ResolvedCalls); err != nil {
			return store.ModelUsageSummary{}, fmt.Errorf("scan PostgreSQL model usage: %w", err)
		}
		stat.Calls = stat.RequestedCalls + stat.ResolvedCalls
		models = append(models, stat)
	}
	if err := rows.Err(); err != nil {
		return store.ModelUsageSummary{}, err
	}
	total, err := p.CountUsageEvents(ctx)
	if err != nil {
		return store.ModelUsageSummary{}, err
	}
	sampled := total
	if sampled > int64(limit) {
		sampled = int64(limit)
	}
	return store.ModelUsageSummary{SampledEvents: sampled, TotalEvents: total, Truncated: sampled < total, Models: models}, nil
}

func (p *Postgres) WriteExportJSONL(ctx context.Context, writer io.Writer, limit int) error {
	events, err := p.RecentEvents(ctx, limit)
	if err != nil {
		return err
	}
	buffer := bufio.NewWriterSize(writer, 64*1024)
	encoder := json.NewEncoder(buffer)
	for index := len(events) - 1; index >= 0; index-- {
		if err := encoder.Encode(events[index]); err != nil {
			return err
		}
	}
	return buffer.Flush()
}

func (p *Postgres) WriteCompatibleUsage(ctx context.Context, writer io.Writer, limit int) error {
	events, err := p.RecentEvents(ctx, limit)
	if err != nil {
		return err
	}
	payload := usage.Payload{APIs: make(map[string]*usage.APIAggregate)}
	for _, event := range events {
		payload.TotalRequests++
		payload.TotalTokens += event.TotalTokens
		if event.Failed {
			payload.FailureCount++
		} else {
			payload.SuccessCount++
		}
		endpoint := strings.TrimSpace(event.Endpoint)
		if endpoint == "" {
			endpoint = "-"
		}
		modelName := strings.TrimSpace(event.Model)
		if modelName == "" {
			modelName = "-"
		}
		api := payload.APIs[endpoint]
		if api == nil {
			api = &usage.APIAggregate{Models: make(map[string]*usage.ModelAggregate)}
			payload.APIs[endpoint] = api
		}
		modelAggregate := api.Models[modelName]
		if modelAggregate == nil {
			modelAggregate = &usage.ModelAggregate{}
			api.Models[modelName] = modelAggregate
		}
		modelAggregate.Details = append(modelAggregate.Details, usage.Detail{
			Timestamp:             event.Timestamp,
			Source:                event.Source,
			AuthIndex:             event.AuthIndex,
			APIKeyHash:            event.APIKeyHash,
			AccountSnapshot:       event.AccountSnapshot,
			AuthLabelSnapshot:     event.AuthLabelSnapshot,
			AuthFileSnapshot:      event.AuthFileSnapshot,
			AuthProviderSnapshot:  event.AuthProviderSnapshot,
			AuthProjectIDSnapshot: event.AuthProjectIDSnapshot,
			AuthSnapshotAtMS:      event.AuthSnapshotAtMS,
			LatencyMS:             event.LatencyMS,
			TTFTMS:                event.TTFTMS,
			ResolvedModel:         event.ResolvedModel,
			ReasoningEffort:       event.ReasoningEffort,
			ServiceTier:           event.ServiceTier,
			RequestServiceTier:    event.RequestServiceTier,
			ResponseServiceTier:   event.ResponseServiceTier,
			CacheInputMode:        event.CacheInputMode,
			ExecutorType:          event.ExecutorType,
			Tokens: usage.Tokens{
				InputTokens:         event.InputTokens,
				OutputTokens:        event.OutputTokens,
				ReasoningTokens:     event.ReasoningTokens,
				CachedTokens:        usage.CompatibleCachedTokens(event.CachedTokens, event.CacheTokens, event.CacheReadTokens, event.CacheCreationTokens),
				CacheTokens:         usage.CompatibleCachedTokens(event.CachedTokens, event.CacheTokens, event.CacheReadTokens, event.CacheCreationTokens),
				CacheReadTokens:     event.CacheReadTokens,
				CacheCreationTokens: event.CacheCreationTokens,
				TotalTokens:         event.TotalTokens,
			},
			Failed:           event.Failed,
			FailStatusCode:   event.FailStatusCode,
			FailSummary:      event.FailSummary,
			ResponseMetadata: event.ResponseMetadata,
		})
	}
	return json.NewEncoder(writer).Encode(payload)
}

func (p *Postgres) BackfillUsageResponseMetadata(context.Context, int) (int, error) {
	// PostgreSQL persistence derives response metadata before commit. The
	// legacy SQLite repair worker therefore has no work after cutover.
	return 0, nil
}

func normalizePostgresUsageLimit(limit int) int {
	if limit <= 0 {
		return defaultPostgresUsageLimit
	}
	return limit
}
