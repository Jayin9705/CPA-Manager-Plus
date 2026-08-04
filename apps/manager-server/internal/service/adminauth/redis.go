package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const sessionKeyPrefix = "cpamp:auth:session:"

var consumeLoginAttemptScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {count, ttl}
`)

type redisSessionRepository struct {
	client *redis.Client
}

func newRedisSessionRepository(ctx context.Context, addr string, password string, db int) (SessionRepository, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("CPAMP_AUTH_REDIS_ADDR is required")
	}
	repository := &redisSessionRepository{client: redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  750 * time.Millisecond,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
		PoolTimeout:  1 * time.Second,
		MaxRetries:   1,
	})}
	if err := repository.client.Ping(ctx).Err(); err != nil {
		_ = repository.client.Close()
		return nil, fmt.Errorf("ping administrator Redis: %w", err)
	}
	return repository, nil
}

func (r *redisSessionRepository) Save(ctx context.Context, tokenDigest string, session Session, ttl time.Duration) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, sessionKeyPrefix+tokenDigest, payload, ttl).Err()
}

func (r *redisSessionRepository) Load(ctx context.Context, tokenDigest string) (Session, error) {
	payload, err := r.client.Get(ctx, sessionKeyPrefix+tokenDigest).Bytes()
	if errors.Is(err, redis.Nil) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(payload, &session); err != nil {
		return Session{}, fmt.Errorf("decode administrator session: %w", err)
	}
	return session, nil
}

func (r *redisSessionRepository) Delete(ctx context.Context, tokenDigest string) error {
	return r.client.Del(ctx, sessionKeyPrefix+tokenDigest).Err()
}

func (r *redisSessionRepository) ConsumeLoginAttempt(ctx context.Context, keys []string, limit int, window time.Duration) (bool, time.Duration, error) {
	allowed := true
	var retryAfter time.Duration
	for _, key := range keys {
		result, err := consumeLoginAttemptScript.Run(ctx, r.client, []string{key}, window.Milliseconds()).Result()
		if err != nil {
			return false, 0, err
		}
		values, ok := result.([]interface{})
		if !ok || len(values) != 2 {
			return false, 0, errors.New("unexpected Redis login rate-limit response")
		}
		count, ok := redisInt64(values[0])
		if !ok {
			return false, 0, errors.New("invalid Redis login rate-limit count")
		}
		ttlMilliseconds, ok := redisInt64(values[1])
		if !ok {
			return false, 0, errors.New("invalid Redis login rate-limit TTL")
		}
		if count > int64(limit) {
			allowed = false
			if ttl := time.Duration(ttlMilliseconds) * time.Millisecond; ttl > retryAfter {
				retryAfter = ttl
			}
		}
	}
	return allowed, retryAfter, nil
}

func (r *redisSessionRepository) ResetLoginAttempts(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.client.Del(ctx, keys...).Err()
}

func (r *redisSessionRepository) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func redisInt64(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case string:
		var parsed int64
		_, err := fmt.Sscan(typed, &parsed)
		return parsed, err == nil
	default:
		return 0, false
	}
}
