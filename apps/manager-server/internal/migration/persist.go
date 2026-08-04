package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type PersistOptions struct {
	Stream     string
	Group      string
	Consumer   string
	BatchSize  int64
	MaxBatches int
	Follow     bool
	ClaimIdle  time.Duration
	// DeleteAfterAck keeps Redis as a bounded transport rather than a second
	// durable event store. Messages are deleted only after PostgreSQL commits
	// and the consumer group acknowledges the exact IDs.
	DeleteAfterAck bool
	Redis          *redis.Client
}

func (p *Postgres) Persist(ctx context.Context, options PersistOptions) (MigrationState, error) {
	if p == nil || p.Pool == nil {
		return MigrationState{}, errors.New("postgres is not open")
	}
	if options.Redis == nil {
		return MigrationState{}, errors.New("redis client is required")
	}
	if options.Stream == "" {
		options.Stream = UsageStreamKey
	}
	if options.Group == "" {
		options.Group = PersistConsumerGroup
	}
	if options.Consumer == "" {
		options.Consumer = "cpamp-persist-v1"
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 100
	}
	if options.ClaimIdle <= 0 {
		options.ClaimIdle = 30 * time.Second
	}
	if options.MaxBatches <= 0 && !options.Follow {
		options.MaxBatches = 1
	}
	if err := EnsureUsageStream(ctx, options.Redis, options.Stream, options.Group); err != nil {
		return MigrationState{}, err
	}
	lease, err := p.AcquireTaskLease(ctx, PersistTaskName)
	if err != nil {
		return MigrationState{}, err
	}
	defer lease()

	state, err := p.State(ctx, PersistTaskName)
	if err != nil {
		return MigrationState{}, err
	}
	for batchNo := 0; options.MaxBatches == 0 || batchNo < options.MaxBatches; {
		if state.PauseRequested {
			return p.MarkTaskPaused(ctx, PersistTaskName)
		}
		messages, readErr := readPersistMessages(ctx, options)
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return state, nil
			}
			_ = p.recordTaskError(ctx, PersistTaskName, readErr)
			return state, readErr
		}
		if len(messages) == 0 {
			if !options.Follow {
				return p.State(ctx, PersistTaskName)
			}
			state, err = p.State(ctx, PersistTaskName)
			if err != nil {
				return state, err
			}
			continue
		}

		lastStreamID := state.LastStreamID
		for _, message := range messages {
			if streamIDGreater(message.ID, lastStreamID) {
				lastStreamID = message.ID
			}
		}
		_, _, _, _, _, err = p.persistMessages(ctx, options, messages, lastStreamID)
		if err != nil {
			_ = p.recordTaskError(ctx, PersistTaskName, err)
			return state, err
		}
		ackIDs := make([]string, 0, len(messages))
		for _, message := range messages {
			ackIDs = append(ackIDs, message.ID)
		}
		if _, err := options.Redis.XAck(ctx, options.Stream, options.Group, ackIDs...).Result(); err != nil {
			wrapped := fmt.Errorf("ack redis messages through %s: %w", lastStreamID, err)
			_ = p.recordTaskError(ctx, PersistTaskName, wrapped)
			return state, wrapped
		}
		if options.DeleteAfterAck {
			if _, err := options.Redis.XDel(ctx, options.Stream, ackIDs...).Result(); err != nil {
				// The PostgreSQL commit and XACK are already durable. Failing the
				// worker here cannot make XDEL retry those acknowledged IDs and
				// would only interrupt ingestion, so leave them for an operator
				// trim while subsequent batches continue.
				_ = p.recordTaskError(ctx, PersistTaskName, fmt.Errorf("delete acknowledged redis messages through %s: %w", lastStreamID, err))
			}
		}
		// PG is committed before XACK. If this process is interrupted between
		// those operations, the pending message is safely replayed by the
		// idempotent event_hash insert.
		state, err = p.State(ctx, PersistTaskName)
		if err != nil {
			return state, err
		}
		batchNo++
	}
	return state, nil
}

func readPersistMessages(ctx context.Context, options PersistOptions) ([]redis.XMessage, error) {
	// Recover this consumer's own pending messages immediately after a restart.
	if streams, err := options.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    options.Group,
		Consumer: options.Consumer,
		Streams:  []string{options.Stream, "0"},
		Count:    options.BatchSize,
		Block:    -1,
	}).Result(); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read own redis pending messages: %w", err)
	} else if len(streams) > 0 && len(streams[0].Messages) > 0 {
		return streams[0].Messages, nil
	}

	claimed, _, err := options.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   options.Stream,
		Group:    options.Group,
		Consumer: options.Consumer,
		MinIdle:  options.ClaimIdle,
		Start:    "0-0",
		Count:    options.BatchSize,
	}).Result()
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNKNOWN COMMAND") || strings.Contains(strings.ToUpper(err.Error()), "XAUTOCLAIM") {
			claimed, fallbackErr := claimPendingWithXClaim(ctx, options)
			if fallbackErr != nil {
				return nil, fmt.Errorf("claim redis pending messages (XAUTOCLAIM unavailable): %w", fallbackErr)
			}
			if len(claimed) > 0 {
				return claimed, nil
			}
		} else {
			return nil, fmt.Errorf("autoclaim redis pending messages: %w", err)
		}
	}
	if len(claimed) > 0 {
		return claimed, nil
	}

	block := time.Duration(-1)
	if options.Follow {
		block = time.Second
	}
	streams, err := options.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    options.Group,
		Consumer: options.Consumer,
		Streams:  []string{options.Stream, ">"},
		Count:    options.BatchSize,
		Block:    block,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		var networkError net.Error
		if options.Follow && errors.As(err, &networkError) && networkError.Timeout() {
			return nil, nil
		}
		return nil, fmt.Errorf("read new redis stream messages: %w", err)
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return streams[0].Messages, nil
}

