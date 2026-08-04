package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	UsageStreamKey       = "cpamp:usage:v1"
	PersistConsumerGroup = "cpamp-persist-v1"
	RelayTaskName        = "relay.sqlite_to_redis"
	PersistTaskName      = "persist.redis_to_postgres"
	HourlyRollupTaskName = "rollup.hourly"
	DailyRollupTaskName  = "rollup.daily"
	DefaultRedisAddress  = "127.0.0.1:6379"
)

// RedisOptions deliberately defaults to localhost. The migration stream
// contains request metadata and is never meant to be reachable from the
// public network.
type RedisOptions struct {
	Address  string
	Password string
	DB       int
}

func OpenRedis(ctx context.Context, options RedisOptions) (*redis.Client, error) {
	address := strings.TrimSpace(options.Address)
	if address == "" {
		address = DefaultRedisAddress
	}
	client := redis.NewClient(&redis.Options{
		Addr:         address,
		Password:     options.Password,
		DB:           options.DB,
		MinIdleConns: 1,
		PoolSize:     8,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis %s: %w", address, err)
	}
	return client, nil
}

func EnsureUsageStream(ctx context.Context, client *redis.Client, stream, group string) error {
	if client == nil {
		return errors.New("redis client is required")
	}
	if strings.TrimSpace(stream) == "" {
		return errors.New("redis stream is required")
	}
	if strings.TrimSpace(group) == "" {
		return errors.New("redis consumer group is required")
	}
	err := client.XGroupCreateMkStream(ctx, stream, group, "0-0").Err()
	if err != nil && !strings.Contains(strings.ToUpper(err.Error()), "BUSYGROUP") {
		return fmt.Errorf("create redis stream group %s/%s: %w", stream, group, err)
	}
	return nil
}

type streamEventEnvelope struct {
	Version       int         `json:"version"`
	SourceEventID int64       `json:"source_event_id"`
	EventHash     string      `json:"event_hash"`
	Event         SQLiteEvent `json:"event"`
}

func marshalStreamEvent(event SQLiteEvent) (string, error) {
	envelope := streamEventEnvelope{
		Version:       1,
		SourceEventID: event.ID,
		EventHash:     event.EventHash,
		Event:         event,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal event %d %s: %w", event.ID, event.EventHash, err)
	}
	return string(payload), nil
}

func decodeStreamEvent(message redis.XMessage) (SQLiteEvent, error) {
	raw, ok := message.Values["event_json"]
	if !ok {
		return SQLiteEvent{}, errors.New("redis message has no event_json field")
	}
	payload, ok := redisValueString(raw)
	if !ok || strings.TrimSpace(payload) == "" {
		return SQLiteEvent{}, errors.New("redis event_json field is empty")
	}
	var envelope streamEventEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return SQLiteEvent{}, fmt.Errorf("decode event_json: %w", err)
	}
	if envelope.Version != 1 {
		return SQLiteEvent{}, fmt.Errorf("unsupported event envelope version %d", envelope.Version)
	}
	if envelope.SourceEventID <= 0 || envelope.Event.ID != envelope.SourceEventID {
		return SQLiteEvent{}, fmt.Errorf("event source id mismatch envelope=%d event=%d", envelope.SourceEventID, envelope.Event.ID)
	}
	if strings.TrimSpace(envelope.EventHash) == "" || envelope.EventHash != envelope.Event.EventHash {
		return SQLiteEvent{}, errors.New("event hash mismatch or empty")
	}
	return envelope.Event, nil
}

func redisValueString(value interface{}) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case []byte:
		return string(value), true
	case nil:
		return "", false
	default:
		return fmt.Sprint(value), true
	}
}

func streamIDGreater(left, right string) bool {
	if right == "" {
		return left != ""
	}
	leftMS, leftSeq, leftOK := parseStreamID(left)
	rightMS, rightSeq, rightOK := parseStreamID(right)
	if leftOK && rightOK {
		if leftMS != rightMS {
			return leftMS > rightMS
		}
		return leftSeq > rightSeq
	}
	return left > right
}

func parseStreamID(value string) (int64, int64, bool) {
	parts := strings.SplitN(value, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	ms, err1 := strconv.ParseInt(parts[0], 10, 64)
	seq, err2 := strconv.ParseInt(parts[1], 10, 64)
	return ms, seq, err1 == nil && err2 == nil
}
