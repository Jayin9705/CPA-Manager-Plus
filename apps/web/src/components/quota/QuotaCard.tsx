/**
 * Generic quota card component.
 */

import { useTranslation } from 'react-i18next';
import type { ReactElement, ReactNode } from 'react';
import type { TFunction } from 'i18next';
import { Button } from '@/components/ui/Button';
import {
  DEFAULT_QUOTA_ACCOUNT_DISPLAY_MODE,
  type QuotaAccountDisplayMode,
} from '@/features/quota/quotaPageUiState';
import type { AuthFileItem, ResolvedTheme, ThemeColors } from '@/types';
import type { QuotaWeeklyUsageItem } from '@/services/api/usageService';
import { TYPE_COLORS } from '@/utils/quota';
import { resolveQuotaAccountDisplayText } from './quotaDisplay';
import styles from '@/features/quota/QuotaPage.module.scss';

type QuotaStatus = 'idle' | 'loading' | 'success' | 'error';

export interface QuotaStatusState {
  status: QuotaStatus;
  error?: string;
  errorStatus?: number;
}

export interface QuotaProgressBarProps {
  percent: number | null;
  highThreshold: number;
  mediumThreshold: number;
}

export function QuotaProgressBar({
  percent,
  highThreshold,
  mediumThreshold,
}: QuotaProgressBarProps) {
  const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));
  const normalized = percent === null ? null : clamp(percent, 0, 100);
  const fillClass =
    normalized === null
      ? styles.quotaBarFillMedium
      : normalized >= highThreshold
        ? styles.quotaBarFillHigh
        : normalized >= mediumThreshold
          ? styles.quotaBarFillMedium
          : styles.quotaBarFillLow;
  const widthPercent = Math.round(normalized ?? 0);

  return (
    <div className={styles.quotaBar}>
      <div
        className={`${styles.quotaBarFill} ${fillClass}`}
        style={{ width: `${widthPercent}%` }}
      />
    </div>
  );
}

export interface QuotaRenderHelpers {
  styles: typeof styles;
  QuotaProgressBar: (props: QuotaProgressBarProps) => ReactElement;
}

interface QuotaCardProps<TState extends QuotaStatusState> {
  item: AuthFileItem;
  quota?: TState;
  resolvedTheme: ResolvedTheme;
  i18nPrefix: string;
  cardIdleMessageKey?: string;
  cardClassName: string;
  defaultType: string;
  accountDisplayMode?: QuotaAccountDisplayMode;
  canRefresh?: boolean;
  onRefresh?: () => void;
  canReset?: boolean;
  resetLabel?: string;
  onReset?: () => void;
  canReauth?: boolean;
  onReauth?: () => void;
  weeklyUsage?: QuotaWeeklyUsageItem;
  weeklyUsageLoading?: boolean;
  weeklyUsageError?: string;
  renderQuotaItems: (quota: TState, t: TFunction, helpers: QuotaRenderHelpers) => ReactNode;
}

const formatWeeklyTokensM = (tokens: number, locale?: string): string =>
  new Intl.NumberFormat(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(Math.max(0, tokens) / 1_000_000);

const formatEstimatedUSD = (cost: number, locale?: string): string =>
  new Intl.NumberFormat(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: cost > 0 && cost < 0.01 ? 4 : 2,
  }).format(Math.max(0, cost));

const formatWeeklyPeriodDate = (timestampMs: number | undefined, locale?: string): string => {
  if (!timestampMs || !Number.isFinite(timestampMs)) return '-';
  return new Intl.DateTimeFormat(locale, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(timestampMs));
};

