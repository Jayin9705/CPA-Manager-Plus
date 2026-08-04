package monitoring

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/pricing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	maxQuotaWeeklyUsageTargets = 300
	maxQuotaWeeklyUsagePeriod  = 8 * 24 * time.Hour
	maxQuotaUsageIdentityBytes = 1024
)

var ErrQuotaWeeklyUsageUnavailable = errors.New("weekly quota usage requires the PostgreSQL usage store")

type QuotaWeeklyUsageRequest struct {
	Targets []QuotaWeeklyUsageTarget `json:"targets"`
}

type QuotaWeeklyUsageTarget struct {
	TargetKey     string `json:"target_key"`
	AuthFile      string `json:"auth_file,omitempty"`
	AuthIndex     string `json:"auth_index,omitempty"`
	Provider      string `json:"provider,omitempty"`
	PeriodStartMS int64  `json:"period_start_ms"`
	ResetAtMS     int64  `json:"reset_at_ms,omitempty"`
	PeriodSource  string `json:"period_source,omitempty"`
}

type QuotaWeeklyUsageResponse struct {
	GeneratedAtMS int64                  `json:"generated_at_ms"`
	Items         []QuotaWeeklyUsageItem `json:"items"`
}

type QuotaWeeklyUsageItem struct {
	TargetKey        string  `json:"target_key"`
	PeriodStartMS    int64   `json:"period_start_ms"`
	PeriodEndMS      int64   `json:"period_end_ms"`
	ResetAtMS        int64   `json:"reset_at_ms,omitempty"`
	PeriodSource     string  `json:"period_source"`
	Calls            int64   `json:"calls"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	LastSeenMS       int64   `json:"last_seen_ms,omitempty"`
}

func (s *Service) QuotaWeeklyUsage(ctx context.Context, request QuotaWeeklyUsageRequest) (QuotaWeeklyUsageResponse, error) {
	generatedAtMS := time.Now().UnixMilli()
	targets, err := normalizeQuotaWeeklyUsageTargets(request.Targets, generatedAtMS)
	if err != nil {
		return QuotaWeeklyUsageResponse{}, err
	}
	if s == nil || s.quotaUsageSource == nil {
		return QuotaWeeklyUsageResponse{}, ErrQuotaWeeklyUsageUnavailable
	}

	sourceTargets := make([]store.QuotaWeeklyUsageTarget, 0, len(targets))
	items := make([]QuotaWeeklyUsageItem, 0, len(targets))
	itemsByKey := make(map[string]*QuotaWeeklyUsageItem, len(targets))
	for _, target := range targets {
		sourceTargets = append(sourceTargets, store.QuotaWeeklyUsageTarget{
			TargetKey:     target.TargetKey,
			AuthFile:      target.AuthFile,
			AuthIndex:     target.AuthIndex,
			Provider:      target.Provider,
			PeriodStartMS: target.PeriodStartMS,
			PeriodEndMS:   generatedAtMS,
		})
		items = append(items, QuotaWeeklyUsageItem{
			TargetKey:     target.TargetKey,
			PeriodStartMS: target.PeriodStartMS,
			PeriodEndMS:   generatedAtMS,
			ResetAtMS:     target.ResetAtMS,
			PeriodSource:  target.PeriodSource,
		})
		itemsByKey[target.TargetKey] = &items[len(items)-1]
	}

	queryCtx, cancel := context.WithTimeout(ctx, analyticsQueryTimeout)
	defer cancel()
	stats, err := s.quotaUsageSource.QuotaWeeklyUsageModelStats(queryCtx, sourceTargets)
	if err != nil {
		return QuotaWeeklyUsageResponse{}, err
	}
	prices, err := s.loadModelPrices(queryCtx)
	if err != nil {
		return QuotaWeeklyUsageResponse{}, err
	}
	for _, stat := range stats {
		item := itemsByKey[stat.TargetKey]
		if item == nil {
			continue
		}
		item.Calls += stat.Calls
		item.TotalTokens += stat.TotalTokens
		item.EstimatedCostUSD += pricing.CostForModelCandidatesWithServiceTier(
			[]string{stat.BillingModel, stat.Model},
			stat.ServiceTier,
			pricing.ModelTokens{
				InputTokens:             stat.InputTokens,
				OutputTokens:            stat.OutputTokens,
				CachedTokens:            stat.CachedTokens,
				CacheReadTokens:         stat.CacheReadTokens,
				CacheCreationTokens:     stat.CacheCreationTokens,
				LongInputTokens:         stat.LongInputTokens,
				LongOutputTokens:        stat.LongOutputTokens,
				LongCachedTokens:        stat.LongCachedTokens,
				LongCacheReadTokens:     stat.LongCacheReadTokens,
				LongCacheCreationTokens: stat.LongCacheCreationTokens,
			},
			prices,
		)
		if stat.LastSeenMS > item.LastSeenMS {
			item.LastSeenMS = stat.LastSeenMS
		}
	}

	return QuotaWeeklyUsageResponse{GeneratedAtMS: generatedAtMS, Items: items}, nil
}

func ValidateQuotaWeeklyUsageRequest(request QuotaWeeklyUsageRequest) error {
	_, err := normalizeQuotaWeeklyUsageTargets(request.Targets, time.Now().UnixMilli())
	return err
}

func normalizeQuotaWeeklyUsageTargets(targets []QuotaWeeklyUsageTarget, nowMS int64) ([]QuotaWeeklyUsageTarget, error) {
	if len(targets) == 0 {
		return nil, errors.New("targets are required")
	}
	if len(targets) > maxQuotaWeeklyUsageTargets {
		return nil, fmt.Errorf("targets must be less than or equal to %d", maxQuotaWeeklyUsageTargets)
	}
	seen := make(map[string]struct{}, len(targets))
	normalized := make([]QuotaWeeklyUsageTarget, 0, len(targets))
	for index, target := range targets {
		target.TargetKey = strings.TrimSpace(target.TargetKey)
		target.AuthFile = strings.TrimSpace(target.AuthFile)
		target.AuthIndex = strings.TrimSpace(target.AuthIndex)
		target.Provider = strings.TrimSpace(target.Provider)
		target.PeriodSource = strings.TrimSpace(target.PeriodSource)
		if target.TargetKey == "" {
			return nil, fmt.Errorf("targets[%d].target_key is required", index)
		}
		if _, exists := seen[target.TargetKey]; exists {
			return nil, fmt.Errorf("targets[%d].target_key must be unique", index)
		}
		seen[target.TargetKey] = struct{}{}
		if target.AuthFile == "" && target.AuthIndex == "" {
			return nil, fmt.Errorf("targets[%d] requires auth_file or auth_index", index)
		}
		for field, value := range map[string]string{
			"target_key": target.TargetKey,
			"auth_file":  target.AuthFile,
			"auth_index": target.AuthIndex,
			"provider":   target.Provider,
		} {
			if len(value) > maxQuotaUsageIdentityBytes {
				return nil, fmt.Errorf("targets[%d].%s is too long", index, field)
			}
		}
		if target.PeriodStartMS <= 0 || target.PeriodStartMS > nowMS {
			return nil, fmt.Errorf("targets[%d].period_start_ms must be before the current time", index)
		}
		if nowMS-target.PeriodStartMS > maxQuotaWeeklyUsagePeriod.Milliseconds() {
			return nil, fmt.Errorf("targets[%d] period must not exceed 8 days", index)
		}
		if target.ResetAtMS > 0 {
			if target.ResetAtMS <= target.PeriodStartMS {
				return nil, fmt.Errorf("targets[%d].reset_at_ms must be after period_start_ms", index)
			}
			periodDurationMS := target.ResetAtMS - target.PeriodStartMS
			if periodDurationMS > maxQuotaWeeklyUsagePeriod.Milliseconds() {
				return nil, fmt.Errorf("targets[%d] reset interval must not exceed 8 days", index)
			}
			// A panel can remain open across a quota reset, or submit just as the
			// reset timer fires. Advance an expired boundary before querying so a
			// stale request can never mix the previous and current quota weeks.
			if target.ResetAtMS <= nowMS {
				elapsedPeriods := (nowMS - target.PeriodStartMS) / periodDurationMS
				target.PeriodStartMS += elapsedPeriods * periodDurationMS
				target.ResetAtMS = target.PeriodStartMS + periodDurationMS
			}
		}
		if target.PeriodSource == "" {
			target.PeriodSource = "calendar_week"
		}
		if target.PeriodSource != "calendar_week" && target.PeriodSource != "quota_week" {
			return nil, fmt.Errorf("targets[%d].period_source is invalid", index)
		}
		normalized = append(normalized, target)
	}
	return normalized, nil
}
