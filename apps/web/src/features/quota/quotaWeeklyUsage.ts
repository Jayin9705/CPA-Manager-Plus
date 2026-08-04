import type {
  AntigravityQuotaState,
  AuthFileItem,
  ClaudeQuotaState,
  CodexQuotaState,
  KimiQuotaState,
  XaiQuotaState,
} from '@/types';
import type { QuotaWeeklyUsageItem, QuotaWeeklyUsageTarget } from '@/services/api/usageService';
import type { QuotaSectionType } from './quotaPageUiState';

export const QUOTA_WEEK_MS = 7 * 24 * 60 * 60 * 1000;

export type QuotaWeeklyState =
  | AntigravityQuotaState
  | ClaudeQuotaState
  | CodexQuotaState
  | KimiQuotaState
  | XaiQuotaState
  | undefined;

export interface QuotaWeeklyPeriod {
  periodStartMs: number;
  resetAtMs: number;
  periodSource: 'quota_week' | 'calendar_week';
}

export type QuotaWeeklyUsageLookup = Record<string, QuotaWeeklyUsageItem>;

const normalizeAuthIndex = (file: AuthFileItem): string => {
  const value = file.authIndex ?? file['auth_index'] ?? file['auth-index'];
  if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  return typeof value === 'string' ? value.trim() : '';
};

export const getQuotaWeeklyUsageTargetKey = (
  sectionType: QuotaSectionType,
  file: AuthFileItem
): string => `${sectionType}:${file.name}::${normalizeAuthIndex(file) || '-'}`;

export const calendarWeekPeriod = (nowMs = Date.now()): QuotaWeeklyPeriod => {
  const start = new Date(nowMs);
  start.setHours(0, 0, 0, 0);
  const daysSinceMonday = (start.getDay() + 6) % 7;
  start.setDate(start.getDate() - daysSinceMonday);
  const reset = new Date(start.getTime());
  reset.setDate(reset.getDate() + 7);
  return {
    periodStartMs: start.getTime(),
    resetAtMs: reset.getTime(),
    periodSource: 'calendar_week',
  };
};

const rollingWeekPeriod = (
  resetAtMs: number | null | undefined,
  nowMs: number,
  durationMs = QUOTA_WEEK_MS
): QuotaWeeklyPeriod | null => {
  if (
    !Number.isFinite(resetAtMs) ||
    !resetAtMs ||
    !Number.isFinite(durationMs) ||
    durationMs < 6 * 24 * 60 * 60 * 1000 ||
    durationMs > 8 * 24 * 60 * 60 * 1000
  ) {
    return null;
  }
  const cycles = Math.floor((nowMs - resetAtMs) / durationMs) + 1;
  const activeResetAtMs = Math.round(resetAtMs + cycles * durationMs);
  const periodStartMs = Math.round(activeResetAtMs - durationMs);
  if (periodStartMs > nowMs || activeResetAtMs <= nowMs) return null;
  return { periodStartMs, resetAtMs: activeResetAtMs, periodSource: 'quota_week' };
};

const parseTimestamp = (value: unknown): number | null => {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value < 1_000_000_000_000 ? Math.round(value * 1000) : Math.round(value);
  }
  if (typeof value !== 'string' || !value.trim()) return null;
  const numeric = Number(value.trim());
  if (Number.isFinite(numeric)) {
    return numeric < 1_000_000_000_000 ? Math.round(numeric * 1000) : Math.round(numeric);
  }
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
};

const codexWeekPeriod = (quota: CodexQuotaState, nowMs: number): QuotaWeeklyPeriod | null => {
  const weeklyWindow = quota.windows?.find(
    (window) =>
      window.id === 'weekly' ||
      (window.limitWindowSeconds !== null &&
        window.limitWindowSeconds !== undefined &&
        Math.abs(window.limitWindowSeconds * 1000 - QUOTA_WEEK_MS) <= 60_000)
  );
  if (!weeklyWindow) return null;
  const durationMs = (weeklyWindow.limitWindowSeconds ?? QUOTA_WEEK_MS / 1000) * 1000;
  return rollingWeekPeriod(weeklyWindow.resetAtMs, nowMs, durationMs);
};