export function QuotaCard<TState extends QuotaStatusState>({
  item,
  quota,
  resolvedTheme,
  i18nPrefix,
  cardIdleMessageKey,
  cardClassName,
  defaultType,
  accountDisplayMode = DEFAULT_QUOTA_ACCOUNT_DISPLAY_MODE,
  canRefresh = false,
  onRefresh,
  canReset = false,
  resetLabel,
  onReset,
  canReauth = false,
  onReauth,
  weeklyUsage,
  weeklyUsageLoading = false,
  weeklyUsageError = '',
  renderQuotaItems,
}: QuotaCardProps<TState>) {
  const { t, i18n } = useTranslation();

  const displayType = item.type || item.provider || defaultType;
  const typeColorKey = String(displayType).trim().toLowerCase().replace(/_/g, '-');
  const normalizedTypeColorKey =
    typeColorKey === 'x-ai' || typeColorKey === 'grok' ? 'xai' : typeColorKey;
  const typeColorSet = TYPE_COLORS[normalizedTypeColorKey] || TYPE_COLORS.unknown;
  const typeColor: ThemeColors =
    resolvedTheme === 'dark' && typeColorSet.dark ? typeColorSet.dark : typeColorSet.light;

  const quotaStatus = quota?.status ?? 'idle';
  const quotaErrorMessage = resolveQuotaErrorMessage(
    t,
    quota?.errorStatus,
    quota?.error || t('common.unknown_error')
  );
  const idleMessageKey = onRefresh
    ? `${i18nPrefix}.idle`
    : (cardIdleMessageKey ?? `${i18nPrefix}.idle`);
  const accountDisplay = resolveQuotaAccountDisplayText(item, accountDisplayMode);
  const locale = i18n?.resolvedLanguage || i18n?.language || undefined;
  const weeklyPeriodTitle = weeklyUsage
    ? t('quota_management.weekly_usage_period', {
        start: formatWeeklyPeriodDate(weeklyUsage.period_start_ms, locale),
        end: formatWeeklyPeriodDate(weeklyUsage.reset_at_ms, locale),
      })
    : weeklyUsageError || t('quota_management.weekly_usage_unavailable');

  const getTypeLabel = (type: string): string => {
    const key = `auth_files.filter_${type}`;
    const translated = t(key);
    if (translated !== key) return translated;
    if (type.toLowerCase() === 'iflow') return 'iFlow';
    return type.charAt(0).toUpperCase() + type.slice(1);
  };

  const renderActions = (options?: { includeReauth?: boolean }) =>
    onRefresh || (onReset && resetLabel) || (options?.includeReauth && onReauth) ? (
      <div className={styles.quotaActions}>
        {options?.includeReauth && onReauth ? (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className={styles.quotaActionButton}
            onClick={onReauth}
            disabled={!canReauth}
          >
            {t('codex_reauth.button')}
          </Button>
        ) : null}
        {onReset && resetLabel ? (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className={styles.quotaActionButton}
            onClick={onReset}
            disabled={!canReset}
          >
            {resetLabel}
          </Button>
        ) : null}
        {onRefresh ? (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className={styles.quotaActionButton}
            onClick={onRefresh}
            disabled={!canRefresh}
          >
            {t(`${i18nPrefix}.refresh_button`)}
          </Button>
        ) : null}
      </div>
    ) : null;

  return (
    <div className={`${styles.fileCard} ${cardClassName}`}>
      <div className={styles.cardHeader}>
        <span
          className={styles.typeBadge}
          style={{
            backgroundColor: typeColor.bg,
            color: typeColor.text,
            ...(typeColor.border ? { border: typeColor.border } : {}),
          }}
        >
          {getTypeLabel(displayType)}
        </span>
        <span className={styles.fileName} title={accountDisplay.title}>
          {accountDisplay.primary}
        </span>
      </div>

      <div
        className={styles.weeklyUsageSummary}
        role="group"
        aria-label={`${t('quota_management.weekly_usage_title')}. ${weeklyPeriodTitle}`}
        aria-busy={weeklyUsageLoading}
        title={weeklyPeriodTitle}
      >
        <div className={styles.weeklyUsageHeading}>
          <span>{t('quota_management.weekly_usage_title')}</span>
          <span className={styles.weeklyUsagePeriodBadge}>
            {t(
              weeklyUsage?.period_source === 'quota_week'
                ? 'quota_management.weekly_usage_quota_period'
                : 'quota_management.weekly_usage_calendar_period'
            )}
          </span>
        </div>
        <div className={styles.weeklyUsageMetrics}>
          <div className={styles.weeklyUsageMetric}>
            <span className={styles.weeklyUsageMetricLabel}>
              {t('quota_management.weekly_usage_tokens')}
            </span>
            <span className={styles.weeklyUsageMetricValue}>
              {weeklyUsageLoading
                ? '—'
                : weeklyUsage
                  ? `${formatWeeklyTokensM(weeklyUsage.total_tokens, locale)} M`
                  : '-- M'}
            </span>
          </div>
          <div className={styles.weeklyUsageMetric}>
            <span className={styles.weeklyUsageMetricLabel}>
              {t('quota_management.weekly_usage_estimated_cost')}
            </span>
            <span className={styles.weeklyUsageMetricValue}>
              {weeklyUsageLoading
                ? '—'
                : weeklyUsage
                  ? `≈ $${formatEstimatedUSD(weeklyUsage.estimated_cost_usd, locale)}`
                  : '--'}
            </span>
          </div>
        </div>
      </div>

      <div className={styles.quotaSection}>
        {quotaStatus === 'loading' ? (
          <div className={styles.quotaMessage}>{t(`${i18nPrefix}.loading`)}</div>
        ) : quotaStatus === 'idle' ? (
          onRefresh ? (
            <button
              type="button"
              className={`${styles.quotaMessage} ${styles.quotaMessageAction}`}
              onClick={onRefresh}
              disabled={!canRefresh}
            >
              {t(idleMessageKey)}
            </button>
          ) : (
            <div className={styles.quotaMessage}>{t(idleMessageKey)}</div>
          )
        ) : quotaStatus === 'error' ? (
          <>
            <div className={styles.quotaError}>
              {t(`${i18nPrefix}.load_failed`, {
                message: quotaErrorMessage,
              })}
            </div>
            {renderActions({ includeReauth: canReauth })}
          </>
        ) : quota ? (
          <>
            {renderQuotaItems(quota, t, { styles, QuotaProgressBar })}
            {renderActions()}
          </>
        ) : (
          <div className={styles.quotaMessage}>{t(idleMessageKey)}</div>
        )}
      </div>
    </div>
  );
}

const resolveQuotaErrorMessage = (
  t: TFunction,
  status: number | undefined,
  fallback: string
): string => {
  if (status === 404) return t('common.quota_update_required');
  if (status === 403) return t('common.quota_check_credential');
  return fallback;
};
