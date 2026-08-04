package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

const (
	migrationName         = "cpamp-usage-pg-redis-v1"
	backfillTaskName      = "backfill.events"
	verifyTaskName        = "verify.event_counts"
	defaultMigrationBatch = 500
)

type MigrationState struct {
	MigrationName    string
	Phase            string
	Status           string
	SourceHWMID      int64
	SourceHWMHash    string
	LastCopiedID     int64
	LastCopiedHash   string
	LastStreamID     string
	LastVerifiedID   int64
	LastVerifiedHash string
	ProcessedRows    int64
	InsertedRows     int64
	SkippedRows      int64
	MismatchRows     int64
	PauseRequested   bool
	LastError        string
	Checkpoint       json.RawMessage
	UpdatedAt        time.Time
}

type Postgres struct {
	Pool             *pgxpool.Pool
	monitoringFlight singleflight.Group
}

var migrationTaskNames = []string{
	migrationName,
	backfillTaskName,
	verifyTaskName,
	RelayTaskName,
	PersistTaskName,
	HourlyRollupTaskName,
	DailyRollupTaskName,
	"cache.warm",
	"cutover.monitoring",
}

// AcquireTaskLease prevents two independently started migration workers from
// advancing the same checkpoint at the same time. The advisory lock is held
// on a dedicated pool connection for the full worker lifetime.
func (p *Postgres) AcquireTaskLease(ctx context.Context, task string) (func(), error) {
	if p == nil || p.Pool == nil {
		return nil, errors.New("postgres is not open")
	}
	conn, err := p.Pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire task lease connection %s: %w", task, err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `select pg_try_advisory_lock(hashtext($1))`, task).Scan(&acquired); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire task lease %s: %w", task, err)
	}
	if !acquired {
		conn.Release()
		return nil, fmt.Errorf("task lease is already held: %s", task)
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `select pg_advisory_unlock(hashtext($1))`, task)
		conn.Release()
	}, nil
}

func (p *Postgres) MarkTaskPaused(ctx context.Context, task string) (MigrationState, error) {
	if _, err := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
		set phase = 'PHASE_4_DUAL_RELAY', status = 'PAUSED', updated_at = now()
		where migration_name = $1`, task); err != nil {
		return MigrationState{}, fmt.Errorf("mark task %s paused: %w", task, err)
	}
	return p.State(ctx, task)
}

func (p *Postgres) MarkRedisReady(ctx context.Context, stream, group string) error {
	checkpoint := checkpointJSON(map[string]string{
		"stream": stream,
		"group":  group,
		"mode":   "redis_stream_dual_relay",
	})
	_, err := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
		set phase = 'PHASE_4_DUAL_RELAY', status = 'READY',
			last_error = null, checkpoint = $1::jsonb, updated_at = now()
		where migration_name in ($2, $3)`, checkpoint, RelayTaskName, PersistTaskName)
	return err
}

func checkpointJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}
	return payload
}

func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres DSN is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	config.MinConns = 1
	config.MaxConns = 8
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Postgres{Pool: pool}, nil
}

func (p *Postgres) Close() {
	if p != nil && p.Pool != nil {
		p.Pool.Close()
	}
}

