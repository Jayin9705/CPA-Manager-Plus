package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	usageevent "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageevent"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

// postgresAnalyticsEvent is the small, normalized raw-event projection used
// by the monitoring read path. PostgreSQL stores the canonical event, while
// the monitor keeps the same grouping and local-time bucket semantics as the
// legacy usage repository.
type postgresAnalyticsEvent struct {
	ID                    int64
	TimestampMS           int64
	Timestamp             string
	Model                 string
	BillingModel          string
	ServiceTier           string
	Provider              string
	AuthIndex             string
	Source                string
	SourceHash            string
	APIKeyHash            string
	AccountSnapshot       string
	AuthLabelSnapshot     string
	AuthFileSnapshot      string
	AuthProviderSnapshot  string
	AuthProjectIDSnapshot string
	RequestType           string
	Endpoint              string
	Failed                bool
	InputTokens           int64
	OutputTokens          int64
	ReasoningTokens       int64
	CachedTokens          int64
	CacheReadTokens       int64
	CacheCreationTokens   int64
	TotalTokens           int64
	LatencyMS             sql.NullInt64
	TTFTMS                sql.NullInt64
	HeaderErrorKind       string
	HeaderErrorCode       string
	HeaderQuotaPlanType   string
	HeaderTraceID         string
}

const (
	postgresNormalizedInputExpr = "coalesce(normalized_total_input, input_tokens)"
	postgresCachedTokensExpr    = "greatest(greatest(coalesce(cached_tokens, 0), coalesce(cache_tokens, 0)) - greatest(coalesce(cache_read_tokens, 0), 0) - greatest(coalesce(cache_creation_tokens, 0), 0), 0)"
	postgresBillingModelExpr    = "coalesce(nullif(resolved_model, ''), model)"
	postgresServiceTierExpr     = "coalesce(service_tier, '')"
	postgresProviderExpr        = "coalesce(nullif(auth_provider_snapshot, ''), provider, '')"
	postgresLongInputExpr       = "case when " + postgresNormalizedInputExpr + " > 272000 then " + postgresNormalizedInputExpr + " else 0 end"
	postgresLongOutputExpr      = "case when " + postgresNormalizedInputExpr + " > 272000 then coalesce(output_tokens, 0) else 0 end"
	postgresLongCachedExpr      = "case when " + postgresNormalizedInputExpr + " > 272000 then " + postgresCachedTokensExpr + " else 0 end"
	postgresLongCacheReadExpr   = "case when " + postgresNormalizedInputExpr + " > 272000 then coalesce(cache_read_tokens, 0) else 0 end"
	postgresLongCacheCreateExpr = "case when " + postgresNormalizedInputExpr + " > 272000 then coalesce(cache_creation_tokens, 0) else 0 end"
)

func (p *Postgres) loadMonitoringEvents(ctx context.Context, filter store.AnalyticsFilter) ([]postgresAnalyticsEvent, error) {
	if cache := monitoringEventsCacheFromContext(ctx); cache != nil {
		return cache.load(ctx, monitoringAnalyticsFilterKey(filter), func(loadCtx context.Context) ([]postgresAnalyticsEvent, error) {
			return p.loadMonitoringEventsShared(loadCtx, filter)
		})
	}
	return p.loadMonitoringEventsShared(ctx, filter)
}

// loadMonitoringEventsShared coalesces identical raw-event reads across
// concurrent HTTP requests. The request-scoped cache above handles duplicate
// dimensions inside one analytics response; this second layer handles the
// common case where the page and a refresh/component request arrive together.
// singleflight retains no event data after the query finishes, so the monitor
// never turns this optimization into an unbounded process cache.
func (p *Postgres) loadMonitoringEventsShared(ctx context.Context, filter store.AnalyticsFilter) ([]postgresAnalyticsEvent, error) {
	key := monitoringAnalyticsFilterKey(filter)
	result, err, _ := p.monitoringFlight.Do(key, func() (any, error) {
		return p.loadMonitoringEventsUncached(ctx, filter)
	})
	if err != nil {
		return nil, err
	}
	events, ok := result.([]postgresAnalyticsEvent)
	if !ok {
		return nil, fmt.Errorf("invalid PostgreSQL monitoring event snapshot")
	}
	return events, nil
}

