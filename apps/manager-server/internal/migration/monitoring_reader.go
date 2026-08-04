package migration

import (
	"context"
	"fmt"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const postgresHourMS int64 = 3_600_000

// UsageHourlyAggregateRows exposes the PostgreSQL permanent hourly aggregate
// through the same read contract used by the existing monitoring rollup
// reader. It intentionally returns unavailable while the PG rollup checkpoint
// is behind the raw event high-water; the caller can then use its legacy
// fallback instead of serving a partial aggregate as complete.
func (p *Postgres) UsageHourlyAggregateRows(
	ctx context.Context,
	filter store.UsageHourlyAggregateFilter,
) ([]store.UsageHourlyAggregateRow, store.UsageHourlyAggregateState, bool, error) {
	if p == nil || p.Pool == nil {
		return nil, store.UsageHourlyAggregateState{}, false, nil
	}
	if filter.FromMS >= filter.ToMS {
		return []store.UsageHourlyAggregateRow{}, store.UsageHourlyAggregateState{
			AggregateName: "postgres.hourly",
			SchemaVersion: 1,
			Status:        "ready",
		}, true, nil
	}
	// The permanent aggregate stores whole UTC hours. Read those rows for the
	// stable middle of the range and aggregate only the two partial edges from
	// PostgreSQL raw events. The old implementation rejected any non-aligned
	// range and made the caller rescan the entire window from usage_events.
	// Monitoring windows are normally based on the current millisecond, so that
	// fallback turned every refresh into a full fact-table scan.
	fullStartMS := ceilPostgresHourMS(filter.FromMS)
	fullEndMS := floorPostgresHourMS(filter.ToMS)
	if fullStartMS >= fullEndMS {
		return nil, store.UsageHourlyAggregateState{
			AggregateName: "postgres.hourly",
			SchemaVersion: 1,
			Status:        "edge_only",
		}, false, nil
	}

	var latestEventID, rollupEventID int64
	if err := p.Pool.QueryRow(ctx, `
		select coalesce(max(id), 0)
		from cpamp_usage.usage_events`).Scan(&latestEventID); err != nil {
		return nil, store.UsageHourlyAggregateState{}, false, fmt.Errorf("read PostgreSQL usage high-water: %w", err)
	}
	var rollupStatus string
	if err := p.Pool.QueryRow(ctx, `
		select coalesce(last_copied_id, 0), coalesce(status, '')
		from cpamp_usage.cpamp_migration_state
		where migration_name = 'rollup.hourly'`).Scan(&rollupEventID, &rollupStatus); err != nil {
		return nil, store.UsageHourlyAggregateState{}, false, fmt.Errorf("read PostgreSQL hourly rollup checkpoint: %w", err)
	}
	state := store.UsageHourlyAggregateState{
		AggregateName:       "postgres.hourly",
		SchemaVersion:       1,
		Status:              rollupStatus,
		CoverageEventID:     rollupEventID,
		TargetEventID:       latestEventID,
		BackfillLastEventID: rollupEventID,
	}
	if latestEventID > rollupEventID {
		return nil, state, false, nil
	}

	conditions := []string{"bucket_ms >= $1", "bucket_ms < $2"}
	args := []any{fullStartMS, fullEndMS}
	argIndex := 3
	if models := normalizePGValues(filter.Models); len(models) > 0 {
		conditions = append(conditions, fmt.Sprintf("model = any($%d)", argIndex))
		args = append(args, models)
		argIndex++
	}
	if !filter.IncludeFailed {
		conditions = append(conditions, "failed = false")
	}
	if filter.FailedOnly {
		conditions = append(conditions, "failed = true")
	}

	bucketExpr := "bucket_ms"
	if filter.CollapseBuckets {
		bucketExpr = "0"
	}
	query := fmt.Sprintf(`
		select
			%s as bucket_ms,
			model,
			billing_model,
			service_tier,
			failed,
			sum(calls),
			sum(input_tokens),
			sum(output_tokens),
			sum(reasoning_tokens),
			sum(cached_tokens),
			sum(cache_read_tokens),
			sum(cache_creation_tokens),
			sum(long_input_tokens),
			sum(long_output_tokens),
			sum(long_cached_tokens),
			sum(long_cache_read_tokens),
			sum(long_cache_creation_tokens),
			sum(total_tokens),
			sum(latency_sum_ms),
			sum(latency_samples),
			sum(zero_token_calls)
		from cpamp_usage.usage_hourly_aggregate_v1
		where %s
		group by 1, model, billing_model, service_tier, failed
		order by 1, model, billing_model, service_tier, failed`,
		bucketExpr, strings.Join(conditions, " and "))

	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, state, false, fmt.Errorf("read PostgreSQL hourly aggregate: %w", err)
	}
	defer rows.Close()
	result := make([]store.UsageHourlyAggregateRow, 0)
	for rows.Next() {
		var row store.UsageHourlyAggregateRow
		if err := rows.Scan(
			&row.BucketMS,
			&row.Model,
			&row.BillingModel,
			&row.ServiceTier,
			&row.Failed,
			&row.Calls,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.CacheReadTokens,
			&row.CacheCreationTokens,
			&row.LongInputTokens,
			&row.LongOutputTokens,
			&row.LongCachedTokens,
			&row.LongCacheReadTokens,
			&row.LongCacheCreationTokens,
			&row.TotalTokens,
			&row.LatencySumMS,
			&row.LatencySamples,
			&row.ZeroTokenCalls,
		); err != nil {
			return nil, state, false, fmt.Errorf("scan PostgreSQL hourly aggregate: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, state, false, fmt.Errorf("iterate PostgreSQL hourly aggregate: %w", err)
	}
	if err := p.appendPostgresRawHourlyRows(ctx, filter, filter.FromMS, fullStartMS, &result); err != nil {
		return nil, state, false, err
	}
	if err := p.appendPostgresRawHourlyRows(ctx, filter, fullEndMS, filter.ToMS, &result); err != nil {
		return nil, state, false, err
	}
	return result, state, true, nil
}

func floorPostgresHourMS(value int64) int64 {
	return value - value%postgresHourMS
}

func ceilPostgresHourMS(value int64) int64 {
	floor := floorPostgresHourMS(value)
	if floor == value {
		return value
	}
	return floor + postgresHourMS
}

func postgresHourlyFilterWhere(filter store.UsageHourlyAggregateFilter, fromMS, toMS int64) (string, []any) {
	conditions := []string{"timestamp_ms >= $1", "timestamp_ms < $2"}
	args := []any{fromMS, toMS}
	next := 3
	if models := normalizePGValues(filter.Models); len(models) > 0 {
		conditions = append(conditions, fmt.Sprintf("model = any($%d)", next))
		args = append(args, models)
		next++
	}
	if !filter.IncludeFailed {
		conditions = append(conditions, "failed = false")
	}
	if filter.FailedOnly {
		conditions = append(conditions, "failed = true")
	}
	return strings.Join(conditions, " and "), args
}

func (p *Postgres) appendPostgresRawHourlyRows(
	ctx context.Context,
	filter store.UsageHourlyAggregateFilter,
	fromMS int64,
	toMS int64,
	result *[]store.UsageHourlyAggregateRow,
) error {
	if fromMS >= toMS {
		return nil
	}
	where, args := postgresHourlyFilterWhere(filter, fromMS, toMS)
	bucketExpr := fmt.Sprintf("timestamp_ms - (timestamp_ms %% %d)", postgresHourMS)
	if filter.CollapseBuckets {
		bucketExpr = "0"
	}
	inputExpr := "coalesce(normalized_total_input, input_tokens, 0)"
	cachedExpr := "greatest(greatest(coalesce(cached_tokens, 0), coalesce(cache_tokens, 0)) - greatest(coalesce(cache_read_tokens, 0), 0) - greatest(coalesce(cache_creation_tokens, 0), 0), 0)"
	longThreshold := usage.LongContextInputTokenThreshold
	query := fmt.Sprintf(`
		select
			%s as bucket_ms,
			model,
			coalesce(nullif(resolved_model, ''), model) as billing_model,
			coalesce(service_tier, '') as service_tier,
			failed,
			count(*),
			coalesce(sum(%s), 0),
			coalesce(sum(coalesce(output_tokens, 0)), 0),
			coalesce(sum(coalesce(reasoning_tokens, 0)), 0),
			coalesce(sum(%s), 0),
			coalesce(sum(coalesce(cache_read_tokens, 0)), 0),
			coalesce(sum(coalesce(cache_creation_tokens, 0)), 0),
			coalesce(sum(case when %s > %d then %s else 0 end), 0),
			coalesce(sum(case when %s > %d then coalesce(output_tokens, 0) else 0 end), 0),
			coalesce(sum(case when %s > %d then %s else 0 end), 0),
			coalesce(sum(case when %s > %d then coalesce(cache_read_tokens, 0) else 0 end), 0),
			coalesce(sum(case when %s > %d then coalesce(cache_creation_tokens, 0) else 0 end), 0),
			coalesce(sum(coalesce(total_tokens, 0)), 0),
			coalesce(sum(case when latency_ms is not null and latency_ms <> 0 then latency_ms else 0 end), 0),
			count(nullif(latency_ms, 0)),
			coalesce(sum(case when coalesce(total_tokens, 0) = 0 and not failed then 1 else 0 end), 0)
		from cpamp_usage.usage_events
		where %s
		group by 1, model, billing_model, service_tier, failed
		order by 1, model, billing_model, service_tier, failed`,
		bucketExpr,
		inputExpr,
		cachedExpr,
		inputExpr, longThreshold, inputExpr,
		inputExpr, longThreshold,
		inputExpr, longThreshold, cachedExpr,
		inputExpr, longThreshold,
		inputExpr, longThreshold,
		where)
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("read PostgreSQL raw hourly edge: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row store.UsageHourlyAggregateRow
		if err := rows.Scan(
			&row.BucketMS,
			&row.Model,
			&row.BillingModel,
			&row.ServiceTier,
			&row.Failed,
			&row.Calls,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.CacheReadTokens,
			&row.CacheCreationTokens,
			&row.LongInputTokens,
			&row.LongOutputTokens,
			&row.LongCachedTokens,
			&row.LongCacheReadTokens,
			&row.LongCacheCreationTokens,
			&row.TotalTokens,
			&row.LatencySumMS,
			&row.LatencySamples,
			&row.ZeroTokenCalls,
		); err != nil {
			return fmt.Errorf("scan PostgreSQL raw hourly edge: %w", err)
		}
		*result = append(*result, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate PostgreSQL raw hourly edge: %w", err)
	}
	return nil
}

func normalizePGValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// LatestHeaderSnapshots reads the latest header-derived snapshot for each
// credential context directly from PostgreSQL. The query mirrors the SQLite
// semantics, including one newest row per snapshot key.
func (p *Postgres) LatestHeaderSnapshots(ctx context.Context, sinceMS int64, limit int) ([]store.HeaderSnapshot, error) {
	if p == nil || p.Pool == nil || limit <= 0 {
		return []store.HeaderSnapshot{}, nil
	}
	rows, err := p.Pool.Query(ctx, `
		with candidates as (
			select
				id,
				event_hash,
				timestamp_ms,
				coalesce(auth_file_snapshot, '') as auth_file_snapshot,
				coalesce(auth_index, '') as auth_index,
				coalesce(account_snapshot, '') as account_snapshot,
				coalesce(auth_label_snapshot, '') as auth_label_snapshot,
				coalesce(nullif(auth_provider_snapshot, ''), provider, '') as auth_provider_snapshot,
				coalesce(auth_project_id_snapshot, '') as auth_project_id_snapshot,
				coalesce(source, '') as source,
				coalesce(source_hash, '') as source_hash,
				coalesce(response_metadata_json, '{}'::jsonb) as response_metadata_json,
				header_quota_recover_at_ms,
				header_quota_used_percent,
				coalesce(header_quota_plan_type, '') as header_quota_plan_type,
				coalesce(header_error_kind, '') as header_error_kind,
				coalesce(header_error_code, '') as header_error_code,
				coalesce(header_trace_id, '') as header_trace_id,
				case
					when btrim(coalesce(auth_file_snapshot, '')) <> '' and btrim(coalesce(auth_index, '')) <> '' then auth_file_snapshot || '::' || auth_index
					when btrim(coalesce(auth_file_snapshot, '')) <> '' then 'file::' || auth_file_snapshot
					when btrim(coalesce(auth_index, '')) <> '' then 'auth::' || auth_index
					when btrim(coalesce(account_snapshot, '')) <> '' then 'account::' || lower(account_snapshot)
					when btrim(coalesce(source_hash, '')) <> '' then 'source::' || source_hash
					else 'event::' || event_hash
				end as snapshot_key
			from cpamp_usage.usage_events
			where timestamp_ms >= $1
			  and (
					response_metadata_json is not null
					or header_quota_recover_at_ms is not null
					or header_quota_used_percent is not null
					or btrim(coalesce(header_quota_plan_type, '')) <> ''
					or btrim(coalesce(header_error_kind, '')) <> ''
					or btrim(coalesce(header_error_code, '')) <> ''
					or btrim(coalesce(header_trace_id, '')) <> ''
				)
			  and (
					btrim(coalesce(auth_file_snapshot, '')) <> ''
					or btrim(coalesce(auth_index, '')) <> ''
					or btrim(coalesce(account_snapshot, '')) <> ''
					or btrim(coalesce(source_hash, '')) <> ''
				)
		), ranked as (
			select *, row_number() over (partition by snapshot_key order by timestamp_ms desc, id desc) as rn
			from candidates
		)
		select
			id,
			event_hash,
			timestamp_ms,
			auth_file_snapshot,
			auth_index,
			account_snapshot,
			auth_label_snapshot,
			auth_provider_snapshot,
			auth_project_id_snapshot,
			source,
			source_hash,
			response_metadata_json,
			header_quota_recover_at_ms,
			header_quota_used_percent,
			header_quota_plan_type,
			header_error_kind,
			header_error_code,
			header_trace_id
		from ranked
		where rn = 1
		order by timestamp_ms desc, id desc
		limit $2`, sinceMS, limit)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL header snapshots: %w", err)
	}
	defer rows.Close()

	items := make([]store.HeaderSnapshot, 0, limit)
	for rows.Next() {
		var item store.HeaderSnapshot
		var responseMetadataJSON []byte
		if err := rows.Scan(
			&item.ID,
			&item.EventHash,
			&item.TimestampMS,
			&item.AuthFileSnapshot,
			&item.AuthIndex,
			&item.AccountSnapshot,
			&item.AuthLabelSnapshot,
			&item.AuthProviderSnapshot,
			&item.AuthProjectIDSnapshot,
			&item.Source,
			&item.SourceHash,
			&responseMetadataJSON,
			&item.HeaderQuotaRecoverAtMS,
			&item.HeaderQuotaUsedPercent,
			&item.HeaderQuotaPlanType,
			&item.HeaderErrorKind,
			&item.HeaderErrorCode,
			&item.HeaderTraceID,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL header snapshot: %w", err)
		}
		item.ResponseMetadata = usage.ResponseHeaderMetadataFromJSON(string(responseMetadataJSON))
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL header snapshots: %w", err)
	}
	return items, nil
}

// LatestUsageEventID returns the PostgreSQL raw-event high-water used by the
// account-history response checkpoint during the read cutover.
func (p *Postgres) LatestUsageEventID(ctx context.Context) (int64, error) {
	if p == nil || p.Pool == nil {
		return 0, nil
	}
	var id int64
	if err := p.Pool.QueryRow(ctx, `select coalesce(max(id), 0) from cpamp_usage.usage_events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("read PostgreSQL usage event high-water: %w", err)
	}
	return id, nil
}

// AccountHistoryRows is the first PG account-history read implementation. It
// uses the raw event fact table with an account-key expression equivalent to
// the legacy rollup. A dedicated PG account-model rollup can replace this
// query later without changing the monitoring service contract.
func (p *Postgres) AccountHistoryRows(ctx context.Context, accountKeys []string) ([]store.AccountHistoryRollupRow, error) {
	keys := normalizePGValues(accountKeys)
	if p == nil || p.Pool == nil || len(keys) == 0 {
		return []store.AccountHistoryRollupRow{}, nil
	}
	rows, err := p.Pool.Query(ctx, `
		with filtered as (
			select
				case
					when btrim(coalesce(account_snapshot, '')) <> '' then account_snapshot
					when btrim(coalesce(auth_label_snapshot, '')) <> '' then auth_label_snapshot
					when btrim(coalesce(source, '')) <> '' then source
					when btrim(coalesce(auth_index, '')) <> '' then auth_index
					else '-'
				end as account_key,
				coalesce(account_snapshot, '') as account_snapshot,
				coalesce(auth_label_snapshot, '') as auth_label_snapshot,
				coalesce(nullif(auth_provider_snapshot, ''), provider, '') as auth_provider_snapshot,
				coalesce(auth_index, '') as auth_index,
				coalesce(source, '') as source,
				coalesce(source_hash, '') as source_hash,
				model,
				coalesce(nullif(resolved_model, ''), model) as billing_model,
				coalesce(service_tier, '') as service_tier,
				failed,
				coalesce(normalized_total_input, input_tokens, 0) as input_tokens,
				coalesce(output_tokens, 0) as output_tokens,
				coalesce(reasoning_tokens, 0) as reasoning_tokens,
				coalesce(cached_tokens, 0) as cached_tokens,
				coalesce(cache_tokens, 0) as cache_tokens,
				coalesce(cache_read_tokens, 0) as cache_read_tokens,
				coalesce(cache_creation_tokens, 0) as cache_creation_tokens,
				coalesce(total_tokens, 0) as total_tokens,
				timestamp_ms,
				created_at_ms
			from cpamp_usage.usage_events
		)
		select
			account_key,
			max(account_snapshot),
			max(auth_label_snapshot),
			max(auth_provider_snapshot),
			max(auth_index),
			max(source),
			max(source_hash),
			model,
			billing_model,
			service_tier,
			count(*) as calls,
			count(*) filter (where not failed) as success_calls,
			count(*) filter (where failed) as failure_calls,
			coalesce(sum(input_tokens), 0),
			coalesce(sum(output_tokens), 0),
			coalesce(sum(reasoning_tokens), 0),
			coalesce(sum(greatest(greatest(cached_tokens, cache_tokens) - greatest(cache_read_tokens, 0) - greatest(cache_creation_tokens, 0), 0)), 0),
			coalesce(sum(cache_read_tokens), 0),
			coalesce(sum(cache_creation_tokens), 0),
			coalesce(sum(case when input_tokens > 272000 then input_tokens else 0 end), 0),
			coalesce(sum(case when input_tokens > 272000 then output_tokens else 0 end), 0),
			coalesce(sum(case when input_tokens > 272000 then greatest(greatest(cached_tokens, cache_tokens) - greatest(cache_read_tokens, 0) - greatest(cache_creation_tokens, 0), 0) else 0 end), 0),
			coalesce(sum(case when input_tokens > 272000 then cache_read_tokens else 0 end), 0),
			coalesce(sum(case when input_tokens > 272000 then cache_creation_tokens else 0 end), 0),
			coalesce(sum(total_tokens), 0),
			min(timestamp_ms),
			max(timestamp_ms),
			max(created_at_ms)
		from filtered
		where account_key = any($1)
		group by account_key, model, billing_model, service_tier
		order by account_key, max(timestamp_ms) desc`, keys)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL account history: %w", err)
	}
	defer rows.Close()

	result := make([]store.AccountHistoryRollupRow, 0)
	for rows.Next() {
		var row store.AccountHistoryRollupRow
		if err := rows.Scan(
			&row.AccountKey,
			&row.AccountSnapshot,
			&row.AuthLabelSnapshot,
			&row.AuthProviderSnapshot,
			&row.AuthIndex,
			&row.Source,
			&row.SourceHash,
			&row.Model,
			&row.BillingModel,
			&row.ServiceTier,
			&row.Calls,
			&row.SuccessCalls,
			&row.FailureCalls,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.CacheReadTokens,
			&row.CacheCreationTokens,
			&row.LongInputTokens,
			&row.LongOutputTokens,
			&row.LongCachedTokens,
			&row.LongCacheReadTokens,
			&row.LongCacheCreationTokens,
			&row.TotalTokens,
			&row.FirstSeenMS,
			&row.LastSeenMS,
			&row.UpdatedAtMS,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL account history: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL account history: %w", err)
	}
	return result, nil
}

var postgresAnalyticsSearchColumns = []string{
	"request_id",
	"event_hash",
	"model",
	"resolved_model",
	"endpoint",
	"method",
	"path",
	"source",
	"source_hash",
	"api_key_hash",
	"auth_index",
	"account_snapshot",
	"auth_label_snapshot",
	"auth_file_snapshot",
	"auth_provider_snapshot",
	"auth_project_id_snapshot",
	"reasoning_effort",
	"service_tier",
	"executor_type",
	"fail_summary",
	"header_quota_plan_type",
	"header_error_kind",
	"header_error_code",
	"header_trace_id",
}

const postgresCredentialIDExpr = "coalesce(nullif(auth_file_snapshot, ''), nullif(auth_index, ''), nullif(source_hash, ''), nullif(source, ''), '-')"

func postgresAnalyticsWhere(filter store.AnalyticsFilter, next int) (string, []any, int) {
	conditions := []string{
		fmt.Sprintf("timestamp_ms >= $%d", next),
		fmt.Sprintf("timestamp_ms < $%d", next+1),
	}
	args := []any{filter.FromMS, filter.ToMS}
	next += 2

	query := strings.ToLower(strings.TrimSpace(filter.SearchQuery))
	hash := strings.ToLower(strings.TrimSpace(filter.SearchAPIKeyHash))
	if query != "" {
		like := "%" + query + "%"
		searchConditions := make([]string, 0, len(postgresAnalyticsSearchColumns)+1)
		placeholder := fmt.Sprintf("$%d", next)
		for _, column := range postgresAnalyticsSearchColumns {
			searchConditions = append(searchConditions, fmt.Sprintf("lower(coalesce(%s, '')) like %s", column, placeholder))
		}
		args = append(args, like)
		next++
		if hash != "" {
			hashPlaceholder := fmt.Sprintf("$%d", next)
			searchConditions = append(searchConditions, fmt.Sprintf("lower(coalesce(api_key_hash, '')) = %s", hashPlaceholder))
			args = append(args, hash)
			next++
		}
		conditions = append(conditions, "("+strings.Join(searchConditions, " or ")+")")
	} else if hash != "" {
		conditions = append(conditions, fmt.Sprintf("lower(coalesce(api_key_hash, '')) = $%d", next))
		args = append(args, hash)
		next++
	}

	addAny := func(expression string, values []string, lower bool) {
		normalized := normalizePGValues(values)
		if len(normalized) == 0 {
			return
		}
		if lower {
			for index := range normalized {
				normalized[index] = strings.ToLower(normalized[index])
			}
		}
		conditions = append(conditions, fmt.Sprintf("%s = any($%d)", expression, next))
		args = append(args, normalized)
		next++
	}
	addAny("model", filter.Models, false)
	providers := normalizePGValues(filter.Providers)
	if len(providers) > 0 {
		for index := range providers {
			providers[index] = strings.ToLower(providers[index])
		}
		placeholder := fmt.Sprintf("$%d", next)
		conditions = append(conditions, fmt.Sprintf("(lower(coalesce(provider, '')) = any(%s) or lower(coalesce(auth_provider_snapshot, '')) = any(%s))", placeholder, placeholder))
		args = append(args, providers)
		next++
	}
	accounts := normalizePGValues(filter.Accounts)
	if len(accounts) > 0 {
		for index := range accounts {
			accounts[index] = strings.ToLower(accounts[index])
		}
		placeholder := fmt.Sprintf("$%d", next)
		conditions = append(conditions, fmt.Sprintf("(lower(coalesce(account_snapshot, '')) = any(%s) or lower(coalesce(auth_label_snapshot, '')) = any(%s) or lower(coalesce(source, '')) = any(%s) or lower(coalesce(auth_index, '')) = any(%s))", placeholder, placeholder, placeholder, placeholder))
		args = append(args, accounts)
		next++
	}
	addAny(postgresCredentialIDExpr, filter.CredentialIDs, false)
	addAny("coalesce(auth_file_snapshot, '')", filter.AuthFiles, false)
	addAny("coalesce(auth_index, '')", filter.AuthIndices, false)
	addAny("coalesce(api_key_hash, '')", filter.APIKeyHashes, false)
	addAny("coalesce(source_hash, '')", filter.SourceHashes, false)
	addAny("coalesce(auth_project_id_snapshot, '')", filter.ProjectIDs, false)
	addAny("coalesce(executor_type, '')", filter.RequestTypes, false)
	addAny("coalesce(header_error_kind, '')", filter.HeaderErrorKinds, false)
	addAny("coalesce(header_error_code, '')", filter.HeaderErrorCodes, false)
	addAny("coalesce(header_quota_plan_type, '')", filter.HeaderQuotaPlans, false)
	addAny("coalesce(header_trace_id, '')", filter.HeaderTraceIDs, false)
	if !filter.IncludeFailed {
		conditions = append(conditions, "failed = false")
	}
	if filter.FailedOnly {
		conditions = append(conditions, "failed = true")
	}
	if filter.MinLatencyMS > 0 {
		conditions = append(conditions, fmt.Sprintf("latency_ms >= $%d", next))
		args = append(args, filter.MinLatencyMS)
		next++
	}
	cacheHitCondition := "(coalesce(cached_tokens, 0) > 0 or coalesce(cache_tokens, 0) > 0 or coalesce(cache_read_tokens, 0) > 0 or coalesce(cache_creation_tokens, 0) > 0)"
	switch strings.ToLower(strings.TrimSpace(filter.CacheStatus)) {
	case "hit":
		conditions = append(conditions, cacheHitCondition)
	case "miss":
		conditions = append(conditions, "not "+cacheHitCondition)
	case "read":
		conditions = append(conditions, "coalesce(cache_read_tokens, 0) > 0")
	case "creation":
		conditions = append(conditions, "coalesce(cache_creation_tokens, 0) > 0")
	}
	return "where " + strings.Join(conditions, " and "), args, next
}

func (p *Postgres) EventsCountWithFilter(ctx context.Context, filter store.AnalyticsFilter) (int64, error) {
	if p == nil || p.Pool == nil {
		return 0, nil
	}
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	var total int64
	if err := p.Pool.QueryRow(ctx, `select count(*) from cpamp_usage.usage_events `+where, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count PostgreSQL usage events: %w", err)
	}
	return total, nil
}

func (p *Postgres) EventsPageWithFilter(ctx context.Context, filter store.AnalyticsFilter, beforeMS int64, beforeID int64, limit int) (store.EventsPage, error) {
	if p == nil || p.Pool == nil || limit <= 0 {
		return store.EventsPage{}, nil
	}
	where, args, next := postgresAnalyticsWhere(filter, 1)
	if beforeMS > 0 {
		if beforeID > 0 {
			where += fmt.Sprintf(" and (timestamp_ms < $%d or (timestamp_ms = $%d and id < $%d))", next, next, next+1)
			args = append(args, beforeMS, beforeID)
			next += 2
		} else {
			where += fmt.Sprintf(" and timestamp_ms < $%d", next)
			args = append(args, beforeMS)
			next++
		}
	}
	queryLimit := limit + 1
	query := fmt.Sprintf(`select
		id,
		coalesce(request_id, ''),
		event_hash,
		timestamp_ms,
		timestamp,
		model,
		coalesce(resolved_model, ''),
		coalesce(endpoint, ''),
		coalesce(method, ''),
		coalesce(path, ''),
		coalesce(auth_index, ''),
		coalesce(source, ''),
		coalesce(source_hash, ''),
		coalesce(api_key_hash, ''),
		coalesce(account_snapshot, ''),
		coalesce(auth_label_snapshot, ''),
		coalesce(auth_file_snapshot, ''),
		coalesce(nullif(auth_provider_snapshot, ''), provider, ''),
		coalesce(auth_project_id_snapshot, ''),
		coalesce(reasoning_effort, ''),
		coalesce(service_tier, ''),
		coalesce(executor_type, ''),
		coalesce(normalized_total_input, input_tokens, 0),
		coalesce(output_tokens, 0),
		greatest(greatest(coalesce(cached_tokens, 0), coalesce(cache_tokens, 0)) - greatest(coalesce(cache_read_tokens, 0), 0) - greatest(coalesce(cache_creation_tokens, 0), 0), 0),
		coalesce(cache_read_tokens, 0),
		coalesce(cache_creation_tokens, 0),
		coalesce(reasoning_tokens, 0),
		coalesce(total_tokens, 0),
		latency_ms,
		ttft_ms,
		failed,
		fail_status_code,
		coalesce(fail_summary, ''),
		coalesce(response_metadata_json, '{}'::jsonb),
		header_quota_recover_at_ms,
		header_quota_used_percent,
		coalesce(header_quota_plan_type, ''),
		coalesce(header_error_kind, ''),
		coalesce(header_error_code, ''),
		coalesce(header_trace_id, '')
	from cpamp_usage.usage_events %s
	order by timestamp_ms desc, id desc
	limit $%d`, where, next)
	args = append(args, queryLimit)
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return store.EventsPage{}, fmt.Errorf("read PostgreSQL usage event page: %w", err)
	}
	defer rows.Close()
	items := make([]store.EventPageItem, 0, limit)
	for rows.Next() {
		var item store.EventPageItem
		var responseMetadataJSON []byte
		if err := rows.Scan(
			&item.ID, &item.RequestID, &item.EventHash, &item.TimestampMS, &item.Timestamp,
			&item.Model, &item.ResolvedModel, &item.Endpoint, &item.Method, &item.Path,
			&item.AuthIndex, &item.Source, &item.SourceHash, &item.APIKeyHash,
			&item.AccountSnapshot, &item.AuthLabelSnapshot, &item.AuthFileSnapshot,
			&item.AuthProviderSnapshot, &item.AuthProjectIDSnapshot, &item.ReasoningEffort,
			&item.ServiceTier, &item.ExecutorType, &item.InputTokens, &item.OutputTokens,
			&item.CachedTokens, &item.CacheReadTokens, &item.CacheCreationTokens,
			&item.ReasoningTokens, &item.TotalTokens, &item.LatencyMS, &item.TTFTMS,
			&item.Failed, &item.FailStatusCode, &item.FailSummary, &responseMetadataJSON,
			&item.HeaderQuotaRecoverAtMS, &item.HeaderQuotaUsedPercent, &item.HeaderQuotaPlanType,
			&item.HeaderErrorKind, &item.HeaderErrorCode, &item.HeaderTraceID,
		); err != nil {
			return store.EventsPage{}, fmt.Errorf("scan PostgreSQL usage event page: %w", err)
		}
		item.ResponseMetadata = usage.ResponseHeaderMetadataFromJSON(string(responseMetadataJSON))
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return store.EventsPage{}, fmt.Errorf("iterate PostgreSQL usage event page: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	page := store.EventsPage{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.NextBeforeMS = last.TimestampMS
		page.NextBeforeID = last.ID
	}
	return page, nil
}

func (p *Postgres) RecentFailuresWithFilter(ctx context.Context, filter store.AnalyticsFilter, limit int) ([]store.RecentFailure, error) {
	if p == nil || p.Pool == nil || limit <= 0 {
		return []store.RecentFailure{}, nil
	}
	filter.IncludeFailed = true
	where, args, next := postgresAnalyticsWhere(filter, 1)
	where += " and failed = true"
	query := fmt.Sprintf(`select
		timestamp_ms, model, coalesce(api_key_hash, ''), coalesce(source, ''), coalesce(source_hash, ''),
		coalesce(auth_index, ''), coalesce(endpoint, ''), latency_ms,
		coalesce(account_snapshot, ''), coalesce(auth_label_snapshot, ''),
		coalesce(nullif(auth_provider_snapshot, ''), provider, ''), coalesce(auth_project_id_snapshot, ''),
		fail_status_code, coalesce(fail_summary, ''), coalesce(response_metadata_json, '{}'::jsonb),
		header_quota_recover_at_ms, header_quota_used_percent,
		coalesce(header_quota_plan_type, ''), coalesce(header_error_kind, ''),
		coalesce(header_error_code, ''), coalesce(header_trace_id, '')
	from cpamp_usage.usage_events %s
	order by timestamp_ms desc, id desc
	limit $%d`, where, next)
	args = append(args, limit)
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL recent failures: %w", err)
	}
	defer rows.Close()
	failures := make([]store.RecentFailure, 0, limit)
	for rows.Next() {
		var failure store.RecentFailure
		var responseMetadataJSON []byte
		if err := rows.Scan(
			&failure.TimestampMS, &failure.Model, &failure.APIKeyHash, &failure.Source,
			&failure.SourceHash, &failure.AuthIndex, &failure.Endpoint, &failure.LatencyMS,
			&failure.AccountSnapshot, &failure.AuthLabelSnapshot, &failure.AuthProviderSnapshot,
			&failure.AuthProjectIDSnapshot, &failure.FailStatusCode, &failure.FailSummary,
			&responseMetadataJSON, &failure.HeaderQuotaRecoverAtMS, &failure.HeaderQuotaUsedPercent,
			&failure.HeaderQuotaPlanType, &failure.HeaderErrorKind, &failure.HeaderErrorCode,
			&failure.HeaderTraceID,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL recent failure: %w", err)
		}
		failure.ResponseMetadata = usage.ResponseHeaderMetadataFromJSON(string(responseMetadataJSON))
		failures = append(failures, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL recent failures: %w", err)
	}
	return failures, nil
}
