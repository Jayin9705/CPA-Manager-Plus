package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// SQLiteEvent is the migration-only representation of the existing
// usage_events table. It intentionally keeps nullable values nullable so the
// PostgreSQL mapper does not invent values that were not present in SQLite.
type SQLiteEvent struct {
	ID                      int64
	RequestID               sql.NullString
	EventHash               string
	TimestampMS             int64
	Timestamp               string
	Provider                sql.NullString
	ExecutorType            sql.NullString
	Model                   string
	Endpoint                sql.NullString
	Method                  sql.NullString
	Path                    sql.NullString
	AuthType                sql.NullString
	AuthIndex               sql.NullString
	Source                  sql.NullString
	SourceHash              sql.NullString
	APIKeyHash              sql.NullString
	AccountSnapshot         sql.NullString
	AuthLabelSnapshot       sql.NullString
	AuthFileSnapshot        sql.NullString
	AuthProviderSnapshot    sql.NullString
	AuthProjectIDSnapshot   sql.NullString
	AuthSnapshotAtMS        sql.NullInt64
	RequestedModel          sql.NullString
	ResolvedModel           sql.NullString
	ReasoningEffort         sql.NullString
	ServiceTier             sql.NullString
	RequestServiceTier      sql.NullString
	ResponseServiceTier     sql.NullString
	CacheInputMode          sql.NullString
	InputTokens             int64
	OutputTokens            int64
	ReasoningTokens         int64
	CachedTokens            int64
	CacheTokens             int64
	CacheReadTokens         int64
	CacheCreationTokens     int64
	NormalizedUncachedInput sql.NullInt64
	NormalizedTotalInput    sql.NullInt64
	NormalizedCacheRead     sql.NullInt64
	NormalizedCacheCreation sql.NullInt64
	TotalTokens             int64
	LatencyMS               sql.NullInt64
	TTFTMS                  sql.NullInt64
	Failed                  int64
	FailStatusCode          sql.NullInt64
	FailSummary             sql.NullString
	ResponseMetadataJSON    sql.NullString
	HeaderQuotaRecoverAtMS  sql.NullInt64
	HeaderQuotaUsedPercent  sql.NullFloat64
	HeaderQuotaPlanType     sql.NullString
	HeaderErrorKind         sql.NullString
	HeaderErrorCode         sql.NullString
	HeaderTraceID           sql.NullString
	FailBody                sql.NullString
	RawJSON                 sql.NullString
	CreatedAtMS             int64
}

type SQLiteStats struct {
	Events              int64
	MaxID               int64
	MinTimestampMS      int64
	MaxTimestampMS      int64
	DuplicateHashGroups int64
}

type SQLiteSource struct {
	db *sql.DB
}

func OpenSQLiteSource(path string) (*SQLiteSource, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite path: %w", err)
	}
	fileURL := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := fileURL.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "query_only(1)")
	fileURL.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", fileURL.String())
	if err != nil {
		return nil, fmt.Errorf("open sqlite source: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite source: %w", err)
	}
	return &SQLiteSource{db: db}, nil
}

func (s *SQLiteSource) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteSource) Stats(ctx context.Context, maxID int64) (SQLiteStats, error) {
	if s == nil || s.db == nil {
		return SQLiteStats{}, errors.New("sqlite source is not open")
	}
	where := ""
	args := []any{}
	if maxID > 0 {
		where = " where id <= ?"
		args = append(args, maxID)
	}
	var stats SQLiteStats
	if err := s.db.QueryRowContext(ctx, `select
		count(*),
		coalesce(max(id), 0),
		coalesce(min(timestamp_ms), 0),
		coalesce(max(timestamp_ms), 0)
	from usage_events`+where, args...).Scan(
		&stats.Events,
		&stats.MaxID,
		&stats.MinTimestampMS,
		&stats.MaxTimestampMS,
	); err != nil {
		return SQLiteStats{}, fmt.Errorf("read sqlite stats: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from (
		select event_hash from usage_events`+where+` group by event_hash having count(*) > 1
	)`, args...).Scan(&stats.DuplicateHashGroups); err != nil {
		return SQLiteStats{}, fmt.Errorf("read duplicate hash stats: %w", err)
	}
	return stats, nil
}

func (s *SQLiteSource) EventHashAt(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", nil
	}
	var hash string
	if err := s.db.QueryRowContext(ctx, `select event_hash from usage_events where id = ?`, id).Scan(&hash); err != nil {
		return "", fmt.Errorf("read sqlite event hash at id %d: %w", id, err)
	}
	return hash, nil
}

// MaxID returns the current append-only high-water mark without performing
// the full source statistics scan. It is used by the online relay so polling
// does not compete with the live CPAMP writer for unnecessary read work.
func (s *SQLiteSource) MaxID(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite source is not open")
	}
	var maxID int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(id), 0) from usage_events`).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("read sqlite max event id: %w", err)
	}
	return maxID, nil
}