func (p *Postgres) loadMonitoringEventsUncached(ctx context.Context, filter store.AnalyticsFilter) ([]postgresAnalyticsEvent, error) {
	if p == nil || p.Pool == nil {
		return []postgresAnalyticsEvent{}, nil
	}
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select
		id,
		timestamp_ms,
		coalesce(timestamp, ''),
		model,
		coalesce(nullif(resolved_model, ''), model),
		coalesce(service_tier, ''),
		coalesce(nullif(auth_provider_snapshot, ''), provider, ''),
		coalesce(auth_index, ''),
		coalesce(source, ''),
		coalesce(source_hash, ''),
		coalesce(api_key_hash, ''),
		coalesce(account_snapshot, ''),
		coalesce(auth_label_snapshot, ''),
		coalesce(auth_file_snapshot, ''),
		coalesce(auth_provider_snapshot, ''),
		coalesce(auth_project_id_snapshot, ''),
		coalesce(executor_type, ''),
		coalesce(endpoint, ''),
		failed,
		` + postgresNormalizedInputExpr + `,
		coalesce(output_tokens, 0),
		coalesce(reasoning_tokens, 0),
		` + postgresCachedTokensExpr + `,
		coalesce(cache_read_tokens, 0),
		coalesce(cache_creation_tokens, 0),
		coalesce(total_tokens, 0),
		latency_ms,
		ttft_ms,
		coalesce(header_error_kind, ''),
		coalesce(header_error_code, ''),
		coalesce(header_quota_plan_type, ''),
		coalesce(header_trace_id, '')
	from cpamp_usage.usage_events ` + where
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL monitoring events: %w", err)
	}
	defer rows.Close()

	events := make([]postgresAnalyticsEvent, 0)
	for rows.Next() {
		var event postgresAnalyticsEvent
		if err := rows.Scan(
			&event.ID,
			&event.TimestampMS,
			&event.Timestamp,
			&event.Model,
			&event.BillingModel,
			&event.ServiceTier,
			&event.Provider,
			&event.AuthIndex,
			&event.Source,
			&event.SourceHash,
			&event.APIKeyHash,
			&event.AccountSnapshot,
			&event.AuthLabelSnapshot,
			&event.AuthFileSnapshot,
			&event.AuthProviderSnapshot,
			&event.AuthProjectIDSnapshot,
			&event.RequestType,
			&event.Endpoint,
			&event.Failed,
			&event.InputTokens,
			&event.OutputTokens,
			&event.ReasoningTokens,
			&event.CachedTokens,
			&event.CacheReadTokens,
			&event.CacheCreationTokens,
			&event.TotalTokens,
			&event.LatencyMS,
			&event.TTFTMS,
			&event.HeaderErrorKind,
			&event.HeaderErrorCode,
			&event.HeaderQuotaPlanType,
			&event.HeaderTraceID,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL monitoring event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL monitoring events: %w", err)
	}
	return events, nil
}

type postgresMetricTotals struct {
	usage.LongContextTokens
	calls           int64
	success         int64
	failure         int64
	inputTokens     int64
	outputTokens    int64
	reasoningTokens int64
	cachedTokens    int64
	cacheReadTokens int64
	cacheCreation   int64
	totalTokens     int64
	latencySumMS    float64
	latencySamples  int64
	firstMS         int64
	lastMS          int64
	maxLatencyMS    sql.NullInt64
	zeroTokenCalls  int64
}

func (totals *postgresMetricTotals) add(event postgresAnalyticsEvent) {
	totals.calls++
	if event.Failed {
		totals.failure++
	} else {
		totals.success++
	}
	totals.inputTokens += event.InputTokens
	totals.outputTokens += event.OutputTokens
	totals.reasoningTokens += event.ReasoningTokens
	totals.cachedTokens += event.CachedTokens
	totals.cacheReadTokens += event.CacheReadTokens
	totals.cacheCreation += event.CacheCreationTokens
	totals.totalTokens += event.TotalTokens
	totals.AddIfLongContext(event.InputTokens, event.OutputTokens, event.CachedTokens, event.CacheReadTokens, event.CacheCreationTokens)
	if totals.firstMS == 0 || event.TimestampMS < totals.firstMS {
		totals.firstMS = event.TimestampMS
	}
	if event.TimestampMS > totals.lastMS {
		totals.lastMS = event.TimestampMS
	}
	if event.LatencyMS.Valid {
		if !totals.maxLatencyMS.Valid || event.LatencyMS.Int64 > totals.maxLatencyMS.Int64 {
			totals.maxLatencyMS = event.LatencyMS
		}
		if event.LatencyMS.Int64 > 0 {
			totals.latencySumMS += float64(event.LatencyMS.Int64)
			totals.latencySamples++
		}
	}
}

func (totals postgresMetricTotals) averageLatency() sql.NullFloat64 {
	if totals.latencySamples <= 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: totals.latencySumMS / float64(totals.latencySamples), Valid: true}
}

func (totals postgresMetricTotals) aggregate() store.Aggregate {
	return store.Aggregate{
		LongContextTokens:   totals.LongContextTokens,
		TotalCalls:          totals.calls,
		SuccessCalls:        totals.success,
		FailureCalls:        totals.failure,
		InputTokens:         totals.inputTokens,
		OutputTokens:        totals.outputTokens,
		ReasoningTokens:     totals.reasoningTokens,
		CachedTokens:        totals.cachedTokens,
		CacheReadTokens:     totals.cacheReadTokens,
		CacheCreationTokens: totals.cacheCreation,
		TotalTokens:         totals.totalTokens,
		AvgLatencyMS:        totals.averageLatency(),
		LatencySamples:      totals.latencySamples,
		ZeroTokenCalls:      totals.zeroTokenCalls,
	}
}

// AggregateWithFilter reads the canonical PostgreSQL events. It is used for
// filtered and partial-hour windows where the permanent hourly rollup cannot
// represent every predicate or edge event.
func (p *Postgres) AggregateWithFilter(ctx context.Context, filter store.AnalyticsFilter) (store.Aggregate, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return store.Aggregate{}, err
	}
	var totals postgresMetricTotals
	for _, event := range events {
		totals.add(event)
		if event.TotalTokens == 0 && !event.Failed {
			totals.zeroTokenCalls++
		}
	}
	return totals.aggregate(), nil
}

type postgresModelKey struct {
	model        string
	billingModel string
	serviceTier  string
}

type postgresModelGroup struct {
	key postgresModelKey
	postgresMetricTotals
}

func collectPostgresModelGroups(events []postgresAnalyticsEvent) map[postgresModelKey]*postgresModelGroup {
	groups := make(map[postgresModelKey]*postgresModelGroup)
	for _, event := range events {
		key := postgresModelKey{event.Model, event.BillingModel, event.ServiceTier}
		group := groups[key]
		if group == nil {
			group = &postgresModelGroup{key: key}
			groups[key] = group
		}
		group.add(event)
	}
	return groups
}

func (group postgresModelGroup) modelStat() store.ModelStat {
	return store.ModelStat{
		LongContextTokens:   group.LongContextTokens,
		Model:               group.key.model,
		BillingModel:        group.key.billingModel,
		ServiceTier:         group.key.serviceTier,
		Calls:               group.calls,
		SuccessCalls:        group.success,
		InputTokens:         group.inputTokens,
		OutputTokens:        group.outputTokens,
		ReasoningTokens:     group.reasoningTokens,
		CachedTokens:        group.cachedTokens,
		CacheReadTokens:     group.cacheReadTokens,
		CacheCreationTokens: group.cacheCreation,
		TotalTokens:         group.totalTokens,
	}
}

func (p *Postgres) ModelStatsWithFilter(ctx context.Context, filter store.AnalyticsFilter, limit int) ([]store.ModelStat, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := collectPostgresModelGroups(events)
	models := make(map[string]int64)
	for _, group := range groups {
		models[group.key.model] += group.calls
	}
	selected := map[string]struct{}{}
	if limit > 0 {
		type modelCount struct {
			model string
			calls int64
		}
		counts := make([]modelCount, 0, len(models))
		for model, calls := range models {
			counts = append(counts, modelCount{model: model, calls: calls})
		}
		sort.SliceStable(counts, func(i, j int) bool {
			return counts[i].calls > counts[j].calls || (counts[i].calls == counts[j].calls && counts[i].model < counts[j].model)
		})
		if limit > len(counts) {
			limit = len(counts)
		}
		for _, item := range counts[:limit] {
			selected[item.model] = struct{}{}
		}
	}
	result := make([]store.ModelStat, 0, len(groups))
	for _, group := range groups {
		if limit > 0 {
			if _, ok := selected[group.key.model]; !ok {
				continue
			}
		}
		result = append(result, group.modelStat())
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Calls > result[j].Calls || (result[i].Calls == result[j].Calls && result[i].Model < result[j].Model)
	})
	return result, nil
}

type postgresTimelineKey struct {
	bucketMS     int64
	model        string
	billingModel string
	serviceTier  string
}

func (p *Postgres) TimelineWithFilter(ctx context.Context, filter store.AnalyticsFilter, granularity string, location *time.Location) ([]store.TimelinePoint, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[postgresTimelineKey]*postgresMetricTotals)
	for _, event := range events {
		key := postgresTimelineKey{
			bucketMS:     usage.AnalyticsBucketMS(event.TimestampMS, granularity, location),
			model:        event.Model,
			billingModel: event.BillingModel,
			serviceTier:  event.ServiceTier,
		}
		group := groups[key]
		if group == nil {
			group = &postgresMetricTotals{}
			groups[key] = group
		}
		group.add(event)
	}
	keys := make([]postgresTimelineKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].bucketMS != keys[j].bucketMS {
			return keys[i].bucketMS < keys[j].bucketMS
		}
		return keys[i].model < keys[j].model
	})
	result := make([]store.TimelinePoint, 0, len(keys))
	for _, key := range keys {
		totals := groups[key]
		result = append(result, store.TimelinePoint{
			LongContextTokens:   totals.LongContextTokens,
			BucketMS:            key.bucketMS,
			Model:               key.model,
			BillingModel:        key.billingModel,
			ServiceTier:         key.serviceTier,
			Calls:               totals.calls,
			Tokens:              totals.totalTokens,
			Success:             totals.success,
			Failure:             totals.failure,
			InputTokens:         totals.inputTokens,
			OutputTokens:        totals.outputTokens,
			ReasoningTokens:     totals.reasoningTokens,
			CachedTokens:        totals.cachedTokens,
			CacheReadTokens:     totals.cacheReadTokens,
			CacheCreationTokens: totals.cacheCreation,
			AvgLatencyMS:        totals.averageLatency(),
			LatencySamples:      totals.latencySamples,
		})
	}
	return result, nil
}

type postgresAPIKeyTimelineKey struct {
	apiKeyHash   string
	bucketMS     int64
	model        string
	billingModel string
	serviceTier  string
}

func (p *Postgres) APIKeyTimelineWithFilter(ctx context.Context, filter store.AnalyticsFilter, granularity string, location *time.Location) ([]store.APIKeyTimelinePoint, error) {
	if len(normalizePGValues(filter.APIKeyHashes)) == 0 && strings.TrimSpace(filter.SearchAPIKeyHash) == "" {
		return nil, nil
	}
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[postgresAPIKeyTimelineKey]*postgresMetricTotals)
	for _, event := range events {
		key := postgresAPIKeyTimelineKey{
			apiKeyHash:   event.APIKeyHash,
			bucketMS:     usage.AnalyticsBucketMS(event.TimestampMS, granularity, location),
			model:        event.Model,
			billingModel: event.BillingModel,
			serviceTier:  event.ServiceTier,
		}
		group := groups[key]
		if group == nil {
			group = &postgresMetricTotals{}
			groups[key] = group
		}
		group.add(event)
	}
	keys := make([]postgresAPIKeyTimelineKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].bucketMS != keys[j].bucketMS {
			return keys[i].bucketMS < keys[j].bucketMS
		}
		if keys[i].apiKeyHash != keys[j].apiKeyHash {
			return keys[i].apiKeyHash < keys[j].apiKeyHash
		}
		return keys[i].model < keys[j].model
	})
	result := make([]store.APIKeyTimelinePoint, 0, len(keys))
	for _, key := range keys {
		totals := groups[key]
		result = append(result, store.APIKeyTimelinePoint{
			LongContextTokens:   totals.LongContextTokens,
			APIKeyHash:          key.apiKeyHash,
			BucketMS:            key.bucketMS,
			Model:               key.model,
			BillingModel:        key.billingModel,
			ServiceTier:         key.serviceTier,
			Calls:               totals.calls,
			Tokens:              totals.totalTokens,
			Success:             totals.success,
			Failure:             totals.failure,
			InputTokens:         totals.inputTokens,
			OutputTokens:        totals.outputTokens,
			ReasoningTokens:     totals.reasoningTokens,
			CachedTokens:        totals.cachedTokens,
			CacheReadTokens:     totals.cacheReadTokens,
			CacheCreationTokens: totals.cacheCreation,
			AvgLatencyMS:        totals.averageLatency(),
			LatencySamples:      totals.latencySamples,
		})
	}
	return result, nil
}

func (p *Postgres) LatencyPercentilesWithFilter(ctx context.Context, filter store.AnalyticsFilter, granularity string, location *time.Location) ([]store.LatencyPercentiles, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	latencies := make(map[int64][]float64)
	ttfts := make(map[int64][]float64)
	for _, event := range events {
		bucket := usage.AnalyticsBucketMS(event.TimestampMS, granularity, location)
		if event.LatencyMS.Valid && event.LatencyMS.Int64 > 0 {
			latencies[bucket] = append(latencies[bucket], float64(event.LatencyMS.Int64))
		}
		if event.TTFTMS.Valid && event.TTFTMS.Int64 > 0 {
			ttfts[bucket] = append(ttfts[bucket], float64(event.TTFTMS.Int64))
		}
	}
	bucketSet := make(map[int64]struct{}, len(latencies)+len(ttfts))
	for bucket := range latencies {
		bucketSet[bucket] = struct{}{}
	}
	for bucket := range ttfts {
		bucketSet[bucket] = struct{}{}
	}
	buckets := make([]int64, 0, len(bucketSet))
	for bucket := range bucketSet {
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	result := make([]store.LatencyPercentiles, 0, len(buckets))
	for _, bucket := range buckets {
		point := store.LatencyPercentiles{BucketMS: bucket}
		if value, ok := postgresPercentile95(latencies[bucket]); ok {
			point.P95LatencyMS = sql.NullFloat64{Float64: value, Valid: true}
		}
		if value, ok := postgresPercentile95(ttfts[bucket]); ok {
			point.P95TTFTMS = sql.NullFloat64{Float64: value, Valid: true}
		}
		result = append(result, point)
	}
	return result, nil
}

func (p *Postgres) latencySummaryFromEvents(ctx context.Context, filter store.AnalyticsFilter) (store.LatencySummary, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return store.LatencySummary{}, err
	}
	latencies := make([]float64, 0)
	ttfts := make([]float64, 0)
	for _, event := range events {
		if event.LatencyMS.Valid && event.LatencyMS.Int64 > 0 {
			latencies = append(latencies, float64(event.LatencyMS.Int64))
		}
		if event.TTFTMS.Valid && event.TTFTMS.Int64 > 0 {
			ttfts = append(ttfts, float64(event.TTFTMS.Int64))
		}
	}
	var result store.LatencySummary
	if value, ok := postgresPercentile95(latencies); ok {
		result.P95LatencyMS = sql.NullFloat64{Float64: value, Valid: true}
	}
	if value, ok := postgresPercentile95(ttfts); ok {
		result.P95TTFTMS = sql.NullFloat64{Float64: value, Valid: true}
	}
	return result, nil
}

func (p *Postgres) LatencySummaryWithFilter(ctx context.Context, filter store.AnalyticsFilter) (store.LatencySummary, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select
		percentile_disc(0.95) within group (order by latency_ms) filter (where latency_ms > 0),
		percentile_disc(0.95) within group (order by ttft_ms) filter (where ttft_ms > 0)
	from cpamp_usage.usage_events ` + where
	var latencyMS, ttftMS sql.NullInt64
	if err := p.Pool.QueryRow(ctx, query, args...).Scan(&latencyMS, &ttftMS); err != nil {
		return store.LatencySummary{}, fmt.Errorf("read PostgreSQL latency summary: %w", err)
	}
	return store.LatencySummary{
		P95LatencyMS: nullInt64AsFloat(latencyMS),
		P95TTFTMS:    nullInt64AsFloat(ttftMS),
	}, nil
}