// Initialize creates the PostgreSQL usage schema and the worker checkpoints
// required by a fresh Redis -> PostgreSQL deployment. Unlike Prepare it does
// not require a legacy SQLite database and is safe to run on every startup.
func (p *Postgres) Initialize(ctx context.Context) error {
	if p == nil || p.Pool == nil {
		return errors.New("postgres is not open")
	}
	for _, statement := range schemaStatements {
		if _, err := p.Pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("initialize postgres usage schema: %w", err)
		}
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin postgres usage initialization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, task := range migrationTaskNames {
		if _, err := tx.Exec(ctx, `insert into cpamp_usage.cpamp_migration_state
			(migration_name, phase, status, updated_at)
			values ($1, 'PHASE_1_CODE_PREPARE', 'PREPARED', now())
			on conflict (migration_name) do nothing`, task); err != nil {
			return fmt.Errorf("initialize migration task %s: %w", task, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit postgres usage initialization: %w", err)
	}
	return nil
}

func (p *Postgres) Prepare(ctx context.Context, source *SQLiteSource) (MigrationState, error) {
	if p == nil || p.Pool == nil {
		return MigrationState{}, errors.New("postgres is not open")
	}
	if source == nil {
		return MigrationState{}, errors.New("sqlite source is required")
	}
	if err := p.Initialize(ctx); err != nil {
		return MigrationState{}, err
	}
	stats, err := source.Stats(ctx, 0)
	if err != nil {
		return MigrationState{}, err
	}
	hwmHash, err := source.EventHashAt(ctx, stats.MaxID)
	if err != nil {
		return MigrationState{}, err
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return MigrationState{}, fmt.Errorf("begin prepare state: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `update cpamp_usage.cpamp_migration_state
		set phase = case when status in ('PREPARED', 'PAUSED') then 'PHASE_3_BACKFILL' else phase end,
		    source_hwm_id = case when source_hwm_id = 0 then $2 else source_hwm_id end,
		    source_hwm_hash = case when source_hwm_id = 0 then $3 else source_hwm_hash end,
		    updated_at = now()
		where migration_name = $1`, backfillTaskName, stats.MaxID, hwmHash); err != nil {
		return MigrationState{}, fmt.Errorf("set source high-water: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MigrationState{}, fmt.Errorf("commit prepare state: %w", err)
	}
	return p.State(ctx, backfillTaskName)
}

func (p *Postgres) State(ctx context.Context, task string) (MigrationState, error) {
	var state MigrationState
	err := p.Pool.QueryRow(ctx, `select
		migration_name, phase, status, source_hwm_id, coalesce(source_hwm_hash, ''),
		last_copied_id, coalesce(last_copied_hash, ''), coalesce(last_stream_id, ''), last_verified_id,
		coalesce(last_verified_hash, ''), processed_rows, inserted_rows, skipped_rows,
		mismatch_rows, pause_requested, coalesce(last_error, ''), checkpoint, updated_at
	from cpamp_usage.cpamp_migration_state
	where migration_name = $1`, task).Scan(
		&state.MigrationName, &state.Phase, &state.Status, &state.SourceHWMID,
		&state.SourceHWMHash, &state.LastCopiedID, &state.LastCopiedHash,
		&state.LastStreamID,
		&state.LastVerifiedID, &state.LastVerifiedHash, &state.ProcessedRows,
		&state.InsertedRows, &state.SkippedRows, &state.MismatchRows,
		&state.PauseRequested, &state.LastError, &state.Checkpoint, &state.UpdatedAt,
	)
	if err != nil {
		return MigrationState{}, fmt.Errorf("read migration state %s: %w", task, err)
	}
	return state, nil
}

func (p *Postgres) States(ctx context.Context) ([]MigrationState, error) {
	rows, err := p.Pool.Query(ctx, `select
		migration_name, phase, status, source_hwm_id, coalesce(source_hwm_hash, ''),
		last_copied_id, coalesce(last_copied_hash, ''), coalesce(last_stream_id, ''), last_verified_id,
		coalesce(last_verified_hash, ''), processed_rows, inserted_rows, skipped_rows,
		mismatch_rows, pause_requested, coalesce(last_error, ''), checkpoint, updated_at
	from cpamp_usage.cpamp_migration_state order by migration_name`)
	if err != nil {
		return nil, fmt.Errorf("list migration state: %w", err)
	}
	defer rows.Close()
	states := make([]MigrationState, 0)
	for rows.Next() {
		var state MigrationState
		if err := rows.Scan(
			&state.MigrationName, &state.Phase, &state.Status, &state.SourceHWMID,
			&state.SourceHWMHash, &state.LastCopiedID, &state.LastCopiedHash,
			&state.LastStreamID,
			&state.LastVerifiedID, &state.LastVerifiedHash, &state.ProcessedRows,
			&state.InsertedRows, &state.SkippedRows, &state.MismatchRows,
			&state.PauseRequested, &state.LastError, &state.Checkpoint, &state.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan migration state: %w", err)
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

func (p *Postgres) SetPause(ctx context.Context, pause bool) error {
	_, err := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
		set pause_requested = $1,
		    status = case
				when $1 and status not in ('COMPLETED', 'FAILED') then 'PAUSING'
				when not $1 and status in ('PAUSED', 'PAUSING', 'PREPARED') then 'RUNNING'
				else status
			end,
		    updated_at = now()
		where migration_name <> ''`, pause)
	return err
}

func (p *Postgres) Backfill(ctx context.Context, source *SQLiteSource, batchSize, maxBatches int) (MigrationState, error) {
	if batchSize <= 0 {
		batchSize = defaultMigrationBatch
	}
	if maxBatches <= 0 {
		maxBatches = 1
	}
	state, err := p.State(ctx, backfillTaskName)
	if err != nil {
		return MigrationState{}, err
	}
	if state.SourceHWMID <= 0 {
		return MigrationState{}, errors.New("backfill source high-water is not prepared")
	}
	if state.Status == "COMPLETED" && state.LastCopiedID >= state.SourceHWMID {
		return state, nil
	}

	for batchNo := 0; batchNo < maxBatches; batchNo++ {
		state, err = p.State(ctx, backfillTaskName)
		if err != nil {
			return state, err
		}
		if state.PauseRequested {
			_, _ = p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
				set status = 'PAUSED', updated_at = now() where migration_name = $1`, backfillTaskName)
			return p.State(ctx, backfillTaskName)
		}
		events, err := source.Batch(ctx, state.LastCopiedID, state.SourceHWMID, batchSize)
		if err != nil {
			_ = p.recordTaskError(ctx, backfillTaskName, err)
			return state, err
		}
		if len(events) == 0 {
			if _, err := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
				set phase = 'PHASE_3_BACKFILL', status = 'COMPLETED',
				    last_copied_id = source_hwm_id, last_copied_hash = source_hwm_hash,
				    updated_at = now(), last_error = null
				where migration_name = $1`, backfillTaskName); err != nil {
				return state, err
			}
			return p.State(ctx, backfillTaskName)
		}

		tx, err := p.Pool.Begin(ctx)
		if err != nil {
			_ = p.recordTaskError(ctx, backfillTaskName, err)
			return state, fmt.Errorf("begin backfill batch: %w", err)
		}
		inserted := int64(0)
		skipped := int64(0)
		for _, event := range events {
			tag, execErr := tx.Exec(ctx, insertEventSQL, eventArgs(event)...)
			if execErr != nil {
				_ = tx.Rollback(ctx)
				_ = p.recordTaskError(ctx, backfillTaskName, execErr)
				return state, fmt.Errorf("insert event %d hash %s: %w", event.ID, event.EventHash, execErr)
			}
			if tag.RowsAffected() == 0 {
				skipped++
			} else {
				inserted++
			}
			if _, execErr := tx.Exec(ctx, `insert into cpamp_usage.usage_event_identity_ledger
				(event_hash, raw_event_id, timestamp_ms, bucket_ms, aggregate_schema_version,
				 daily_aggregate_schema_version, first_seen_at_ms, updated_at_ms)
				select event_hash, id, timestamp_ms,
				       timestamp_ms - (timestamp_ms % 3600000),
				       0, 0,
				       created_at_ms, created_at_ms
				from cpamp_usage.usage_events
				where event_hash = $1
				on conflict (event_hash) do update set
				    raw_event_id = excluded.raw_event_id,
				    timestamp_ms = excluded.timestamp_ms,
				    bucket_ms = excluded.bucket_ms,
				    aggregate_schema_version = least(cpamp_usage.usage_event_identity_ledger.aggregate_schema_version, 0),
				    daily_aggregate_schema_version = least(cpamp_usage.usage_event_identity_ledger.daily_aggregate_schema_version, 0),
				    updated_at_ms = excluded.updated_at_ms`, event.EventHash); execErr != nil {
				_ = tx.Rollback(ctx)
				_ = p.recordTaskError(ctx, backfillTaskName, execErr)
				return state, fmt.Errorf("insert identity ledger %s: %w", event.EventHash, execErr)
			}
		}
		last := events[len(events)-1]
		nextStatus := "RUNNING"
		if last.ID >= state.SourceHWMID {
			nextStatus = "COMPLETED"
		}
		if _, err := tx.Exec(ctx, `update cpamp_usage.cpamp_migration_state
			set phase = 'PHASE_3_BACKFILL',
			    status = $2,
			    last_copied_id = $3,
			    last_copied_hash = $4,
			    processed_rows = processed_rows + $5,
			    inserted_rows = inserted_rows + $6,
			    skipped_rows = skipped_rows + $7,
			    last_error = null,
			    updated_at = now()
			where migration_name = $1`, backfillTaskName, nextStatus, last.ID, last.EventHash,
			int64(len(events)), inserted, skipped); err != nil {
			_ = tx.Rollback(ctx)
			_ = p.recordTaskError(ctx, backfillTaskName, err)
			return state, fmt.Errorf("advance backfill checkpoint: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			_ = p.recordTaskError(ctx, backfillTaskName, err)
			return state, fmt.Errorf("commit backfill batch: %w", err)
		}
	}
	return p.State(ctx, backfillTaskName)
}

func (p *Postgres) Verify(ctx context.Context, source *SQLiteSource) (MigrationState, error) {
	state, err := p.State(ctx, backfillTaskName)
	if err != nil {
		return MigrationState{}, err
	}
	if state.SourceHWMID <= 0 {
		return MigrationState{}, errors.New("verify source high-water is not prepared")
	}
	sourceStats, err := source.Stats(ctx, state.SourceHWMID)
	if err != nil {
		return state, err
	}
	var targetCount, targetDistinct, targetMaxID int64
	if err := p.Pool.QueryRow(ctx, `select
		count(*), count(distinct event_hash), coalesce(max(source_event_id), 0)
	from cpamp_usage.usage_events
	where source_event_id is not null and source_event_id <= $1`, state.SourceHWMID).Scan(
		&targetCount, &targetDistinct, &targetMaxID,
	); err != nil {
		return state, fmt.Errorf("read postgres verify counts: %w", err)
	}
	mismatch := int64(0)
	if targetCount != sourceStats.Events || targetDistinct != sourceStats.Events || targetMaxID != sourceStats.MaxID {
		mismatch = 1
	}
	status := "COMPLETED"
	lastError := any(nil)
	if mismatch != 0 {
		status = "FAILED"
		lastError = fmt.Sprintf("event count mismatch source=%d target=%d distinct=%d max_source_id=%d max_target_source_id=%d",
			sourceStats.Events, targetCount, targetDistinct, sourceStats.MaxID, targetMaxID)
	}
	if _, err := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
		set phase = 'PHASE_5_VERIFY', status = $2, last_verified_id = $3,
		    last_verified_hash = $4, mismatch_rows = $5,
		    last_error = $6, updated_at = now()
		where migration_name = $1`, verifyTaskName, status, sourceStats.MaxID,
		state.SourceHWMHash, mismatch, lastError); err != nil {
		return state, fmt.Errorf("write verify state: %w", err)
	}
	return p.State(ctx, verifyTaskName)
}

func (p *Postgres) recordTaskError(ctx context.Context, task string, err error) error {
	if err == nil {
		return nil
	}
	_, updateErr := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
		set status = 'FAILED', last_error = $2, updated_at = now()
		where migration_name = $1`, task, err.Error())
	return updateErr
}

func eventArgs(event SQLiteEvent) []any {
	return []any{
		event.ID, event.RequestID, event.EventHash, event.TimestampMS, event.Timestamp,
		event.Provider, event.ExecutorType, event.Model, event.Endpoint, event.Method, event.Path,
		event.AuthType, event.AuthIndex, event.Source, event.SourceHash, event.APIKeyHash,
		event.AccountSnapshot, event.AuthLabelSnapshot, event.AuthFileSnapshot,
		event.AuthProviderSnapshot, event.AuthProjectIDSnapshot, event.AuthSnapshotAtMS,
		event.RequestedModel, event.ResolvedModel, event.ReasoningEffort, event.ServiceTier,
		event.RequestServiceTier, event.ResponseServiceTier, event.CacheInputMode,
		event.InputTokens, event.OutputTokens, event.ReasoningTokens, event.CachedTokens,
		event.CacheTokens, event.CacheReadTokens, event.CacheCreationTokens,
		event.NormalizedUncachedInput, event.NormalizedTotalInput, event.NormalizedCacheRead,
		event.NormalizedCacheCreation, event.TotalTokens, event.LatencyMS, event.TTFTMS,
		event.Failed != 0, event.FailStatusCode, event.FailSummary,
		jsonArg(event.ResponseMetadataJSON), event.HeaderQuotaRecoverAtMS, event.HeaderQuotaUsedPercent,
		event.HeaderQuotaPlanType, event.HeaderErrorKind, event.HeaderErrorCode, event.HeaderTraceID,
		event.FailBody, jsonArg(event.RawJSON), event.CreatedAtMS,
	}
}

func jsonArg(value sql.NullString) any {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	if !json.Valid([]byte(value.String)) {
		return nil
	}
	return []byte(value.String)
}