func (s *SQLiteSource) Batch(ctx context.Context, afterID, maxID int64, limit int) ([]SQLiteEvent, error) {
	if limit <= 0 {
		limit = 1000
	}
	if maxID <= afterID {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `select
		id, request_id, event_hash, timestamp_ms, timestamp, provider, executor_type, model,
		endpoint, method, path, auth_type, auth_index, source, source_hash, api_key_hash,
		account_snapshot, auth_label_snapshot, auth_file_snapshot, auth_provider_snapshot,
		auth_project_id_snapshot, auth_snapshot_at_ms, requested_model, resolved_model,
		reasoning_effort, service_tier, request_service_tier, response_service_tier,
		cache_input_mode, input_tokens, output_tokens, reasoning_tokens, cached_tokens,
		cache_tokens, cache_read_tokens, cache_creation_tokens, normalized_uncached_input_tokens,
		normalized_total_input_tokens, normalized_cache_read_tokens, normalized_cache_creation_tokens,
		total_tokens, latency_ms, ttft_ms, failed, fail_status_code, fail_summary,
		response_metadata_json, header_quota_recover_at_ms, header_quota_used_percent,
		header_quota_plan_type, header_error_kind, header_error_code, header_trace_id,
		fail_body, raw_json, created_at_ms
	from usage_events
	where id > ? and id <= ?
	order by id asc
	limit ?`, afterID, maxID, limit)
	if err != nil {
		return nil, fmt.Errorf("read sqlite event batch after %d: %w", afterID, err)
	}
	defer rows.Close()

	events := make([]SQLiteEvent, 0, limit)
	for rows.Next() {
		var event SQLiteEvent
		if err := rows.Scan(
			&event.ID,
			&event.RequestID,
			&event.EventHash,
			&event.TimestampMS,
			&event.Timestamp,
			&event.Provider,
			&event.ExecutorType,
			&event.Model,
			&event.Endpoint,
			&event.Method,
			&event.Path,
			&event.AuthType,
			&event.AuthIndex,
			&event.Source,
			&event.SourceHash,
			&event.APIKeyHash,
			&event.AccountSnapshot,
			&event.AuthLabelSnapshot,
			&event.AuthFileSnapshot,
			&event.AuthProviderSnapshot,
			&event.AuthProjectIDSnapshot,
			&event.AuthSnapshotAtMS,
			&event.RequestedModel,
			&event.ResolvedModel,
			&event.ReasoningEffort,
			&event.ServiceTier,
			&event.RequestServiceTier,
			&event.ResponseServiceTier,
			&event.CacheInputMode,
			&event.InputTokens,
			&event.OutputTokens,
			&event.ReasoningTokens,
			&event.CachedTokens,
			&event.CacheTokens,
			&event.CacheReadTokens,
			&event.CacheCreationTokens,
			&event.NormalizedUncachedInput,
			&event.NormalizedTotalInput,
			&event.NormalizedCacheRead,
			&event.NormalizedCacheCreation,
			&event.TotalTokens,
			&event.LatencyMS,
			&event.TTFTMS,
			&event.Failed,
			&event.FailStatusCode,
			&event.FailSummary,
			&event.ResponseMetadataJSON,
			&event.HeaderQuotaRecoverAtMS,
			&event.HeaderQuotaUsedPercent,
			&event.HeaderQuotaPlanType,
			&event.HeaderErrorKind,
			&event.HeaderErrorCode,
			&event.HeaderTraceID,
			&event.FailBody,
			&event.RawJSON,
			&event.CreatedAtMS,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite event batch: %w", err)
	}
	return events, nil
}