func nullInt64AsFloat(value sql.NullInt64) sql.NullFloat64 {
	if !value.Valid {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: float64(value.Int64), Valid: true}
}

func postgresPercentile95(values []float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sort.Float64s(values)
	index := int(float64(len(values))*0.95+0.999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index], true
}

func (p *Postgres) HourlyDistributionWithFilter(ctx context.Context, filter store.AnalyticsFilter, location *time.Location) ([]store.HourlyPoint, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	pointsByHour := make(map[int]*store.HourlyPoint)
	for _, event := range events {
		hour := time.UnixMilli(event.TimestampMS).In(location).Hour()
		point := pointsByHour[hour]
		if point == nil {
			point = &store.HourlyPoint{Hour: hour}
			pointsByHour[hour] = point
		}
		point.Calls++
		point.Tokens += event.TotalTokens
	}
	hours := make([]int, 0, len(pointsByHour))
	for hour := range pointsByHour {
		hours = append(hours, hour)
	}
	sort.Ints(hours)
	result := make([]store.HourlyPoint, 0, len(hours))
	for _, hour := range hours {
		result = append(result, *pointsByHour[hour])
	}
	return result, nil
}

type postgresHeatmapKey struct {
	weekday      int
	hour         int
	model        string
	billingModel string
	serviceTier  string
	apiKeyHash   string
	provider     string
}

func (p *Postgres) HeatmapWithFilter(ctx context.Context, filter store.AnalyticsFilter, location *time.Location) ([]store.HeatmapPoint, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	type heatmapGroup struct {
		key postgresHeatmapKey
		postgresMetricTotals
	}
	groups := make(map[postgresHeatmapKey]*heatmapGroup)
	for _, event := range events {
		tm := time.UnixMilli(event.TimestampMS).In(location)
		key := postgresHeatmapKey{int(tm.Weekday()), tm.Hour(), event.Model, event.BillingModel, event.ServiceTier, event.APIKeyHash, event.Provider}
		group := groups[key]
		if group == nil {
			group = &heatmapGroup{key: key}
			groups[key] = group
		}
		group.add(event)
	}
	keys := make([]postgresHeatmapKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].weekday != keys[j].weekday {
			return keys[i].weekday < keys[j].weekday
		}
		if keys[i].hour != keys[j].hour {
			return keys[i].hour < keys[j].hour
		}
		return keys[i].model < keys[j].model
	})
	result := make([]store.HeatmapPoint, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		result = append(result, store.HeatmapPoint{
			LongContextTokens:   group.LongContextTokens,
			Weekday:             key.weekday,
			Hour:                key.hour,
			Model:               key.model,
			BillingModel:        key.billingModel,
			ServiceTier:         key.serviceTier,
			APIKeyHash:          key.apiKeyHash,
			Provider:            key.provider,
			Calls:               group.calls,
			SuccessCalls:        group.success,
			FailureCalls:        group.failure,
			InputTokens:         group.inputTokens,
			OutputTokens:        group.outputTokens,
			CachedTokens:        group.cachedTokens,
			CacheReadTokens:     group.cacheReadTokens,
			CacheCreationTokens: group.cacheCreation,
			TotalTokens:         group.totalTokens,
		})
	}
	return result, nil
}

func maxPostgresString(current, candidate string) string {
	if candidate > current {
		return candidate
	}
	return current
}

type postgresChannelKey struct {
	authIndex    string
	model        string
	billingModel string
	serviceTier  string
}

type postgresChannelGroup struct {
	key postgresChannelKey
	postgresMetricTotals
	source               string
	accountSnapshot      string
	authLabelSnapshot    string
	authProviderSnapshot string
}

