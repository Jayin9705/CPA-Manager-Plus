package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RelayOptions struct {
	Stream       string
	Group        string
	BatchSize    int
	MaxBatches   int
	Follow       bool
	PollInterval time.Duration
	Redis        *redis.Client
}

func (p *Postgres) Relay(ctx context.Context, source *SQLiteSource, options RelayOptions) (MigrationState, error) {
	if p == nil || p.Pool == nil {
		return MigrationState{}, errors.New("postgres is not open")
	}
	if source == nil {
		return MigrationState{}, errors.New("sqlite source is required")
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
	if options.BatchSize <= 0 {
		options.BatchSize = 100
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 250 * time.Millisecond
	}
	if options.MaxBatches <= 0 && !options.Follow {
		options.MaxBatches = 1
	}
	if err := EnsureUsageStream(ctx, options.Redis, options.Stream, options.Group); err != nil {
		return MigrationState{}, err
	}
	lease, err := p.AcquireTaskLease(ctx, RelayTaskName)
	if err != nil {
		return MigrationState{}, err
	}
	defer lease()

	state, err := p.State(ctx, RelayTaskName)
	if err != nil {
		return MigrationState{}, err
	}
	for batchNo := 0; options.MaxBatches == 0 || batchNo < options.MaxBatches; {
		if state.PauseRequested {
			return p.MarkTaskPaused(ctx, RelayTaskName)
		}

		maxID, err := source.MaxID(ctx)
		if err != nil {
			_ = p.recordTaskError(ctx, RelayTaskName, err)
			return state, err
		}
		if maxID <= state.LastCopiedID {
			if !options.Follow {
				return p.State(ctx, RelayTaskName)
			}
			if err := waitForRelayPoll(ctx, options.PollInterval); err != nil {
				return state, nil
			}
			state, err = p.State(ctx, RelayTaskName)
			if err != nil {
				return state, err
			}
			continue
		}

		events, err := source.Batch(ctx, state.LastCopiedID, maxID, options.BatchSize)
		if err != nil {
			_ = p.recordTaskError(ctx, RelayTaskName, err)
			return state, err
		}
		if len(events) == 0 {
			if !options.Follow {
				return p.State(ctx, RelayTaskName)
			}
			if err := waitForRelayPoll(ctx, options.PollInterval); err != nil {
				return state, nil
			}
			continue
		}

		lastStreamID := state.LastStreamID
		for _, event := range events {
			payload, marshalErr := marshalStreamEvent(event)
			if marshalErr != nil {
				_ = p.recordTaskError(ctx, RelayTaskName, marshalErr)
				return state, marshalErr
			}
			streamID, addErr := options.Redis.XAdd(ctx, &redis.XAddArgs{
				Stream: options.Stream,
				Values: map[string]interface{}{
					"version":         "1",
					"source_event_id": event.ID,
					"event_hash":      event.EventHash,
					"event_json":      payload,
				},
			}).Result()
			if addErr != nil {
				wrapped := fmt.Errorf("xadd source event %d %s: %w", event.ID, event.EventHash, addErr)
				_ = p.recordTaskError(ctx, RelayTaskName, wrapped)
				return state, wrapped
			}
			if streamIDGreater(streamID, lastStreamID) {
				lastStreamID = streamID
			}
		}

		last := events[len(events)-1]
		checkpoint := map[string]interface{}{
			"source_event_id":   last.ID,
			"source_event_hash": last.EventHash,
			"stream":            options.Stream,
			"last_stream_id":    lastStreamID,
			"read_mode":         "sqlite_read_only_tail",
		}
		if _, err := p.Pool.Exec(ctx, `update cpamp_usage.cpamp_migration_state
			set phase = 'PHASE_4_DUAL_RELAY', status = 'RUNNING',
				last_copied_id = $2, last_copied_hash = $3,
				last_stream_id = $4,
				processed_rows = processed_rows + $5,
				inserted_rows = inserted_rows + $5,
				last_error = null, checkpoint = $6::jsonb, updated_at = now()
			where migration_name = $1`, RelayTaskName, last.ID, last.EventHash,
			lastStreamID, int64(len(events)), checkpointJSON(checkpoint)); err != nil {
			_ = p.recordTaskError(ctx, RelayTaskName, err)
			return state, fmt.Errorf("advance relay checkpoint: %w", err)
		}
		state, err = p.State(ctx, RelayTaskName)
		if err != nil {
			return state, err
		}
		batchNo++
	}
	return state, nil
}

func waitForRelayPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
