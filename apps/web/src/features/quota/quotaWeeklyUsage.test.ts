import { describe, expect, it } from 'vitest';
import type { CodexQuotaState, XaiQuotaState } from '@/types';
import {
  QUOTA_WEEK_MS,
  buildQuotaWeeklyUsageLookup,
  buildQuotaWeeklyUsageTarget,
  calendarWeekPeriod,
  resolveQuotaWeeklyPeriod,
} from './quotaWeeklyUsage';

describe('quota weekly usage periods', () => {
  it('uses Monday midnight as the calendar-week fallback', () => {
    const now = new Date(2026, 7, 6, 15, 30, 0, 0);
    const period = calendarWeekPeriod(now.getTime());
    const start = new Date(period.periodStartMs);
    const reset = new Date(period.resetAtMs);

    expect(period.periodSource).toBe('calendar_week');
    expect(start.getDay()).toBe(1);
    expect(start.getHours()).toBe(0);
    expect(start.getMinutes()).toBe(0);
    expect(reset.getTime()).toBeGreaterThan(now.getTime());
  });

  it('uses the active Codex seven-day quota boundary', () => {
    const nowMs = Date.UTC(2026, 7, 3, 1, 0, 0);
    const resetAtMs = nowMs + 2 * 24 * 60 * 60 * 1000;
    const quota: CodexQuotaState = {
      status: 'success',
      windows: [
        {
          id: 'weekly',
          label: 'Weekly',
          usedPercent: 30,
          resetLabel: 'later',
          limitWindowSeconds: QUOTA_WEEK_MS / 1000,
          resetAtMs,
        },
      ],
    };

    expect(resolveQuotaWeeklyPeriod('codex', quota, nowMs)).toEqual({
      periodStartMs: resetAtMs - QUOTA_WEEK_MS,
      resetAtMs,
      periodSource: 'quota_week',
    });
  });

  it('rolls a stale weekly reset into the new quota period', () => {
    const staleResetAtMs = Date.UTC(2026, 7, 3, 0, 0, 0);
    const nowMs = staleResetAtMs + 60 * 60 * 1000;
    const quota: CodexQuotaState = {
      status: 'success',
      windows: [
        {
          id: 'weekly',
          label: 'Weekly',
          usedPercent: 0,
          resetLabel: 'stale',
          limitWindowSeconds: QUOTA_WEEK_MS / 1000,
          resetAtMs: staleResetAtMs,
        },
      ],
    };

    expect(resolveQuotaWeeklyPeriod('codex', quota, nowMs)).toEqual({
      periodStartMs: staleResetAtMs,
      resetAtMs: staleResetAtMs + QUOTA_WEEK_MS,
      periodSource: 'quota_week',
    });
  });

  it('uses an explicit weekly xAI billing period', () => {
    const startMs = Date.UTC(2026, 7, 1, 0, 0, 0);
    const resetAtMs = startMs + QUOTA_WEEK_MS;
    const quota: XaiQuotaState = {
      status: 'success',
      billing: {
        periodType: 'weekly',
        periodStart: new Date(startMs).toISOString(),
        periodEnd: new Date(resetAtMs).toISOString(),
        usagePercent: 20,
        productUsage: [],
        monthlyLimitCents: null,
        usedCents: null,
        includedUsedCents: null,
        onDemandCapCents: null,
        onDemandUsedCents: null,
        onDemandUsedPercent: null,
        usedPercent: null,
      },
    };

    expect(resolveQuotaWeeklyPeriod('xai', quota, startMs + 1_000)).toEqual({
      periodStartMs: startMs,
      resetAtMs,
      periodSource: 'quota_week',
    });
  });
});

describe('quota weekly usage identity', () => {
  it('keeps accounts in the same file separate by auth index', () => {
    const nowMs = new Date(2026, 7, 6, 12, 0, 0).getTime();
    const first = buildQuotaWeeklyUsageTarget(
      'codex',
      { name: 'shared.json', type: 'codex', authIndex: 'auth-1' },
      undefined,
      nowMs
    );
    const second = buildQuotaWeeklyUsageTarget(
      'codex',
      { name: 'shared.json', type: 'codex', authIndex: 'auth-2' },
      undefined,
      nowMs
    );

    expect(first.target_key).not.toBe(second.target_key);
    expect(first.auth_index).toBe('auth-1');
    expect(second.auth_index).toBe('auth-2');
  });

  it('indexes API results by the echoed target key', () => {
    const item = {
      target_key: 'codex:a.json::auth-1',
      period_start_ms: 1,
      period_end_ms: 2,
      period_source: 'calendar_week' as const,
      calls: 1,
      total_tokens: 2_000_000,
      estimated_cost_usd: 3,
    };
    expect(buildQuotaWeeklyUsageLookup([item])).toEqual({ [item.target_key]: item });
  });
});
