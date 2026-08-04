/**
 * Quota management page - coordinates the three quota sections.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { useAuthStore, useQuotaStore } from '@/stores';
import { authFilesApi, configFileApi } from '@/services/api';
import {
  monitoringAnalyticsApi,
  type QuotaWeeklyUsageTarget,
  type UsageHeaderSnapshot,
} from '@/services/api/usageService';
import { buildUsageHeaderSnapshotLookup } from '@/utils/usageHeaderSnapshots';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { IconSearch } from '@/components/ui/icons';
import {
  QuotaSection,
  ANTIGRAVITY_CONFIG,
  CLAUDE_CONFIG,
  CODEX_CONFIG,
  KIMI_CONFIG,
  XAI_CONFIG,
} from '@/components/quota';
import { CodexReauthDialog } from '@/features/oauth/CodexReauthDialog';
import {
  createCodexReauthTargetFromAuthFile,
  type CodexReauthTarget,
} from '@/features/oauth/codexReauthModel';
import type { QuotaSortMode } from '@/components/quota/quotaConfigs';
import { getScopedQuotaState } from '@/components/quota/quotaConfigs';
import type { AuthFileItem } from '@/types';
import {
  DEFAULT_QUOTA_ACCOUNT_DISPLAY_MODE,
  readQuotaPageUiState,
  writeQuotaPageUiState,
  type QuotaSectionType,
  type QuotaSectionViewMode,
  type QuotaAccountDisplayMode,
} from './quotaPageUiState';
import { buildQuotaWeeklyUsageTarget } from './quotaWeeklyUsage';
import { useQuotaWeeklyUsage } from './useQuotaWeeklyUsage';
import styles from './QuotaPage.module.scss';

export function QuotaPage() {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const managementKey = useAuthStore((state) => state.managementKey);
  const featureAvailability = usePanelFeatureAvailability();
  const managerServiceBase = featureAvailability.managerServiceBase;
  const codexQuota = useQuotaStore((state) => state.codexQuota);
  const claudeQuota = useQuotaStore((state) => state.claudeQuota);
  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);
  const kimiQuota = useQuotaStore((state) => state.kimiQuota);
  const xaiQuota = useQuotaStore((state) => state.xaiQuota);
  const initialUiState = useRef(readQuotaPageUiState());

  const [files, setFiles] = useState<AuthFileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [searchQuery, setSearchQuery] = useState(() => initialUiState.current.searchQuery);
  const [sortMode, setSortMode] = useState<QuotaSortMode>(() => initialUiState.current.sortMode);
  const [sectionViewModes, setSectionViewModes] = useState(() => ({
    ...initialUiState.current.sectionViewModes,
  }));
  const [codexReauthTarget, setCodexReauthTarget] = useState<CodexReauthTarget | null>(null);
  const [headerSnapshots, setHeaderSnapshots] = useState<UsageHeaderSnapshot[]>([]);
  const [accountDisplayModes, setAccountDisplayModes] = useState(() => ({
    ...initialUiState.current.accountDisplayModes,
  }));
  const [quotaPeriodClock, setQuotaPeriodClock] = useState(() => Date.now());

  const disableControls = connectionStatus !== 'connected';
  const sortOptions = useMemo(
    () => [
      { value: 'default', label: t('quota_management.sort_default') },
      { value: 'name-asc', label: t('quota_management.sort_name_asc') },
      { value: 'plan-desc', label: t('quota_management.sort_plan_desc') },
      { value: 'plan-asc', label: t('quota_management.sort_plan_asc') },
    ],
    [t]
  );

  const loadConfig = useCallback(async () => {
    try {
      await configFileApi.fetchConfigYaml();
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : t('notification.refresh_failed');
      setError((prev) => prev || errorMessage);
    }
  }, [t]);

  const loadFiles = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await authFilesApi.list();
      setFiles(data?.files || []);
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : t('notification.refresh_failed');
      setError(errorMessage);
    } finally {
      setLoading(false);
    }
  }, [t]);

  const loadHeaderSnapshots = useCallback(async () => {
    if (!managerServiceBase) {
      setHeaderSnapshots([]);
      return;
    }
    try {
      const response = await monitoringAnalyticsApi.getHeaderSnapshots(
        managerServiceBase,
        managementKey,
        {
          days: 30,
          limit: 1000,
        }
      );
      setHeaderSnapshots(response.items ?? []);
    } catch {
      setHeaderSnapshots((current) => current);
    }
  }, [managementKey, managerServiceBase]);

  const weeklyUsageTargets = useMemo(() => {
    const targets: QuotaWeeklyUsageTarget[] = [];
    files.filter(CODEX_CONFIG.filterFn).forEach((file) => {
      targets.push(
        buildQuotaWeeklyUsageTarget(
          CODEX_CONFIG.type,
          file,
          getScopedQuotaState(CODEX_CONFIG, codexQuota, file),
          quotaPeriodClock
        )
      );
    });
    files.filter(CLAUDE_CONFIG.filterFn).forEach((file) => {
      targets.push(
        buildQuotaWeeklyUsageTarget(
          CLAUDE_CONFIG.type,
          file,
          claudeQuota[file.name],
          quotaPeriodClock
        )
      );
    });
    files.filter(ANTIGRAVITY_CONFIG.filterFn).forEach((file) => {
      targets.push(
        buildQuotaWeeklyUsageTarget(
          ANTIGRAVITY_CONFIG.type,
          file,
          antigravityQuota[file.name],
          quotaPeriodClock
        )
      );
    });
    files.filter(KIMI_CONFIG.filterFn).forEach((file) => {
      targets.push(
        buildQuotaWeeklyUsageTarget(KIMI_CONFIG.type, file, kimiQuota[file.name], quotaPeriodClock)
      );
    });
    files.filter(XAI_CONFIG.filterFn).forEach((file) => {
      targets.push(
        buildQuotaWeeklyUsageTarget(XAI_CONFIG.type, file, xaiQuota[file.name], quotaPeriodClock)
      );
    });
    return targets;
  }, [antigravityQuota, claudeQuota, codexQuota, files, kimiQuota, quotaPeriodClock, xaiQuota]);

  const {
    itemsByKey: weeklyUsageByKey,
    loading: weeklyUsageLoading,
    error: weeklyUsageError,
    refresh: refreshWeeklyUsage,
  } = useQuotaWeeklyUsage({
    serviceBase: managerServiceBase,
    managementKey,
    targets: weeklyUsageTargets,
  });

  useEffect(() => {
    const nowMs = Date.now();
    const nextResetAtMs = weeklyUsageTargets.reduce<number | null>((nearest, target) => {
      const resetAtMs = target.reset_at_ms;
      if (!resetAtMs || resetAtMs <= nowMs) return nearest;
      return nearest === null || resetAtMs < nearest ? resetAtMs : nearest;
    }, null);
    if (nextResetAtMs === null) return;
    const delay = Math.min(Math.max(nextResetAtMs - nowMs + 250, 250), 2_147_000_000);
    const timer = window.setTimeout(() => setQuotaPeriodClock(Date.now()), delay);
    return () => window.clearTimeout(timer);
  }, [weeklyUsageTargets]);

  const handleHeaderRefresh = useCallback(async () => {
    await Promise.all([loadConfig(), loadFiles(), loadHeaderSnapshots(), refreshWeeklyUsage()]);
  }, [loadConfig, loadFiles, loadHeaderSnapshots, refreshWeeklyUsage]);

  useHeaderRefresh(handleHeaderRefresh);

  useEffect(() => {
    loadFiles();
    loadConfig();
    loadHeaderSnapshots();
  }, [loadFiles, loadConfig, loadHeaderSnapshots]);

  const headerSnapshotLookup = useMemo(
    () => buildUsageHeaderSnapshotLookup(headerSnapshots),
    [headerSnapshots]
  );

  useEffect(() => {
    writeQuotaPageUiState({
      searchQuery,
      sortMode,
      sectionViewModes,
      accountDisplayModes,
    });
  }, [accountDisplayModes, searchQuery, sectionViewModes, sortMode]);

  const getSectionViewMode = useCallback(
    (sectionType: QuotaSectionType): QuotaSectionViewMode =>
      sectionViewModes[sectionType] ?? 'paged',
    [sectionViewModes]
  );

  const setSectionViewMode = useCallback(
    (sectionType: QuotaSectionType, viewMode: QuotaSectionViewMode) => {
      setSectionViewModes((current) => ({
        ...current,
        [sectionType]: viewMode,
      }));
    },
    []
  );

  const handleCodexReauthSuccess = useCallback(async () => {
    await loadFiles();
  }, [loadFiles]);

  const getAccountDisplayMode = useCallback(
    (sectionType: QuotaSectionType): QuotaAccountDisplayMode =>
      accountDisplayModes[sectionType] ?? DEFAULT_QUOTA_ACCOUNT_DISPLAY_MODE,
    [accountDisplayModes]
  );

  const setAccountDisplayMode = useCallback(
    (sectionType: QuotaSectionType, mode: QuotaAccountDisplayMode) => {
      setAccountDisplayModes((current) => ({
        ...current,
        [sectionType]: mode,
      }));
    },
    []
  );

  return (
    <div className={styles.container}>
      {error && (
        <div className={styles.errorBox} role="alert">
          {error}
        </div>
      )}
      {weeklyUsageError && (
        <div className={styles.errorBox} role="alert">
          {t('quota_management.weekly_usage_load_failed', { message: weeklyUsageError })}
        </div>
      )}

      <div className={styles.toolbar}>
        <div className={styles.toolbarField}>
          <Input
            label={t('quota_management.search_label')}
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder={t('quota_management.search_placeholder')}
            rightElement={<IconSearch size={16} />}
            aria-label={t('quota_management.search_label')}
          />
        </div>
        <div className={`${styles.toolbarField} ${styles.sortField}`}>
          <label htmlFor="quota-sort-mode" className={styles.toolbarLabel}>
            {t('quota_management.sort_label')}
          </label>
          <Select
            id="quota-sort-mode"
            value={sortMode}
            options={sortOptions}
            onChange={(value) => setSortMode(value as QuotaSortMode)}
            ariaLabel={t('quota_management.sort_label')}
            fullWidth
          />
        </div>
      </div>

      <QuotaSection
        config={CODEX_CONFIG}
        files={files}
        loading={loading}
        disabled={disableControls}
        searchQuery={searchQuery}
        sortMode={sortMode}
        viewMode={getSectionViewMode(CODEX_CONFIG.type)}
        onViewModeChange={(viewMode) => setSectionViewMode(CODEX_CONFIG.type, viewMode)}
        onReauthAccount={(file) => setCodexReauthTarget(createCodexReauthTargetFromAuthFile(file))}
        accountDisplayMode={getAccountDisplayMode(CODEX_CONFIG.type)}
        onAccountDisplayModeChange={(mode) => setAccountDisplayMode(CODEX_CONFIG.type, mode)}
        headerSnapshotLookup={headerSnapshotLookup}
        weeklyUsageByKey={weeklyUsageByKey}
        weeklyUsageLoading={weeklyUsageLoading}
        weeklyUsageError={weeklyUsageError}
      />
      <QuotaSection
        config={CLAUDE_CONFIG}
        files={files}
        loading={loading}
        disabled={disableControls}
        searchQuery={searchQuery}
        sortMode={sortMode}
        viewMode={getSectionViewMode(CLAUDE_CONFIG.type)}
        onViewModeChange={(viewMode) => setSectionViewMode(CLAUDE_CONFIG.type, viewMode)}
        accountDisplayMode={getAccountDisplayMode(CLAUDE_CONFIG.type)}
        onAccountDisplayModeChange={(mode) => setAccountDisplayMode(CLAUDE_CONFIG.type, mode)}
        weeklyUsageByKey={weeklyUsageByKey}
        weeklyUsageLoading={weeklyUsageLoading}
        weeklyUsageError={weeklyUsageError}
      />
      <QuotaSection
        config={ANTIGRAVITY_CONFIG}
        files={files}
        loading={loading}
        disabled={disableControls}
        searchQuery={searchQuery}
        sortMode={sortMode}
        viewMode={getSectionViewMode(ANTIGRAVITY_CONFIG.type)}
        onViewModeChange={(viewMode) => setSectionViewMode(ANTIGRAVITY_CONFIG.type, viewMode)}
        accountDisplayMode={getAccountDisplayMode(ANTIGRAVITY_CONFIG.type)}
        onAccountDisplayModeChange={(mode) => setAccountDisplayMode(ANTIGRAVITY_CONFIG.type, mode)}
        weeklyUsageByKey={weeklyUsageByKey}
        weeklyUsageLoading={weeklyUsageLoading}
        weeklyUsageError={weeklyUsageError}
      />
      <QuotaSection
        config={KIMI_CONFIG}
        files={files}
        loading={loading}
        disabled={disableControls}
        searchQuery={searchQuery}
        sortMode={sortMode}
        viewMode={getSectionViewMode(KIMI_CONFIG.type)}
        onViewModeChange={(viewMode) => setSectionViewMode(KIMI_CONFIG.type, viewMode)}
        accountDisplayMode={getAccountDisplayMode(KIMI_CONFIG.type)}
        onAccountDisplayModeChange={(mode) => setAccountDisplayMode(KIMI_CONFIG.type, mode)}
        weeklyUsageByKey={weeklyUsageByKey}
        weeklyUsageLoading={weeklyUsageLoading}
        weeklyUsageError={weeklyUsageError}
      />
      <QuotaSection
        config={XAI_CONFIG}
        files={files}
        loading={loading}
        disabled={disableControls}
        searchQuery={searchQuery}
        sortMode={sortMode}
        viewMode={getSectionViewMode(XAI_CONFIG.type)}
        onViewModeChange={(viewMode) => setSectionViewMode(XAI_CONFIG.type, viewMode)}
        accountDisplayMode={getAccountDisplayMode(XAI_CONFIG.type)}
        onAccountDisplayModeChange={(mode) => setAccountDisplayMode(XAI_CONFIG.type, mode)}
        weeklyUsageByKey={weeklyUsageByKey}
        weeklyUsageLoading={weeklyUsageLoading}
        weeklyUsageError={weeklyUsageError}
      />

      <CodexReauthDialog
        open={Boolean(codexReauthTarget)}
        target={codexReauthTarget}
        onClose={() => setCodexReauthTarget(null)}
        onSuccess={handleCodexReauthSuccess}
      />
    </div>
  );
}