const claudeWeekPeriod = (quota: ClaudeQuotaState, nowMs: number): QuotaWeeklyPeriod | null => {
  return rollingWeekPeriod(quota.weeklyResetAtMs, nowMs);
};

const antigravityWeekPeriod = (
  quota: AntigravityQuotaState,
  nowMs: number
): QuotaWeeklyPeriod | null => {
  for (const group of quota.groups ?? []) {
    for (const bucket of group.buckets ?? []) {
      const window = String(bucket.window ?? '')
        .trim()
        .toLowerCase();
      if (!/(^|[^a-z])(7d|7-day|week|weekly|604800)([^a-z]|$)/.test(window)) continue;
      const period = rollingWeekPeriod(parseTimestamp(bucket.resetTime), nowMs);
      if (period) return period;
    }
  }
  return null;
};

const xaiWeekPeriod = (quota: XaiQuotaState, nowMs: number): QuotaWeeklyPeriod | null => {
  const billing = quota.billing;
  if (!billing || billing.periodType !== 'weekly') return null;
  const startAtMs = parseTimestamp(billing.periodStart);
  const resetAtMs = parseTimestamp(billing.periodEnd);
  if (
    startAtMs !== null &&
    resetAtMs !== null &&
    startAtMs <= nowMs &&
    resetAtMs > nowMs &&
    resetAtMs - startAtMs >= 6 * 24 * 60 * 60 * 1000 &&
    resetAtMs - startAtMs <= 8 * 24 * 60 * 60 * 1000
  ) {
    return { periodStartMs: startAtMs, resetAtMs, periodSource: 'quota_week' };
  }
  return rollingWeekPeriod(resetAtMs, nowMs);
};

export const resolveQuotaWeeklyPeriod = (
  sectionType: QuotaSectionType,
  quota: QuotaWeeklyState,
  nowMs = Date.now()
): QuotaWeeklyPeriod => {
  if (quota?.status === 'success') {
    let providerPeriod: QuotaWeeklyPeriod | null = null;
    if (sectionType === 'codex') providerPeriod = codexWeekPeriod(quota as CodexQuotaState, nowMs);
    if (sectionType === 'claude') {
      providerPeriod = claudeWeekPeriod(quota as ClaudeQuotaState, nowMs);
    }
    if (sectionType === 'antigravity') {
      providerPeriod = antigravityWeekPeriod(quota as AntigravityQuotaState, nowMs);
    }
    if (sectionType === 'xai') providerPeriod = xaiWeekPeriod(quota as XaiQuotaState, nowMs);
    if (providerPeriod) return providerPeriod;
  }
  return calendarWeekPeriod(nowMs);
};

export const buildQuotaWeeklyUsageTarget = (
  sectionType: QuotaSectionType,
  file: AuthFileItem,
  quota: QuotaWeeklyState,
  nowMs = Date.now()
): QuotaWeeklyUsageTarget => {
  const period = resolveQuotaWeeklyPeriod(sectionType, quota, nowMs);
  const provider = String(file.provider ?? file.type ?? sectionType).trim();
  return {
    target_key: getQuotaWeeklyUsageTargetKey(sectionType, file),
    auth_file: file.name,
    auth_index: normalizeAuthIndex(file) || undefined,
    provider: provider || sectionType,
    period_start_ms: period.periodStartMs,
    reset_at_ms: period.resetAtMs,
    period_source: period.periodSource,
  };
};

export const buildQuotaWeeklyUsageLookup = (
  items: QuotaWeeklyUsageItem[] | undefined
): QuotaWeeklyUsageLookup =>
  Object.fromEntries((items ?? []).map((item) => [item.target_key, item]));
