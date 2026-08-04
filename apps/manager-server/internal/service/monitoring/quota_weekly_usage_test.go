package monitoring

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

type quotaWeeklyUsageSourceStub struct {
	targets []store.QuotaWeeklyUsageTarget
	stats   []store.QuotaWeeklyUsageModelStat
	err     error
}

func (source *quotaWeeklyUsageSourceStub) QuotaWeeklyUsageModelStats(_ context.Context, targets []store.QuotaWeeklyUsageTarget) ([]store.QuotaWeeklyUsageModelStat, error) {
	source.targets = append([]store.QuotaWeeklyUsageTarget(nil), targets...)
	return source.stats, source.err
}

func TestQuotaWeeklyUsageAggregatesTokensAndEstimatedCost(t *testing.T) {
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	if err := st.SaveModelPrices(context.Background(), map[string]store.ModelPrice{
		"gpt-priced": {Prompt: 2, Completion: 4},
	}); err != nil {
		t.Fatalf("save prices: %v", err)
	}
	source := &quotaWeeklyUsageSourceStub{stats: []store.QuotaWeeklyUsageModelStat{
		{
			TargetKey:    "codex:account.json::auth-1",
			Model:        "gpt-priced",
			BillingModel: "gpt-priced",
			Calls:        3,
			InputTokens:  1_000_000,
			OutputTokens: 500_000,
			TotalTokens:  1_500_000,
			LastSeenMS:   1234,
		},
	}}
	service := New(st)
	service.SetQuotaWeeklyUsageSource(source)
	now := time.Now()
	startMS := now.Add(-time.Hour).UnixMilli()

	response, err := service.QuotaWeeklyUsage(context.Background(), QuotaWeeklyUsageRequest{
		Targets: []QuotaWeeklyUsageTarget{
			{
				TargetKey:     "codex:account.json::auth-1",
				AuthFile:      "account.json",
				AuthIndex:     "auth-1",
				Provider:      "codex",
				PeriodStartMS: startMS,
				ResetAtMS:     now.Add(7 * 24 * time.Hour).UnixMilli(),
				PeriodSource:  "quota_week",
			},
		},
	})
	if err != nil {
		t.Fatalf("weekly quota usage: %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.TotalTokens != 1_500_000 || item.Calls != 3 || item.LastSeenMS != 1234 {
		t.Fatalf("item totals = %#v", item)
	}
	if math.Abs(item.EstimatedCostUSD-4) > 1e-9 {
		t.Fatalf("estimated cost = %f", item.EstimatedCostUSD)
	}
	if len(source.targets) != 1 || source.targets[0].PeriodStartMS != startMS || source.targets[0].PeriodEndMS != response.GeneratedAtMS {
		t.Fatalf("source targets = %#v", source.targets)
	}
}

func TestValidateQuotaWeeklyUsageRequestRejectsDuplicateTargets(t *testing.T) {
	startMS := time.Now().Add(-time.Hour).UnixMilli()
	err := ValidateQuotaWeeklyUsageRequest(QuotaWeeklyUsageRequest{Targets: []QuotaWeeklyUsageTarget{
		{TargetKey: "same", AuthFile: "a.json", PeriodStartMS: startMS},
		{TargetKey: "same", AuthFile: "b.json", PeriodStartMS: startMS},
	}})
	if err == nil {
		t.Fatal("expected duplicate target validation error")
	}
}

func TestNormalizeQuotaWeeklyUsageTargetsRollsExpiredPeriodForward(t *testing.T) {
	now := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	start := now.Add(-7 * 24 * time.Hour)

	targets, err := normalizeQuotaWeeklyUsageTargets([]QuotaWeeklyUsageTarget{
		{
			TargetKey:     "codex:account.json::auth-1",
			AuthFile:      "account.json",
			AuthIndex:     "auth-1",
			Provider:      "codex",
			PeriodStartMS: start.UnixMilli(),
			ResetAtMS:     now.UnixMilli(),
			PeriodSource:  "quota_week",
		},
	}, now.UnixMilli())
	if err != nil {
		t.Fatalf("normalize targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %#v", targets)
	}
	if targets[0].PeriodStartMS != now.UnixMilli() {
		t.Fatalf("period start = %d, want %d", targets[0].PeriodStartMS, now.UnixMilli())
	}
	wantResetMS := now.Add(7 * 24 * time.Hour).UnixMilli()
	if targets[0].ResetAtMS != wantResetMS {
		t.Fatalf("reset at = %d, want %d", targets[0].ResetAtMS, wantResetMS)
	}
}
