package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	rollupHourlyVersion = "aggregate_schema_version"
	rollupDailyVersion  = "daily_aggregate_schema_version"
	rollupHourMS        = int64(time.Hour / time.Millisecond)
	rollupDayMS         = int64(24 * time.Hour / time.Millisecond)
	longInputThreshold  = int64(272000)
)

type RollupOptions struct {
	Task         string
	BatchSize    int
	MaxBatches   int
	Follow       bool
	PollInterval time.Duration
}

func (p *Postgres) Rollup(ctx context.Context, options RollupOptions) (MigrationState, error) {
	if p == nil || p.Pool == nil {
		return MigrationState{}, errors.New("postgres is not open")
	}
	if options.Task != HourlyRollupTaskName && options.Task != DailyRollupTaskName {
		return MigrationState{}, fmt.Errorf("unsupported rollup task %q", options.Task)
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 500
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.MaxBatches <= 0 && !options.Follow {
		options.MaxBatches = 1
	}
	lease, err := p.AcquireTaskLease(ctx, options.Task)
	if err != nil {
		return MigrationState{}, err
	}
	defer lease()

	state, err := p.State(ctx, options.Task)
	if err != nil {
		return MigrationState{}, err
	}
	for batchNo := 0; options.MaxBatches == 0 || batchNo < options.MaxBatches; {
		if state.PauseRequested {
			return p.MarkTaskPaused(ctx, options.Task)
		}
		ids, lastHash, err := p.rollupEventIDs(ctx, state.LastCopiedID, options.BatchSize)
		if err != nil {
			_ = p.recordTaskError(ctx, options.Task, err)
			return state, err
		}
		if len(ids) == 0 {
			if !options.Follow {
				if _, err := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
					set phase = 'PHASE_5_VERIFY', status = 'COMPLETED', last_error = null, updated_at = now()
					where migration_name = $1`, options.Task); err != nil {
					return state, err
				}
				return p.State(ctx, options.Task)
			}
			if err := waitForRollupPoll(ctx, options.PollInterval); err != nil {
				return state, nil
			}
			state, err = p.State(ctx, options.Task)
			if err != nil {
				return state, err
			}
			continue
		}

		afterID := state.LastCopiedID
		throughID := ids[len(ids)-1]
		nowMS := time.Now().UnixMilli()
		tx, err := p.Pool.Begin(ctx)
		if err != nil {
			_ = p.recordTaskError(ctx, options.Task, err)
			return state, fmt.Errorf("begin %s rollup transaction: %w", options.Task, err)
		}
		if err := ensureRollupLedger(ctx, tx, afterID, throughID, nowMS); err != nil {
			_ = tx.Rollback(ctx)
			_ = p.recordTaskError(ctx, options.Task, err)
			return state, err
		}
		if err := upsertPostgresRollup(ctx, tx, options.Task, afterID, throughID, nowMS); err != nil {
			_ = tx.Rollback(ctx)
			_ = p.recordTaskError(ctx, options.Task, err)
			return state, err
		}
		versionColumn := rollupHourlyVersion
		if options.Task == DailyRollupTaskName {
			versionColumn = rollupDailyVersion
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`update cpamp_usage.usage_event_identity_ledger
			set %s = 1, updated_at_ms = $3
			where raw_event_id > $1 and raw_event_id <= $2 and %s < 1`, versionColumn, versionColumn), afterID, throughID, nowMS); err != nil {
			_ = tx.Rollback(ctx)
			_ = p.recordTaskError(ctx, options.Task, err)
			return state, fmt.Errorf("mark %s aggregate identity: %w", options.Task, err)
		}
		checkpoint := map[string]interface{}{
			"raw_event_id_after":   afterID,
			"raw_event_id_through": throughID,
			"last_event_hash":      lastHash,
			"aggregate":            options.Task,
			"schema_version":       1,
		}
		if _, err := tx.Exec(ctx, `update cpamp_usage.cpamp_migration_state
			set phase = 'PHASE_5_VERIFY', status = 'RUNNING',
				last_copied_id = $2, last_copied_hash = $3,
				processed_rows = processed_rows + $4,
				last_error = null, checkpoint = $5::jsonb, updated_at = now()
			where migration_name = $1`, options.Task, throughID, lastHash,
			int64(len(ids)), checkpointJSON(checkpoint)); err != nil {
			_ = tx.Rollback(ctx)
			_ = p.recordTaskError(ctx, options.Task, err)
			return state, fmt.Errorf("advance %s rollup checkpoint: %w", options.Task, err)
		}
		if err := tx.Commit(ctx); err != nil {
			_ = p.recordTaskError(ctx, options.Task, err)
			return state, fmt.Errorf("commit %s rollup transaction: %w", options.Task, err)
		}
		state, err = p.State(ctx, options.Task)
		if err != nil {
			return state, err
		}
		batchNo++
	}
	return state, nil
}

func (p *Postgres) rollupEventIDs(ctx context.Context, afterID int64, limit int) ([]int64, string, error) {
	rows, err := p.Pool.Query(ctx, `select id, event_hash
		from cpamp_usage.usage_events
		where id > $1 order by id asc limit $2`, afterID, limit)
	if err != nil {
		return nil, "", fmt.Errorf("read postgres rollup events after %d: %w", afterID, err)
	}
	defer rows.Close()
	ids := make([]int64, 0, limit)
	lastHash := ""
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id, &lastHash); err != nil {
			return nil, "", fmt.Errorf("scan postgres rollup event: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate postgres rollup events: %w", err)
	}
	return ids, lastHash, nil
}

func ensureRollupLedger(ctx context.Context, tx pgx.Tx, afterID, throughID, nowMS int64) error {
	_, err := tx.Exec(ctx, `insert into cpamp_usage.usage_event_identity_ledger
		(event_hash, raw_event_id, timestamp_ms, bucket_ms, aggregate_schema_version,
		 daily_aggregate_schema_version, first_seen_at_ms, updated_at_ms)
		select event_hash, id, timestamp_ms,
		       timestamp_ms - (timestamp_ms % 3600000),
		       0, 0, created_at_ms, $3
		from cpamp_usage.usage_events
		where id > $1 and id <= $2
		on conflict (event_hash) do nothing`, afterID, throughID, nowMS)
	if err != nil {
		return fmt.Errorf("ensure rollup identity ledger: %w", err)
	}
	return nil
}

func upsertPostgresRollup(ctx context.Context, tx pgx.Tx, task string, afterID, throughID, nowMS int64) error {
	tableName := "cpamp_usage.usage_hourly_aggregate_v1"
	bucketColumn := "bucket_ms"
	bucketMS := rollupHourMS
	versionColumn := rollupHourlyVersion
	if task == DailyRollupTaskName {
		tableName = "cpamp_usage.usage_daily_aggregate_v1"
		bucketColumn = "day_bucket_ms"
		bucketMS = rollupDayMS
		versionColumn = rollupDailyVersion
	}
	statement := fmt.Sprintf(`insert into %s (
		%s, model, billing_model, service_tier, failed, calls,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens,
		cache_read_tokens, cache_creation_tokens, long_input_tokens,
		long_output_tokens, long_cached_tokens, long_cache_read_tokens,
		long_cache_creation_tokens, total_tokens, latency_sum_ms,
		latency_samples, zero_token_calls
	)
	select
		e.timestamp_ms - (e.timestamp_ms %% %d),
		e.model,
		coalesce(nullif(e.resolved_model, ''), e.model),
		coalesce(e.service_tier, ''),
		e.failed,
		count(*),
		coalesce(sum(coalesce(e.normalized_total_input, e.input_tokens, 0)), 0),
		coalesce(sum(e.output_tokens), 0),
		coalesce(sum(e.reasoning_tokens), 0),
		coalesce(sum(greatest(greatest(e.cached_tokens, e.cache_tokens) - greatest(e.cache_read_tokens, 0) - greatest(e.cache_creation_tokens, 0), 0)), 0),
		coalesce(sum(e.cache_read_tokens), 0),
		coalesce(sum(e.cache_creation_tokens), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input, e.input_tokens, 0) > %d then coalesce(e.normalized_total_input, e.input_tokens, 0) else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input, e.input_tokens, 0) > %d then e.output_tokens else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input, e.input_tokens, 0) > %d then greatest(greatest(e.cached_tokens, e.cache_tokens) - greatest(e.cache_read_tokens, 0) - greatest(e.cache_creation_tokens, 0), 0) else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input, e.input_tokens, 0) > %d then e.cache_read_tokens else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input, e.input_tokens, 0) > %d then e.cache_creation_tokens else 0 end), 0),
		coalesce(sum(e.total_tokens), 0),
		coalesce(sum(case when e.latency_ms is not null and e.latency_ms <> 0 then e.latency_ms else 0 end), 0),
		count(nullif(e.latency_ms, 0)),
		coalesce(sum(case when e.total_tokens = 0 and not e.failed then 1 else 0 end), 0)
	from cpamp_usage.usage_events e
	join cpamp_usage.usage_event_identity_ledger ledger on ledger.event_hash = e.event_hash
	where e.id > $1 and e.id <= $2 and ledger.%s < 1
	group by e.timestamp_ms - (e.timestamp_ms %% %d), e.model,
		coalesce(nullif(e.resolved_model, ''), e.model), coalesce(e.service_tier, ''), e.failed
	on conflict (%s, model, billing_model, service_tier, failed) do update set
		calls = %s.calls + excluded.calls,
		input_tokens = %s.input_tokens + excluded.input_tokens,
		output_tokens = %s.output_tokens + excluded.output_tokens,
		reasoning_tokens = %s.reasoning_tokens + excluded.reasoning_tokens,
		cached_tokens = %s.cached_tokens + excluded.cached_tokens,
		cache_read_tokens = %s.cache_read_tokens + excluded.cache_read_tokens,
		cache_creation_tokens = %s.cache_creation_tokens + excluded.cache_creation_tokens,
		long_input_tokens = %s.long_input_tokens + excluded.long_input_tokens,
		long_output_tokens = %s.long_output_tokens + excluded.long_output_tokens,
		long_cached_tokens = %s.long_cached_tokens + excluded.long_cached_tokens,
		long_cache_read_tokens = %s.long_cache_read_tokens + excluded.long_cache_read_tokens,
		long_cache_creation_tokens = %s.long_cache_creation_tokens + excluded.long_cache_creation_tokens,
		total_tokens = %s.total_tokens + excluded.total_tokens,
		latency_sum_ms = %s.latency_sum_ms + excluded.latency_sum_ms,
		latency_samples = %s.latency_samples + excluded.latency_samples,
		zero_token_calls = %s.zero_token_calls + excluded.zero_token_calls`,
		tableName, bucketColumn, bucketMS, longInputThreshold, longInputThreshold, longInputThreshold,
		longInputThreshold, longInputThreshold, versionColumn, bucketMS,
		bucketColumn,
		tableName, tableName, tableName, tableName, tableName, tableName,
		tableName, tableName, tableName, tableName, tableName, tableName,
		tableName, tableName, tableName, tableName)
	if _, err := tx.Exec(ctx, statement, afterID, throughID); err != nil {
		return fmt.Errorf("upsert %s aggregate: %w", task, err)
	}
	return nil
}

func waitForRollupPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