func (p *Postgres) ChannelModelStatsWithFilter(ctx context.Context, filter store.AnalyticsFilter) ([]store.ChannelModelStat, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select
		coalesce(auth_index, ''),
		coalesce(max(source), ''),
		coalesce(max(account_snapshot), ''),
		coalesce(max(auth_label_snapshot), ''),
		max(` + postgresProviderExpr + `),
		model,
		` + postgresBillingModelExpr + `,
		` + postgresServiceTierExpr + `,
		count(*),
		count(*) filter (where not failed),
		count(*) filter (where failed),
		coalesce(sum(` + postgresNormalizedInputExpr + `), 0),
		coalesce(sum(coalesce(output_tokens, 0)), 0),
		coalesce(sum(` + postgresCachedTokensExpr + `), 0),
		coalesce(sum(coalesce(cache_read_tokens, 0)), 0),
		coalesce(sum(coalesce(cache_creation_tokens, 0)), 0),
		coalesce(sum(` + postgresLongInputExpr + `), 0),
		coalesce(sum(` + postgresLongOutputExpr + `), 0),
		coalesce(sum(` + postgresLongCachedExpr + `), 0),
		coalesce(sum(` + postgresLongCacheReadExpr + `), 0),
		coalesce(sum(` + postgresLongCacheCreateExpr + `), 0),
		coalesce(sum(coalesce(total_tokens, 0)), 0),
		avg(nullif(latency_ms, 0)),
		count(nullif(latency_ms, 0))
	from cpamp_usage.usage_events ` + where + `
	group by auth_index, model, ` + postgresBillingModelExpr + `, ` + postgresServiceTierExpr + `
	order by count(*) desc`
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL channel model stats: %w", err)
	}
	defer rows.Close()
	result := make([]store.ChannelModelStat, 0)
	for rows.Next() {
		var stat store.ChannelModelStat
		if err := rows.Scan(
			&stat.AuthIndex, &stat.Source, &stat.AccountSnapshot, &stat.AuthLabelSnapshot,
			&stat.AuthProviderSnapshot, &stat.Model, &stat.BillingModel, &stat.ServiceTier,
			&stat.Calls, &stat.SuccessCalls, &stat.FailureCalls, &stat.InputTokens,
			&stat.OutputTokens, &stat.CachedTokens, &stat.CacheReadTokens, &stat.CacheCreationTokens,
			&stat.LongInputTokens, &stat.LongOutputTokens, &stat.LongCachedTokens,
			&stat.LongCacheReadTokens, &stat.LongCacheCreationTokens, &stat.TotalTokens,
			&stat.AvgLatencyMS, &stat.LatencySamples,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL channel model stats: %w", err)
		}
		result = append(result, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL channel model stats: %w", err)
	}
	return result, nil
}

type postgresFailureKey struct {
	sourceHash string
	authIndex  string
}

type postgresFailureGroup struct {
	key postgresFailureKey
	postgresMetricTotals
	source               string
	accountSnapshot      string
	authLabelSnapshot    string
	authProviderSnapshot string
}

func (p *Postgres) FailureSourcesWithFilter(ctx context.Context, filter store.AnalyticsFilter) ([]store.FailureSourceStat, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select
		coalesce(max(source), ''),
		coalesce(source_hash, ''),
		coalesce(auth_index, ''),
		coalesce(max(account_snapshot), ''),
		coalesce(max(auth_label_snapshot), ''),
		max(` + postgresProviderExpr + `),
		count(*),
		count(*) filter (where failed),
		max(timestamp_ms),
		avg(nullif(latency_ms, 0))
	from cpamp_usage.usage_events ` + where + `
	group by source_hash, auth_index
	having count(*) filter (where failed) > 0
	order by count(*) filter (where failed) desc, max(timestamp_ms) desc`
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL failure sources: %w", err)
	}
	defer rows.Close()
	result := make([]store.FailureSourceStat, 0)
	for rows.Next() {
		var stat store.FailureSourceStat
		if err := rows.Scan(
			&stat.Source, &stat.SourceHash, &stat.AuthIndex, &stat.AccountSnapshot,
			&stat.AuthLabelSnapshot, &stat.AuthProviderSnapshot, &stat.Calls,
			&stat.FailureCalls, &stat.LastSeenMS, &stat.AvgLatencyMS,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL failure source: %w", err)
		}
		result = append(result, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL failure sources: %w", err)
	}
	return result, nil
}

type postgresAccountKey struct {
	accountSnapshot      string
	authLabelSnapshot    string
	authProviderSnapshot string
	authIndex            string
	sourceHash           string
	model                string
	billingModel         string
	serviceTier          string
}

type postgresAccountGroup struct {
	key postgresAccountKey
	postgresMetricTotals
	source string
}

func (p *Postgres) accountModelStatsFromEvents(ctx context.Context, filter store.AnalyticsFilter) ([]store.AccountModelStat, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[postgresAccountKey]*postgresAccountGroup)
	for _, event := range events {
		key := postgresAccountKey{event.AccountSnapshot, event.AuthLabelSnapshot, event.Provider, event.AuthIndex, event.SourceHash, event.Model, event.BillingModel, event.ServiceTier}
		group := groups[key]
		if group == nil {
			group = &postgresAccountGroup{key: key}
			groups[key] = group
		}
		group.add(event)
		group.source = maxPostgresString(group.source, event.Source)
	}
	result := make([]store.AccountModelStat, 0, len(groups))
	for _, group := range groups {
		result = append(result, store.AccountModelStat{
			LongContextTokens:    group.LongContextTokens,
			AccountSnapshot:      group.key.accountSnapshot,
			AuthLabelSnapshot:    group.key.authLabelSnapshot,
			AuthProviderSnapshot: group.key.authProviderSnapshot,
			AuthIndex:            group.key.authIndex,
			Source:               group.source,
			SourceHash:           group.key.sourceHash,
			Model:                group.key.model,
			BillingModel:         group.key.billingModel,
			ServiceTier:          group.key.serviceTier,
			Calls:                group.calls,
			SuccessCalls:         group.success,
			FailureCalls:         group.failure,
			InputTokens:          group.inputTokens,
			OutputTokens:         group.outputTokens,
			CachedTokens:         group.cachedTokens,
			CacheReadTokens:      group.cacheReadTokens,
			CacheCreationTokens:  group.cacheCreation,
			TotalTokens:          group.totalTokens,
			LastSeenMS:           group.lastMS,
			AvgLatencyMS:         group.averageLatency(),
			LatencySamples:       group.latencySamples,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].LastSeenMS > result[j].LastSeenMS || (result[i].LastSeenMS == result[j].LastSeenMS && result[i].Calls > result[j].Calls)
	})
	return result, nil
}