func claimPendingWithXClaim(ctx context.Context, options PersistOptions) ([]redis.XMessage, error) {
	pending, err := options.Redis.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: options.Stream,
		Group:  options.Group,
		Start:  "-",
		End:    "+",
		Count:  options.BatchSize,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(pending))
	for _, item := range pending {
		if item.Idle < options.ClaimIdle {
			continue
		}
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return options.Redis.XClaim(ctx, &redis.XClaimArgs{
		Stream:   options.Stream,
		Group:    options.Group,
		Consumer: options.Consumer,
		MinIdle:  options.ClaimIdle,
		Messages: ids,
	}).Result()
}

func (p *Postgres) persistMessages(ctx context.Context, options PersistOptions, messages []redis.XMessage, lastStreamID string) (
	int64, int64, int64, int64, string, error,
) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("begin persist transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inserted, skipped, deadLetters int64
	var lastSourceID int64
	var lastSourceHash string
	for _, message := range messages {
		event, decodeErr := decodeStreamEvent(message)
		if decodeErr != nil {
			payload, _ := json.Marshal(message.Values)
			if _, err := insertDeadLetter(ctx, tx, message, payload, "INVALID_EVENT_ENVELOPE", decodeErr); err != nil {
				return 0, 0, 0, 0, "", fmt.Errorf("record dead letter %s: %w", message.ID, err)
			}
			deadLetters++
			continue
		}

		tag, execErr := tx.Exec(ctx, insertEventSQL, eventArgs(event)...)
		if execErr != nil {
			return 0, 0, 0, 0, "", fmt.Errorf("persist event %d %s: %w", event.ID, event.EventHash, execErr)
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
			       0, 0, created_at_ms, created_at_ms
			from cpamp_usage.usage_events
			where event_hash = $1
			on conflict (event_hash) do nothing`, event.EventHash); execErr != nil {
			return 0, 0, 0, 0, "", fmt.Errorf("persist identity ledger %s: %w", event.EventHash, execErr)
		}
		if event.ID >= lastSourceID {
			lastSourceID = event.ID
			lastSourceHash = event.EventHash
		}
	}

	checkpoint := map[string]interface{}{
		"stream":       options.Stream,
		"group":        options.Group,
		"consumer":     options.Consumer,
		"last_ack_id":  lastStreamID,
		"ack_count":    len(messages),
		"dead_letters": deadLetters,
	}
	if _, err := tx.Exec(ctx, `update cpamp_usage.cpamp_migration_state
		set phase = 'PHASE_4_DUAL_RELAY', status = 'RUNNING',
			last_copied_id = case when $2 > last_copied_id then $2 else last_copied_id end,
			last_copied_hash = case when $2 > last_copied_id then $3 else last_copied_hash end,
			last_stream_id = $4,
			processed_rows = processed_rows + $5,
			inserted_rows = inserted_rows + $6,
			skipped_rows = skipped_rows + $7,
			mismatch_rows = mismatch_rows + $8,
			last_error = null, checkpoint = $9::jsonb, updated_at = now()
		where migration_name = $1`, PersistTaskName, lastSourceID, lastSourceHash, lastStreamID,
		int64(len(messages)), inserted, skipped, deadLetters, checkpointJSON(checkpoint)); err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("advance persist checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("commit persist transaction: %w", err)
	}
	return inserted, skipped, deadLetters, lastSourceID, lastSourceHash, nil
}

func insertDeadLetter(ctx context.Context, tx pgx.Tx, message redis.XMessage, payload []byte, code string, cause error) (int64, error) {
	var eventHash string
	if value, ok := message.Values["event_hash"]; ok {
		eventHash, _ = redisValueString(value)
	}
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	var id int64
	err := tx.QueryRow(ctx, `insert into cpamp_usage.usage_dead_letters
		(event_hash, source, payload, error_code, error_message, attempts, first_seen_at_ms, last_seen_at_ms)
		values ($1, 'redis_stream', $2::jsonb, $3, $4, 1,
			extract(epoch from clock_timestamp())::bigint * 1000,
			extract(epoch from clock_timestamp())::bigint * 1000)
		returning id`, eventHash, string(payload), code, cause.Error()).Scan(&id)
	return id, err
}

func eventHashFromMessage(message redis.XMessage) string {
	value, ok := message.Values["event_hash"]
	if !ok {
		return ""
	}
	hash, _ := redisValueString(value)
	return strings.TrimSpace(hash)
}
