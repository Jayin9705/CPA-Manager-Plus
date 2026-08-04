package store

import "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"

// QuotaWeeklyUsageTarget identifies one quota card and the active weekly
// accounting interval that should be read from the canonical usage events.
// TargetKey is supplied by the console and is only echoed/grouped; it is never
// interpolated into SQL.
type QuotaWeeklyUsageTarget struct {
	TargetKey     string `json:"target_key"`
	AuthFile      string `json:"auth_file"`
	AuthIndex     string `json:"auth_index"`
	Provider      string `json:"provider"`
	PeriodStartMS int64  `json:"period_start_ms"`
	PeriodEndMS   int64  `json:"period_end_ms"`
}

// QuotaWeeklyUsageModelStat is the model-level projection needed to price one
// quota card without loading raw events into the Manager Server process.
type QuotaWeeklyUsageModelStat struct {
	usage.LongContextTokens
	TargetKey           string
	Model               string
	BillingModel        string
	ServiceTier         string
	Calls               int64
	InputTokens         int64
	OutputTokens        int64
	CachedTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	TotalTokens         int64
	LastSeenMS          int64
}