func (p *Postgres) AccountModelStatsWithFilter(ctx context.Context, filter store.AnalyticsFilter) ([]store.AccountModelStat, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select
		coalesce(account_snapshot, ''),
		coalesce(auth_label_snapshot, ''),
		` + postgresProviderExpr + `,
		coalesce(auth_index, ''),
		coalesce(max(source), ''),
		coalesce(source_hash, ''),
		model,
		` + postgresBillingModelExpr + `,
		` + postgresServiceTierExpr + `,
		count(*),
		count(*) filter (where not failed),
		count(*) filter (where failed),
		coalesce(sum(` + postgresNormalizedInputExpr + `), 0),
		coalesce(sum(coalesce(output_tokens, 0)), 0),
		coalesce(sum(` + postgresCachedTokensExpr + `), 0),
		coalesce(sum(coalesce(cache_read_tokens, 0)), 0),
		coalesce(sum(coalesce(cache_creation_tokens, 0)), 0),
		coalesce(sum(` + postgresLongInputExpr + `), 0),
		coalesce(sum(` + postgresLongOutputExpr + `), 0),
		coalesce(sum(` + postgresLongCachedExpr + `), 0),
		coalesce(sum(` + postgresLongCacheReadExpr + `), 0),
		coalesce(sum(` + postgresLongCacheCreateExpr + `), 0),
		coalesce(sum(coalesce(total_tokens, 0)), 0),
		max(timestamp_ms),
		avg(nullif(latency_ms, 0)),
		count(nullif(latency_ms, 0))
	from cpamp_usage.usage_events ` + where + `
	group by account_snapshot, auth_label_snapshot, ` + postgresProviderExpr + `, auth_index, source_hash, model, ` + postgresBillingModelExpr + `, ` + postgresServiceTierExpr + `
	order by max(timestamp_ms) desc, count(*) desc`
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL account model stats: %w", err)
	}
	defer rows.Close()
	result := make([]store.AccountModelStat, 0)
	for rows.Next() {
		var stat store.AccountModelStat
		if err := rows.Scan(
			&stat.AccountSnapshot, &stat.AuthLabelSnapshot, &stat.AuthProviderSnapshot,
			&stat.AuthIndex, &stat.Source, &stat.SourceHash, &stat.Model, &stat.BillingModel,
			&stat.ServiceTier, &stat.Calls, &stat.SuccessCalls, &stat.FailureCalls,
			&stat.InputTokens, &stat.OutputTokens, &stat.CachedTokens, &stat.CacheReadTokens,
			&stat.CacheCreationTokens, &stat.LongInputTokens, &stat.LongOutputTokens,
			&stat.LongCachedTokens, &stat.LongCacheReadTokens, &stat.LongCacheCreationTokens,
			&stat.TotalTokens, &stat.LastSeenMS, &stat.AvgLatencyMS, &stat.LatencySamples,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL account model stats: %w", err)
		}
		result = append(result, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL account model stats: %w", err)
	}
	return result, nil
}

func postgresCredentialID(event postgresAnalyticsEvent) string {
	for _, value := range []string{event.AuthFileSnapshot, event.AuthIndex, event.SourceHash, event.Source} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "-"
}

type postgresCredentialKey struct {
	id               string
	authFileSnapshot string
	authIndex        string
	sourceHash       string
	model            string
	billingModel     string
	serviceTier      string
}

type postgresCredentialGroup struct {
	key postgresCredentialKey
	postgresMetricTotals
	source                string
	accountSnapshot       string
	authLabelSnapshot     string
	authProviderSnapshot  string
	authProjectIDSnapshot string
}

func (p *Postgres) CredentialModelStatsWithFilter(ctx context.Context, filter store.AnalyticsFilter) ([]store.CredentialModelStat, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[postgresCredentialKey]*postgresCredentialGroup)
	for _, event := range events {
		key := postgresCredentialKey{postgresCredentialID(event), event.AuthFileSnapshot, event.AuthIndex, event.SourceHash, event.Model, event.BillingModel, event.ServiceTier}
		group := groups[key]
		if group == nil {
			group = &postgresCredentialGroup{key: key}
			groups[key] = group
		}
		group.add(event)
		group.source = maxPostgresString(group.source, event.Source)
		group.accountSnapshot = maxPostgresString(group.accountSnapshot, event.AccountSnapshot)
		group.authLabelSnapshot = maxPostgresString(group.authLabelSnapshot, event.AuthLabelSnapshot)
		group.authProviderSnapshot = maxPostgresString(group.authProviderSnapshot, event.Provider)
		group.authProjectIDSnapshot = maxPostgresString(group.authProjectIDSnapshot, event.AuthProjectIDSnapshot)
	}
	result := make([]store.CredentialModelStat, 0, len(groups))
	for _, group := range groups {
		result = append(result, store.CredentialModelStat{
			LongContextTokens:     group.LongContextTokens,
			ID:                    group.key.id,
			AuthFileSnapshot:      group.key.authFileSnapshot,
			AuthIndex:             group.key.authIndex,
			Source:                group.source,
			SourceHash:            group.key.sourceHash,
			AccountSnapshot:       group.accountSnapshot,
			AuthLabelSnapshot:     group.authLabelSnapshot,
			AuthProviderSnapshot:  group.authProviderSnapshot,
			AuthProjectIDSnapshot: group.authProjectIDSnapshot,
			Model:                 group.key.model,
			BillingModel:          group.key.billingModel,
			ServiceTier:           group.key.serviceTier,
			Calls:                 group.calls,
			SuccessCalls:          group.success,
			FailureCalls:          group.failure,
			InputTokens:           group.inputTokens,
			OutputTokens:          group.outputTokens,
			CachedTokens:          group.cachedTokens,
			CacheReadTokens:       group.cacheReadTokens,
			CacheCreationTokens:   group.cacheCreation,
			TotalTokens:           group.totalTokens,
			LastSeenMS:            group.lastMS,
			AvgLatencyMS:          group.averageLatency(),
			LatencySamples:        group.latencySamples,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].LastSeenMS > result[j].LastSeenMS || (result[i].LastSeenMS == result[j].LastSeenMS && result[i].Calls > result[j].Calls)
	})
	return result, nil
}

type postgresCredentialTimelineKey struct {
	id               string
	authFileSnapshot string
	authIndex        string
	sourceHash       string
	bucketMS         int64
	model            string
	billingModel     string
	serviceTier      string
}

type postgresCredentialTimelineGroup struct {
	key postgresCredentialTimelineKey
	postgresMetricTotals
	source                string
	accountSnapshot       string
	authLabelSnapshot     string
	authProviderSnapshot  string
	authProjectIDSnapshot string
}

func (p *Postgres) CredentialTimelineWithFilter(ctx context.Context, filter store.AnalyticsFilter, granularity string, location *time.Location) ([]store.CredentialTimelinePoint, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[postgresCredentialTimelineKey]*postgresCredentialTimelineGroup)
	for _, event := range events {
		key := postgresCredentialTimelineKey{
			id:               postgresCredentialID(event),
			authFileSnapshot: event.AuthFileSnapshot,
			authIndex:        event.AuthIndex,
			sourceHash:       event.SourceHash,
			bucketMS:         usage.AnalyticsBucketMS(event.TimestampMS, granularity, location),
			model:            event.Model,
			billingModel:     event.BillingModel,
			serviceTier:      event.ServiceTier,
		}
		group := groups[key]
		if group == nil {
			group = &postgresCredentialTimelineGroup{key: key}
			groups[key] = group
		}
		group.add(event)
		group.source = maxPostgresString(group.source, event.Source)
		group.accountSnapshot = maxPostgresString(group.accountSnapshot, event.AccountSnapshot)
		group.authLabelSnapshot = maxPostgresString(group.authLabelSnapshot, event.AuthLabelSnapshot)
		group.authProviderSnapshot = maxPostgresString(group.authProviderSnapshot, event.Provider)
		group.authProjectIDSnapshot = maxPostgresString(group.authProjectIDSnapshot, event.AuthProjectIDSnapshot)
	}
	keys := make([]postgresCredentialTimelineKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].bucketMS != keys[j].bucketMS {
			return keys[i].bucketMS < keys[j].bucketMS
		}
		if groups[keys[i]].firstMS != groups[keys[j]].firstMS {
			return groups[keys[i]].firstMS < groups[keys[j]].firstMS
		}
		if keys[i].id != keys[j].id {
			return keys[i].id < keys[j].id
		}
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		if keys[i].billingModel != keys[j].billingModel {
			return keys[i].billingModel < keys[j].billingModel
		}
		return keys[i].serviceTier < keys[j].serviceTier
	})
	result := make([]store.CredentialTimelinePoint, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		result = append(result, store.CredentialTimelinePoint{
			LongContextTokens:     group.LongContextTokens,
			ID:                    key.id,
			AuthFileSnapshot:      key.authFileSnapshot,
			AuthIndex:             key.authIndex,
			Source:                group.source,
			SourceHash:            key.sourceHash,
			AccountSnapshot:       group.accountSnapshot,
			AuthLabelSnapshot:     group.authLabelSnapshot,
			AuthProviderSnapshot:  group.authProviderSnapshot,
			AuthProjectIDSnapshot: group.authProjectIDSnapshot,
			BucketMS:              key.bucketMS,
			Model:                 key.model,
			BillingModel:          key.billingModel,
			ServiceTier:           key.serviceTier,
			Calls:                 group.calls,
			Tokens:                group.totalTokens,
			Success:               group.success,
			Failure:               group.failure,
			InputTokens:           group.inputTokens,
			OutputTokens:          group.outputTokens,
			ReasoningTokens:       group.reasoningTokens,
			CachedTokens:          group.cachedTokens,
			CacheReadTokens:       group.cacheReadTokens,
			CacheCreationTokens:   group.cacheCreation,
			AvgLatencyMS:          group.averageLatency(),
			LatencySamples:        group.latencySamples,
		})
	}
	return result, nil
}

type postgresAPIKeyKey struct {
	apiKeyHash           string
	accountSnapshot      string
	authLabelSnapshot    string
	authProviderSnapshot string
	authIndex            string
	sourceHash           string
	model                string
	billingModel         string
	serviceTier          string
}

type postgresAPIKeyGroup struct {
	key postgresAPIKeyKey
	postgresMetricTotals
	source string
}

func (p *Postgres) apiKeyModelStatsFromEvents(ctx context.Context, filter store.AnalyticsFilter) ([]store.APIKeyModelStat, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[postgresAPIKeyKey]*postgresAPIKeyGroup)
	for _, event := range events {
		key := postgresAPIKeyKey{event.APIKeyHash, event.AccountSnapshot, event.AuthLabelSnapshot, event.Provider, event.AuthIndex, event.SourceHash, event.Model, event.BillingModel, event.ServiceTier}
		group := groups[key]
		if group == nil {
			group = &postgresAPIKeyGroup{key: key}
			groups[key] = group
		}
		group.add(event)
		group.source = maxPostgresString(group.source, event.Source)
	}
	result := make([]store.APIKeyModelStat, 0, len(groups))
	for _, group := range groups {
		result = append(result, store.APIKeyModelStat{
			LongContextTokens:    group.LongContextTokens,
			APIKeyHash:           group.key.apiKeyHash,
			AccountSnapshot:      group.key.accountSnapshot,
			AuthLabelSnapshot:    group.key.authLabelSnapshot,
			AuthProviderSnapshot: group.key.authProviderSnapshot,
			AuthIndex:            group.key.authIndex,
			Source:               group.source,
			SourceHash:           group.key.sourceHash,
			Model:                group.key.model,
			BillingModel:         group.key.billingModel,
			ServiceTier:          group.key.serviceTier,
			Calls:                group.calls,
			SuccessCalls:         group.success,
			FailureCalls:         group.failure,
			InputTokens:          group.inputTokens,
			OutputTokens:         group.outputTokens,
			CachedTokens:         group.cachedTokens,
			CacheReadTokens:      group.cacheReadTokens,
			CacheCreationTokens:  group.cacheCreation,
			TotalTokens:          group.totalTokens,
			LastSeenMS:           group.lastMS,
			AvgLatencyMS:         group.averageLatency(),
			LatencySamples:       group.latencySamples,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].LastSeenMS > result[j].LastSeenMS || (result[i].LastSeenMS == result[j].LastSeenMS && result[i].Calls > result[j].Calls)
	})
	return result, nil
}

func (p *Postgres) APIKeyModelStatsWithFilter(ctx context.Context, filter store.AnalyticsFilter) ([]store.APIKeyModelStat, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select
		coalesce(api_key_hash, ''),
		coalesce(account_snapshot, ''),
		coalesce(auth_label_snapshot, ''),
		` + postgresProviderExpr + `,
		coalesce(auth_index, ''),
		coalesce(max(source), ''),
		coalesce(source_hash, ''),
		model,
		` + postgresBillingModelExpr + `,
		` + postgresServiceTierExpr + `,
		count(*),
		count(*) filter (where not failed),
		count(*) filter (where failed),
		coalesce(sum(` + postgresNormalizedInputExpr + `), 0),
		coalesce(sum(coalesce(output_tokens, 0)), 0),
		coalesce(sum(` + postgresCachedTokensExpr + `), 0),
		coalesce(sum(coalesce(cache_read_tokens, 0)), 0),
		coalesce(sum(coalesce(cache_creation_tokens, 0)), 0),
		coalesce(sum(` + postgresLongInputExpr + `), 0),
		coalesce(sum(` + postgresLongOutputExpr + `), 0),
		coalesce(sum(` + postgresLongCachedExpr + `), 0),
		coalesce(sum(` + postgresLongCacheReadExpr + `), 0),
		coalesce(sum(` + postgresLongCacheCreateExpr + `), 0),
		coalesce(sum(coalesce(total_tokens, 0)), 0),
		max(timestamp_ms),
		avg(nullif(latency_ms, 0)),
		count(nullif(latency_ms, 0))
	from cpamp_usage.usage_events ` + where + `
	group by api_key_hash, account_snapshot, auth_label_snapshot, ` + postgresProviderExpr + `, auth_index, source_hash, model, ` + postgresBillingModelExpr + `, ` + postgresServiceTierExpr + `
	order by max(timestamp_ms) desc, count(*) desc`
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL API key model stats: %w", err)
	}
	defer rows.Close()
	result := make([]store.APIKeyModelStat, 0)
	for rows.Next() {
		var stat store.APIKeyModelStat
		if err := rows.Scan(
			&stat.APIKeyHash, &stat.AccountSnapshot, &stat.AuthLabelSnapshot, &stat.AuthProviderSnapshot,
			&stat.AuthIndex, &stat.Source, &stat.SourceHash, &stat.Model, &stat.BillingModel,
			&stat.ServiceTier, &stat.Calls, &stat.SuccessCalls, &stat.FailureCalls,
			&stat.InputTokens, &stat.OutputTokens, &stat.CachedTokens, &stat.CacheReadTokens,
			&stat.CacheCreationTokens, &stat.LongInputTokens, &stat.LongOutputTokens,
			&stat.LongCachedTokens, &stat.LongCacheReadTokens, &stat.LongCacheCreationTokens,
			&stat.TotalTokens, &stat.LastSeenMS, &stat.AvgLatencyMS, &stat.LatencySamples,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL API key model stats: %w", err)
		}
		result = append(result, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL API key model stats: %w", err)
	}
	return result, nil
}

func sortedPostgresSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func addPostgresNonEmpty(values map[string]struct{}, value string) {
	if strings.TrimSpace(value) != "" {
		values[value] = struct{}{}
	}
}

func (p *Postgres) filterOptionValuesFromEvents(ctx context.Context, filter store.AnalyticsFilter) (store.FilterOptionValues, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return store.FilterOptionValues{}, err
	}
	providers := map[string]struct{}{}
	authFiles := map[string]struct{}{}
	projectIDs := map[string]struct{}{}
	requestTypes := map[string]struct{}{}
	headerErrorKinds := map[string]struct{}{}
	headerErrorCodes := map[string]struct{}{}
	headerQuotaPlans := map[string]struct{}{}
	headerTraceIDs := map[string]struct{}{}
	for _, event := range events {
		addPostgresNonEmpty(providers, event.Provider)
		addPostgresNonEmpty(authFiles, event.AuthFileSnapshot)
		addPostgresNonEmpty(projectIDs, event.AuthProjectIDSnapshot)
		addPostgresNonEmpty(requestTypes, event.RequestType)
		addPostgresNonEmpty(headerErrorKinds, event.HeaderErrorKind)
		addPostgresNonEmpty(headerErrorCodes, event.HeaderErrorCode)
		addPostgresNonEmpty(headerQuotaPlans, event.HeaderQuotaPlanType)
		addPostgresNonEmpty(headerTraceIDs, event.HeaderTraceID)
	}
	return store.FilterOptionValues{
		Providers:        sortedPostgresSet(providers),
		AuthFiles:        sortedPostgresSet(authFiles),
		ProjectIDs:       sortedPostgresSet(projectIDs),
		RequestTypes:     sortedPostgresSet(requestTypes),
		HeaderErrorKinds: sortedPostgresSet(headerErrorKinds),
		HeaderErrorCodes: sortedPostgresSet(headerErrorCodes),
		HeaderQuotaPlans: sortedPostgresSet(headerQuotaPlans),
		HeaderTraceIDs:   sortedPostgresSet(headerTraceIDs),
	}, nil
}

