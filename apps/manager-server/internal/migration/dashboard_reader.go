package migration

import (
	"context"
	"fmt"
	"sort"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// AggregateBetween exposes the PostgreSQL usage fact table through the legacy
// dashboard contract. The half-open range matches the former store behavior.
func (p *Postgres) AggregateBetween(ctx context.Context, fromMS, toMS int64) (store.Aggregate, error) {
	return p.AggregateWithFilter(ctx, dashboardFilter(fromMS, toMS))
}

func (p *Postgres) ModelStatsBetween(ctx context.Context, fromMS, toMS int64) ([]store.ModelStat, error) {
	return p.ModelStatsWithFilter(ctx, dashboardFilter(fromMS, toMS), 0)
}

func (p *Postgres) TopModelsBetween(ctx context.Context, fromMS, toMS int64, limit int) ([]store.ModelStat, error) {
	if limit <= 0 {
		limit = 5
	}
	return p.ModelStatsWithFilter(ctx, dashboardFilter(fromMS, toMS), limit)
}

func (p *Postgres) RecentFailuresBetween(ctx context.Context, fromMS, toMS int64, limit int) ([]store.RecentFailure, error) {
	return p.RecentFailuresWithFilter(ctx, dashboardFilter(fromMS, toMS), limit)
}

func (p *Postgres) HourlyTimelineBetween(ctx context.Context, fromMS, toMS int64) ([]store.TimelinePoint, error) {
	return p.BucketTimelineBetween(ctx, fromMS, toMS, 60*60*1000)
}

// BucketTimelineBetween keeps buckets relative to fromMS. Dashboard health
// uses ten-minute buckets, which do not map to the monitoring hour/day API.
func (p *Postgres) BucketTimelineBetween(ctx context.Context, fromMS, toMS, bucketMS int64) ([]store.TimelinePoint, error) {
	if bucketMS <= 0 {
		bucketMS = 60 * 60 * 1000
	}
	events, err := p.loadMonitoringEvents(ctx, dashboardFilter(fromMS, toMS))
	if err != nil {
		return nil, err
	}
	pointsByBucket := make(map[int64]*store.TimelinePoint)
	for _, event := range events {
		bucketIndex := (event.TimestampMS - fromMS) / bucketMS
		if bucketIndex < 0 {
			continue
		}
		bucket := fromMS + bucketIndex*bucketMS
		point := pointsByBucket[bucket]
		if point == nil {
			point = &store.TimelinePoint{BucketMS: bucket}
			pointsByBucket[bucket] = point
		}
		point.Calls++
		point.Tokens += event.TotalTokens
		if event.Failed {
			point.Failure++
		} else {
			point.Success++
		}
	}
	buckets := make([]int64, 0, len(pointsByBucket))
	for bucket := range pointsByBucket {
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	result := make([]store.TimelinePoint, 0, len(buckets))
	for _, bucket := range buckets {
		result = append(result, *pointsByBucket[bucket])
	}
	return result, nil
}

func (p *Postgres) CountUsageEvents(ctx context.Context) (int64, error) {
	if p == nil || p.Pool == nil {
		return 0, fmt.Errorf("postgres is not open")
	}
	var count int64
	if err := p.Pool.QueryRow(ctx, `select count(*) from cpamp_usage.usage_events`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count PostgreSQL usage events: %w", err)
	}
	return count, nil
}

func dashboardFilter(fromMS, toMS int64) store.AnalyticsFilter {
	return store.AnalyticsFilter{
		FromMS:        fromMS,
		ToMS:          toMS,
		IncludeFailed: true,
	}
}
