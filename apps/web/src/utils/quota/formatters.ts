/**
 * Formatting functions for quota display.
 */

import type { TFunction } from 'i18next';
import type { CodexUsageWindow } from '@/types';
import { normalizeNumberValue } from './parsers';

export function formatQuotaResetTime(value?: string): string {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

export function formatUnixSeconds(value: number | null): string {
  if (!value) return '-';
  const date = new Date(value * 1000);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

export function formatCodexResetLabel(window?: CodexUsageWindow | null): string {
  const resetAtMs = resolveCodexResetAtMs(window);
  return resetAtMs === null ? '-' : formatUnixSeconds(resetAtMs / 1000);
}

export function resolveCodexResetAtMs(
  window?: CodexUsageWindow | null,
  nowMs = Date.now()
): number | null {
  if (!window) return null;
  const resetAt = normalizeNumberValue(window.reset_at ?? window.resetAt);
  if (resetAt !== null && resetAt > 0) {
    const value = resetAt < 1_000_000_000_000 ? resetAt * 1000 : resetAt;
    return Number.isFinite(value) ? Math.round(value) : null;
  }
  const resetAfter = normalizeNumberValue(window.reset_after_seconds ?? window.resetAfterSeconds);
  if (resetAfter !== null && resetAfter > 0) {
    const value = nowMs + resetAfter * 1000;
    return Number.isFinite(value) ? Math.round(value) : null;
  }
  return null;
}

export function createStatusError(message: string, status?: number): Error & { status?: number } {
  const error = new Error(message) as Error & { status?: number };
  if (status !== undefined) {
    error.status = status;
  }
  return error;
}

export function getStatusFromError(err: unknown): number | undefined {
  if (typeof err === 'object' && err !== null && 'status' in err) {
    const rawStatus = (err as { status?: unknown }).status;
    if (typeof rawStatus === 'number' && Number.isFinite(rawStatus)) {
      return rawStatus;
    }
    const asNumber = Number(rawStatus);
    if (Number.isFinite(asNumber) && asNumber > 0) {
      return asNumber;
    }
  }
  return undefined;
}

export function formatKimiResetHint(t: TFunction, hint?: string): string {
  if (!hint) return '';
  return t('kimi_quota.reset_hint', { hint });
}