func (p *Postgres) FilterOptionValuesWithFilter(ctx context.Context, filter store.AnalyticsFilter) (store.FilterOptionValues, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select
		coalesce(jsonb_agg(distinct ` + postgresProviderExpr + ` order by ` + postgresProviderExpr + `) filter (where btrim(` + postgresProviderExpr + `) <> ''), '[]'::jsonb),
		coalesce(jsonb_agg(distinct coalesce(auth_file_snapshot, '') order by coalesce(auth_file_snapshot, '')) filter (where btrim(coalesce(auth_file_snapshot, '')) <> ''), '[]'::jsonb),
		coalesce(jsonb_agg(distinct coalesce(auth_project_id_snapshot, '') order by coalesce(auth_project_id_snapshot, '')) filter (where btrim(coalesce(auth_project_id_snapshot, '')) <> ''), '[]'::jsonb),
		coalesce(jsonb_agg(distinct coalesce(executor_type, '') order by coalesce(executor_type, '')) filter (where btrim(coalesce(executor_type, '')) <> ''), '[]'::jsonb),
		coalesce(jsonb_agg(distinct coalesce(header_error_kind, '') order by coalesce(header_error_kind, '')) filter (where btrim(coalesce(header_error_kind, '')) <> ''), '[]'::jsonb),
		coalesce(jsonb_agg(distinct coalesce(header_error_code, '') order by coalesce(header_error_code, '')) filter (where btrim(coalesce(header_error_code, '')) <> ''), '[]'::jsonb),
		coalesce(jsonb_agg(distinct coalesce(header_quota_plan_type, '') order by coalesce(header_quota_plan_type, '')) filter (where btrim(coalesce(header_quota_plan_type, '')) <> ''), '[]'::jsonb),
		coalesce(jsonb_agg(distinct coalesce(header_trace_id, '') order by coalesce(header_trace_id, '')) filter (where btrim(coalesce(header_trace_id, '')) <> ''), '[]'::jsonb)
	from cpamp_usage.usage_events ` + where
	var providersJSON, authFilesJSON, projectIDsJSON, requestTypesJSON []byte
	var headerErrorKindsJSON, headerErrorCodesJSON, headerQuotaPlansJSON, headerTraceIDsJSON []byte
	if err := p.Pool.QueryRow(ctx, query, args...).Scan(
		&providersJSON, &authFilesJSON, &projectIDsJSON, &requestTypesJSON,
		&headerErrorKindsJSON, &headerErrorCodesJSON, &headerQuotaPlansJSON, &headerTraceIDsJSON,
	); err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("read PostgreSQL filter option values: %w", err)
	}
	decode := func(payload []byte) ([]string, error) {
		var values []string
		if err := json.Unmarshal(payload, &values); err != nil {
			return nil, err
		}
		return values, nil
	}
	providers, err := decode(providersJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL providers: %w", err)
	}
	authFiles, err := decode(authFilesJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL auth files: %w", err)
	}
	projectIDs, err := decode(projectIDsJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL project IDs: %w", err)
	}
	requestTypes, err := decode(requestTypesJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL request types: %w", err)
	}
	headerErrorKinds, err := decode(headerErrorKindsJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL header error kinds: %w", err)
	}
	headerErrorCodes, err := decode(headerErrorCodesJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL header error codes: %w", err)
	}
	headerQuotaPlans, err := decode(headerQuotaPlansJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL header quota plans: %w", err)
	}
	headerTraceIDs, err := decode(headerTraceIDsJSON)
	if err != nil {
		return store.FilterOptionValues{}, fmt.Errorf("decode PostgreSQL header trace IDs: %w", err)
	}
	return store.FilterOptionValues{
		Providers:        providers,
		AuthFiles:        authFiles,
		ProjectIDs:       projectIDs,
		RequestTypes:     requestTypes,
		HeaderErrorKinds: headerErrorKinds,
		HeaderErrorCodes: headerErrorCodes,
		HeaderQuotaPlans: headerQuotaPlans,
		HeaderTraceIDs:   headerTraceIDs,
	}, nil
}

func (p *Postgres) FilterSelectorValuesWithFilter(ctx context.Context, filter store.AnalyticsFilter) (store.FilterSelectorValues, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return store.FilterSelectorValues{}, err
	}
	models := map[string]struct{}{}
	apiKeyHashes := map[string]struct{}{}
	providers := map[string]struct{}{}
	authFiles := map[string]struct{}{}
	accounts := map[string]struct{}{}
	type accountSelectorKey struct {
		accountSnapshot      string
		authLabelSnapshot    string
		authProviderSnapshot string
		authIndex            string
		sourceHash           string
	}
	type accountSelectorGroup struct {
		value  usageevent.AccountSelectorValue
		source string
	}
	type apiKeySelectorKey struct {
		apiKeyHash           string
		authProviderSnapshot string
		authIndex            string
		sourceHash           string
	}
	type apiKeySelectorGroup struct {
		value  usageevent.APIKeySelectorValue
		source string
	}
	accountSelectors := map[accountSelectorKey]*accountSelectorGroup{}
	apiKeySelectors := map[apiKeySelectorKey]*apiKeySelectorGroup{}
	for _, event := range events {
		addPostgresNonEmpty(models, event.Model)
		addPostgresNonEmpty(apiKeyHashes, strings.ToLower(strings.TrimSpace(event.APIKeyHash)))
		addPostgresNonEmpty(providers, event.Provider)
		addPostgresNonEmpty(authFiles, event.AuthFileSnapshot)
		if account := strings.TrimSpace(event.AccountSnapshot); account != "" {
			accounts[account] = struct{}{}
		}
		accountKey := accountSelectorKey{
			accountSnapshot:      event.AccountSnapshot,
			authLabelSnapshot:    event.AuthLabelSnapshot,
			authProviderSnapshot: event.Provider,
			authIndex:            event.AuthIndex,
			sourceHash:           event.SourceHash,
		}
		if accountKey.accountSnapshot != "" || accountKey.authLabelSnapshot != "" || accountKey.authIndex != "" || event.Source != "" || accountKey.sourceHash != "" {
			group := accountSelectors[accountKey]
			if group == nil {
				group = &accountSelectorGroup{value: usageevent.AccountSelectorValue{
					AccountSnapshot:      accountKey.accountSnapshot,
					AuthLabelSnapshot:    accountKey.authLabelSnapshot,
					AuthProviderSnapshot: accountKey.authProviderSnapshot,
					AuthIndex:            accountKey.authIndex,
					SourceHash:           accountKey.sourceHash,
				}}
				accountSelectors[accountKey] = group
			}
			group.source = maxPostgresString(group.source, event.Source)
			group.value.Source = group.source
		}
		apiKeyKey := apiKeySelectorKey{
			apiKeyHash:           event.APIKeyHash,
			authProviderSnapshot: event.Provider,
			authIndex:            event.AuthIndex,
			sourceHash:           event.SourceHash,
		}
		group := apiKeySelectors[apiKeyKey]
		if group == nil {
			group = &apiKeySelectorGroup{value: usageevent.APIKeySelectorValue{
				APIKeyHash:           apiKeyKey.apiKeyHash,
				AuthProviderSnapshot: apiKeyKey.authProviderSnapshot,
				AuthIndex:            apiKeyKey.authIndex,
				SourceHash:           apiKeyKey.sourceHash,
			}}
			apiKeySelectors[apiKeyKey] = group
		}
		group.source = maxPostgresString(group.source, event.Source)
		group.value.Source = group.source
	}
	accountResult := make([]usageevent.AccountSelectorValue, 0, len(accountSelectors))
	for _, group := range accountSelectors {
		accountResult = append(accountResult, group.value)
	}
	sort.Slice(accountResult, func(i, j int) bool {
		if accountResult[i].AccountSnapshot != accountResult[j].AccountSnapshot {
			return accountResult[i].AccountSnapshot < accountResult[j].AccountSnapshot
		}
		if accountResult[i].AuthLabelSnapshot != accountResult[j].AuthLabelSnapshot {
			return accountResult[i].AuthLabelSnapshot < accountResult[j].AuthLabelSnapshot
		}
		if accountResult[i].Source != accountResult[j].Source {
			return accountResult[i].Source < accountResult[j].Source
		}
		if accountResult[i].AuthIndex != accountResult[j].AuthIndex {
			return accountResult[i].AuthIndex < accountResult[j].AuthIndex
		}
		return accountResult[i].SourceHash < accountResult[j].SourceHash
	})
	apiKeyResult := make([]usageevent.APIKeySelectorValue, 0, len(apiKeySelectors))
	for _, group := range apiKeySelectors {
		apiKeyResult = append(apiKeyResult, group.value)
	}
	sort.Slice(apiKeyResult, func(i, j int) bool {
		if apiKeyResult[i].APIKeyHash != apiKeyResult[j].APIKeyHash {
			return apiKeyResult[i].APIKeyHash < apiKeyResult[j].APIKeyHash
		}
		if apiKeyResult[i].SourceHash != apiKeyResult[j].SourceHash {
			return apiKeyResult[i].SourceHash < apiKeyResult[j].SourceHash
		}
		if apiKeyResult[i].AuthIndex != apiKeyResult[j].AuthIndex {
			return apiKeyResult[i].AuthIndex < apiKeyResult[j].AuthIndex
		}
		if apiKeyResult[i].Source != apiKeyResult[j].Source {
			return apiKeyResult[i].Source < apiKeyResult[j].Source
		}
		return apiKeyResult[i].AuthProviderSnapshot < apiKeyResult[j].AuthProviderSnapshot
	})
	return store.FilterSelectorValues{
		Models:           sortedPostgresSet(models),
		APIKeyHashes:     sortedPostgresSet(apiKeyHashes),
		Providers:        sortedPostgresSet(providers),
		AuthFiles:        sortedPostgresSet(authFiles),
		Accounts:         sortedPostgresSet(accounts),
		AccountSelectors: accountResult,
		APIKeySelectors:  apiKeyResult,
	}, nil
}

type postgresTaskGroup struct {
	key string
	postgresMetricTotals
	source     string
	sourceHash string
	authIndex  string
	models     map[string]struct{}
	endpoints  map[string]struct{}
}

func (p *Postgres) taskBucketsFromEvents(ctx context.Context, filter store.AnalyticsFilter) ([]store.TaskBucket, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	groups := make(map[string]*postgresTaskGroup)
	for _, event := range events {
		key := event.Timestamp + "|" + event.SourceHash + "|" + event.AuthIndex
		group := groups[key]
		if group == nil {
			group = &postgresTaskGroup{key: key, sourceHash: event.SourceHash, authIndex: event.AuthIndex, models: map[string]struct{}{}, endpoints: map[string]struct{}{}}
			groups[key] = group
		}
		group.add(event)
		group.source = maxPostgresString(group.source, event.Source)
		addPostgresNonEmpty(group.models, event.Model)
		addPostgresNonEmpty(group.endpoints, event.Endpoint)
	}
	result := make([]store.TaskBucket, 0, len(groups))
	for _, group := range groups {
		result = append(result, store.TaskBucket{
			BucketKey:           group.key,
			Total:               group.calls,
			Success:             group.success,
			Failure:             group.failure,
			FirstMS:             group.firstMS,
			LastMS:              group.lastMS,
			Source:              group.source,
			SourceHash:          group.sourceHash,
			AuthIndex:           group.authIndex,
			Models:              strings.Join(sortedPostgresSet(group.models), ","),
			Endpoints:           strings.Join(sortedPostgresSet(group.endpoints), ","),
			InputTokens:         group.inputTokens,
			OutputTokens:        group.outputTokens,
			CachedTokens:        group.cachedTokens,
			CacheReadTokens:     group.cacheReadTokens,
			CacheCreationTokens: group.cacheCreation,
			TotalTokens:         group.totalTokens,
			AvgLatencyMS:        group.averageLatency(),
			MaxLatencyMS:        group.maxLatencyMS,
		})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].LastMS > result[j].LastMS })
	if len(result) > 500 {
		result = result[:500]
	}
	return result, nil
}

func (p *Postgres) TaskBucketsWithFilter(ctx context.Context, filter store.AnalyticsFilter) ([]store.TaskBucket, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	// string_agg is PostgreSQL's ordered equivalent of the legacy SQLite
	// group_concat path. Aggregating the task buckets in the database avoids
	// decoding every event just to build at most 500 rows.
	query := `select
		coalesce(timestamp, '') || '|' || coalesce(source_hash, '') || '|' || coalesce(auth_index, '') as bucket_key,
		count(*),
		count(*) filter (where not failed),
		count(*) filter (where failed),
		min(timestamp_ms),
		max(timestamp_ms),
		coalesce(max(source), ''),
		coalesce(source_hash, ''),
		coalesce(auth_index, ''),
		coalesce(string_agg(distinct model, ',' order by model) filter (where btrim(coalesce(model, '')) <> ''), ''),
		coalesce(string_agg(distinct endpoint, ',' order by endpoint) filter (where btrim(coalesce(endpoint, '')) <> ''), ''),
		coalesce(sum(` + postgresNormalizedInputExpr + `), 0),
		coalesce(sum(coalesce(output_tokens, 0)), 0),
		coalesce(sum(` + postgresCachedTokensExpr + `), 0),
		coalesce(sum(coalesce(cache_read_tokens, 0)), 0),
		coalesce(sum(coalesce(cache_creation_tokens, 0)), 0),
		coalesce(sum(coalesce(total_tokens, 0)), 0),
		avg(nullif(latency_ms, 0)),
		max(latency_ms)
	from cpamp_usage.usage_events ` + where + `
	group by bucket_key, source_hash, auth_index
	order by max(timestamp_ms) desc
	limit 500`
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL task buckets: %w", err)
	}
	defer rows.Close()
	result := make([]store.TaskBucket, 0)
	for rows.Next() {
		var bucket store.TaskBucket
		if err := rows.Scan(
			&bucket.BucketKey, &bucket.Total, &bucket.Success, &bucket.Failure,
			&bucket.FirstMS, &bucket.LastMS, &bucket.Source, &bucket.SourceHash,
			&bucket.AuthIndex, &bucket.Models, &bucket.Endpoints, &bucket.InputTokens,
			&bucket.OutputTokens, &bucket.CachedTokens, &bucket.CacheReadTokens,
			&bucket.CacheCreationTokens, &bucket.TotalTokens, &bucket.AvgLatencyMS,
			&bucket.MaxLatencyMS,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL task bucket: %w", err)
		}
		result = append(result, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL task buckets: %w", err)
	}
	return result, nil
}

func (p *Postgres) activeDaysFromEvents(ctx context.Context, filter store.AnalyticsFilter, location *time.Location) (int64, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return 0, err
	}
	activeDays := map[string]struct{}{}
	for _, event := range events {
		activeDays[time.UnixMilli(event.TimestampMS).In(location).Format("2006-01-02")] = struct{}{}
	}
	return int64(len(activeDays)), nil
}

func (p *Postgres) zeroTokenModelsFromEvents(ctx context.Context, filter store.AnalyticsFilter) ([]string, error) {
	events, err := p.loadMonitoringEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	models := map[string]struct{}{}
	for _, event := range events {
		if event.TotalTokens == 0 && !event.Failed && strings.TrimSpace(event.Model) != "" {
			models[event.Model] = struct{}{}
		}
	}
	return sortedPostgresSet(models), nil
}

func (p *Postgres) ActiveDaysWithFilter(ctx context.Context, filter store.AnalyticsFilter, location *time.Location) (int64, error) {
	if location == nil {
		location = time.UTC
	}
	where, args, next := postgresAnalyticsWhere(filter, 1)
	where += fmt.Sprintf(" and timestamp_ms is not null")
	args = append(args, location.String())
	query := `select count(distinct (to_timestamp(timestamp_ms / 1000.0) at time zone $` + fmt.Sprint(next) + `)::date)
	from cpamp_usage.usage_events ` + where
	var days int64
	if err := p.Pool.QueryRow(ctx, query, args...).Scan(&days); err != nil {
		return 0, fmt.Errorf("read PostgreSQL active days: %w", err)
	}
	return days, nil
}

func (p *Postgres) ZeroTokenModelsWithFilter(ctx context.Context, filter store.AnalyticsFilter) ([]string, error) {
	where, args, _ := postgresAnalyticsWhere(filter, 1)
	query := `select distinct coalesce(model, '')
	from cpamp_usage.usage_events ` + where + `
	and total_tokens = 0
	and failed = false
	and btrim(coalesce(model, '')) <> ''
	order by 1`
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL zero-token models: %w", err)
	}
	defer rows.Close()
	models := make([]string, 0)
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL zero-token model: %w", err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL zero-token models: %w", err)
	}
	return models, nil
}

// QuotaWeeklyUsageModelStats aggregates each quota card over its own active
// weekly interval. A JSON recordset keeps this a single PostgreSQL round trip
// even when provider reset times differ for every account.
func (p *Postgres) QuotaWeeklyUsageModelStats(ctx context.Context, targets []store.QuotaWeeklyUsageTarget) ([]store.QuotaWeeklyUsageModelStat, error) {
	if p == nil || p.Pool == nil || len(targets) == 0 {
		return []store.QuotaWeeklyUsageModelStat{}, nil
	}
	payload, err := json.Marshal(targets)
	if err != nil {
		return nil, fmt.Errorf("encode weekly quota usage targets: %w", err)
	}
	query := `with targets as (
		select *
		from jsonb_to_recordset($1::jsonb) as target(
			target_key text,
			auth_file text,
			auth_index text,
			provider text,
			period_start_ms bigint,
			period_end_ms bigint
		)
	)
	select
		target.target_key,
		event.model,
		` + postgresBillingModelExpr + `,
		` + postgresServiceTierExpr + `,
		count(*),
		coalesce(sum(` + postgresNormalizedInputExpr + `), 0),
		coalesce(sum(coalesce(event.output_tokens, 0)), 0),
		coalesce(sum(` + postgresCachedTokensExpr + `), 0),
		coalesce(sum(coalesce(event.cache_read_tokens, 0)), 0),
		coalesce(sum(coalesce(event.cache_creation_tokens, 0)), 0),
		coalesce(sum(` + postgresLongInputExpr + `), 0),
		coalesce(sum(` + postgresLongOutputExpr + `), 0),
		coalesce(sum(` + postgresLongCachedExpr + `), 0),
		coalesce(sum(` + postgresLongCacheReadExpr + `), 0),
		coalesce(sum(` + postgresLongCacheCreateExpr + `), 0),
		coalesce(sum(coalesce(event.total_tokens, 0)), 0),
		max(event.timestamp_ms)
	from targets target
	join cpamp_usage.usage_events event
		on event.timestamp_ms >= target.period_start_ms
		and event.timestamp_ms < target.period_end_ms
		and (
			(
				btrim(coalesce(target.auth_file, '')) <> ''
				and coalesce(event.auth_file_snapshot, '') = target.auth_file
				and (
					btrim(coalesce(target.auth_index, '')) = ''
					or coalesce(event.auth_index, '') = target.auth_index
				)
			)
			or (
				btrim(coalesce(target.auth_index, '')) <> ''
				and coalesce(event.auth_index, '') = target.auth_index
				and (
					btrim(coalesce(target.auth_file, '')) = ''
					or btrim(coalesce(event.auth_file_snapshot, '')) = ''
				)
				and (
					btrim(coalesce(target.provider, '')) = ''
					or lower(coalesce(nullif(event.auth_provider_snapshot, ''), event.provider, '')) = lower(target.provider)
					or (lower(target.provider) = 'claude' and lower(coalesce(nullif(event.auth_provider_snapshot, ''), event.provider, '')) = 'anthropic')
					or (lower(target.provider) in ('xai', 'grok') and lower(coalesce(nullif(event.auth_provider_snapshot, ''), event.provider, '')) in ('xai', 'grok'))
				)
			)
		)
	group by target.target_key, event.model, ` + postgresBillingModelExpr + `, ` + postgresServiceTierExpr + `
	order by target.target_key, max(event.timestamp_ms) desc`
	rows, err := p.Pool.Query(ctx, query, string(payload))
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL weekly quota usage: %w", err)
	}
	defer rows.Close()

	result := make([]store.QuotaWeeklyUsageModelStat, 0)
	for rows.Next() {
		var stat store.QuotaWeeklyUsageModelStat
		if err := rows.Scan(
			&stat.TargetKey,
			&stat.Model,
			&stat.BillingModel,
			&stat.ServiceTier,
			&stat.Calls,
			&stat.InputTokens,
			&stat.OutputTokens,
			&stat.CachedTokens,
			&stat.CacheReadTokens,
			&stat.CacheCreationTokens,
			&stat.LongInputTokens,
			&stat.LongOutputTokens,
			&stat.LongCachedTokens,
			&stat.LongCacheReadTokens,
			&stat.LongCacheCreationTokens,
			&stat.TotalTokens,
			&stat.LastSeenMS,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL weekly quota usage: %w", err)
		}
		result = append(result, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL weekly quota usage: %w", err)
	}
	return result, nil
}
