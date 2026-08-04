import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { IconPencil, IconSearch, IconTrash2, IconX } from '@/components/ui/icons';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import {
  usageServiceApi,
  type ModelPriceSyncCandidate,
  type ModelPriceSyncResponse,
  type ModelPriceUsageSummaryResponse,
} from '@/services/api/usageService';
import { useAuthStore, useNotificationStore } from '@/stores';
import { useUsageData } from '@/features/monitoring/hooks/useUsageData';
import {
  applyCandidatePrice,
  buildModelPriceRows,
  buildModelPriceSummary,
  buildPriceFromDraft,
  buildSyncPriceModelsFromSummary,
  createEmptyPriceDraft,
  createPriceDraft,
  filterModelPriceRows,
  formatContextThreshold,
  formatPriceUnit,
  formatServiceTierRule,
  getModelPriceCandidateIdentity,
  groupModelPriceCandidatesBySource,
  isOfficialPriceSource,
  normalizeModelIdentity,
  resolveContextTierDisplayPrice,
  resolveServiceTierDisplayPrice,
  type ModelPriceFilter,
  type PriceDraft,
} from '@/features/monitoring/model/modelPricesPageModel';
import { readModelPricesPageUiState, writeModelPricesPageUiState } from './modelPricesPageUiState';
import styles from './ModelPricesPage.module.scss';

const FILTERS: ModelPriceFilter[] = ['all', 'missing', 'candidates', 'saved'];

const resolveErrorMessage = (error: unknown, fallback: string) => {
  const rawMessage = error instanceof Error ? error.message : String(error || fallback);
  return rawMessage === 'model_price_sync_requires_usage_service'
    ? 'model_price_sync_requires_usage_service'
    : rawMessage;
};

const resolvePriceSourceLabel = (source: string | undefined, t: TFunction) => {
  const normalizedSource = source?.trim().toLowerCase() || 'manual';
  const sourceKey = normalizedSource.replace(/[^a-z0-9]+/g, '_');
  return t(`model_prices.source_${sourceKey}`, { defaultValue: source || normalizedSource });
};

export function ModelPricesPage() {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const managementKey = useAuthStore((state) => state.managementKey);
  const featureAvailability = usePanelFeatureAvailability();
  const { loading, modelPrices, setModelPrices, syncModelPrices, usageServiceAvailable } =
    useUsageData({ loadUsageEvents: false });
  const [usageSummary, setUsageSummary] = useState<ModelPriceUsageSummaryResponse | null>(null);
  const [usageSummaryLoading, setUsageSummaryLoading] = useState(false);
  const initialUiState = useRef(readModelPricesPageUiState());
  const [search, setSearch] = useState(() => initialUiState.current.search);
  const [filter, setFilter] = useState<ModelPriceFilter>(() => initialUiState.current.filter);
  const [syncing, setSyncing] = useState(false);
  const [syncResult, setSyncResult] = useState<ModelPriceSyncResponse | null>(null);
  const [selectedCandidates, setSelectedCandidates] = useState<Record<string, string>>({});
  const [draft, setDraft] = useState<PriceDraft>(() => createEmptyPriceDraft());
  const [manualEditorOpen, setManualEditorOpen] = useState(false);
  const modelPriceServiceBase = featureAvailability.modelPricesAvailable
    ? featureAvailability.managerServiceBase
    : '';

  const syncModels = useMemo(
    () => buildSyncPriceModelsFromSummary(usageSummary, modelPrices),
    [modelPrices, usageSummary]
  );

  const candidateSets = useMemo(() => syncResult?.candidates ?? [], [syncResult?.candidates]);
  const rows = useMemo(
    () => buildModelPriceRows(usageSummary, modelPrices, candidateSets),
    [candidateSets, modelPrices, usageSummary]
  );
  const summary = useMemo(() => buildModelPriceSummary(rows), [rows]);
  const currentRows = useMemo(() => rows.filter((row) => row.calls > 0), [rows]);
  const visibleRows = useMemo(
    () => filterModelPriceRows(rows, filter, search),
    [filter, rows, search]
  );

  const filterCounts = useMemo<Record<ModelPriceFilter, number>>(
    () => ({
      all: summary.total,
      missing: summary.missing,
      candidates: summary.candidates,
      saved: summary.saved,
    }),
    [summary]
  );

  useEffect(() => {
    writeModelPricesPageUiState({ search, filter });
  }, [filter, search]);

  useEffect(() => {
    const controller = new AbortController();
    if (!modelPriceServiceBase) {
      setUsageSummary(null);
      setUsageSummaryLoading(false);
      return () => controller.abort();
    }

    setUsageSummaryLoading(true);
    void usageServiceApi
      .getModelPriceUsageSummary(modelPriceServiceBase, managementKey, controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) {
          setUsageSummary(response);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          // Older Manager Server versions do not expose this lightweight endpoint.
          // Keep saved prices visible without falling back to the large /usage payload.
          setUsageSummary(null);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setUsageSummaryLoading(false);
        }
      });

    return () => controller.abort();
  }, [managementKey, modelPriceServiceBase]);

  const handleSync = async () => {
    if (syncModels.length === 0) {
      showNotification(t('usage_stats.model_price_sync_no_models'), 'warning');
      return;
    }
    setSyncing(true);
    try {
      const result = await syncModelPrices(syncModels);
      setSyncResult(result);
      showNotification(
        t('model_prices.sync_success_detail', {
          imported: result.imported,
          candidates: result.candidates?.length ?? 0,
          unmatched: result.unmatched?.length ?? 0,
          preserved: result.preserved?.length ?? 0,
        }),
        result.preserved?.length ? 'warning' : 'success'
      );
    } catch (error: unknown) {
      const message = resolveErrorMessage(error, t('common.unknown_error'));
      showNotification(
        `${t('usage_stats.model_price_sync_failed')}: ${
          message === 'model_price_sync_requires_usage_service'
            ? t('usage_stats.model_price_sync_requires_usage_service')
            : message
        }`,
        'error'
      );
    } finally {
      setSyncing(false);
    }
  };

  const handleConfirmCandidate = async (model: string, candidate: ModelPriceSyncCandidate) => {
    await setModelPrices(applyCandidatePrice(modelPrices, model, candidate));
    setSyncResult((previous) =>
      previous
        ? {
            ...previous,
            candidates: previous.candidates?.filter((set) => set.model !== model),
            unmatched: previous.unmatched?.filter((item) => item !== model),
          }
        : previous
    );
    showNotification(t('model_prices.candidate_confirmed'), 'success');
  };

  const handleSaveDraft = async () => {
    const price = buildPriceFromDraft(draft);
    const model = draft.model.trim();
    if (!model || !price) {
      showNotification(t('usage_stats.model_price_model_required'), 'warning');
      return;
    }
    await setModelPrices({ ...modelPrices, [model]: price });
    setDraft(createEmptyPriceDraft());
    setManualEditorOpen(false);
    showNotification(t('usage_stats.model_price_saved'), 'success');
  };

  const handleDelete = async (model: string) => {
    const next = { ...modelPrices };
    delete next[model];
    await setModelPrices(next);
    if (draft.model === model) {
      setDraft(createEmptyPriceDraft());
      setManualEditorOpen(false);
    }
  };

  const setDraftField = (field: keyof PriceDraft, value: string) => {
    setDraft((previous) => ({ ...previous, [field]: value }));
  };

  const openManualEditor = (model = '', price = modelPrices[model]) => {
    setDraft(createPriceDraft(model, price));
    setManualEditorOpen(true);
  };

  const closeManualEditor = () => {
    setDraft(createEmptyPriceDraft());
    setManualEditorOpen(false);
  };

  return (
    <div className={styles.page}>
      <section className={styles.actionBar} aria-label={t('common.action')}>
        <div className={styles.titleGroup}>
          <div className={styles.pageTitle}>
            <span className={styles.pageEyebrow}>{t('model_prices.eyebrow')}</span>
            <h1>{t('model_prices.title')}</h1>
          </div>
          {featureAvailability.requestMonitoringAvailable ? (
            <Link to="/monitoring" className={styles.backLink}>
              {t('model_prices.back_to_monitoring')}
            </Link>
          ) : null}
        </div>
        <div className={styles.actionGroup}>
          <span className={styles.metaPill}>
            {usageServiceAvailable
              ? t('model_prices.usage_service_ready')
              : t('model_prices.usage_service_required')}
          </span>
          <span className={styles.metaPill}>
            {t('model_prices.sync_model_count', { count: syncModels.length })}
          </span>
          <Button
            size="xs"
            variant="secondary"
            onClick={() => openManualEditor()}
            className={styles.toolbarButton}
          >
            {t('model_prices.add_manual')}
          </Button>
          <Button size="xs" onClick={() => void handleSync()} loading={syncing}>
            {t('usage_stats.model_price_sync')}
          </Button>
        </div>
      </section>

      <section className={styles.pricePanel}>
        <div className={styles.panelToolbar}>
          <div className={styles.searchWrap}>
            <Input
              className={styles.compactInput}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t('model_prices.search_placeholder')}
              rightElement={<IconSearch size={15} />}
            />
          </div>
          <div className={styles.filterGroup}>
            {FILTERS.map((item) => (
              <button
                key={item}
                type="button"
                className={`${styles.filterButton} ${
                  filter === item ? styles.filterButtonActive : ''
                }`}
                onClick={() => setFilter(item)}
              >
                <span>{t(`model_prices.filter_${item}`)}</span>
                <strong>{filterCounts[item]}</strong>
              </button>
            ))}
          </div>
        </div>

        {syncResult?.sourceResults?.some((result) => isOfficialPriceSource(result.source)) ? (
          <div className={styles.sourceResults}>
            {syncResult.sourceResults
              .filter((result) => isOfficialPriceSource(result.source))
              .map((result) => (
                <span
                  key={result.source}
                  className={result.error ? styles.sourceResultError : undefined}
                  title={result.error || undefined}
                >
                  <strong>{resolvePriceSourceLabel(result.source, t)}</strong>
                  {result.error
                    ? t('model_prices.source_result_failed')
                    : t('model_prices.source_result_ok', {
                        models: result.models,
                        skipped: result.skipped,
                      })}
                </span>
              ))}
          </div>
        ) : null}

        {manualEditorOpen ? (
          <div className={styles.compactEditor}>
            <Input
              label={t('usage_stats.model_name')}
              className={styles.compactInput}
              value={draft.model}
              onChange={(event) => setDraftField('model', event.target.value)}
              placeholder="gpt-5.5"
            />
            <Input
              label={`${t('usage_stats.model_price_prompt')} ($/1M)`}
              className={styles.compactInput}
              type="number"
              value={draft.prompt}
              onChange={(event) => setDraftField('prompt', event.target.value)}
              placeholder="0.0000"
              step="0.0001"
            />
            <Input
              label={`${t('usage_stats.model_price_completion')} ($/1M)`}
              className={styles.compactInput}
              type="number"
              value={draft.completion}
              onChange={(event) => setDraftField('completion', event.target.value)}
              placeholder="0.0000"
              step="0.0001"
            />
            <div style={{ display: 'grid', gap: 8 }}>
              <Input
                label={`${t('usage_stats.model_price_cache')} ($/1M)`}
                className={styles.compactInput}
                type="number"
                value={draft.cache}
                onChange={(event) => setDraftField('cache', event.target.value)}
                placeholder="0.0000"
                step="0.0001"
              />
              <Input
                label={`${t('usage_stats.model_price_cache_read')} ($/1M)`}
                className={styles.compactInput}
                type="number"
                value={draft.cacheRead}
                onChange={(event) => setDraftField('cacheRead', event.target.value)}
                placeholder={t('model_prices.optional_price_placeholder')}
                step="0.0001"
              />
              <Input
                label={`${t('usage_stats.model_price_cache_creation')} ($/1M)`}
                className={styles.compactInput}
                type="number"
                value={draft.cacheCreation}
                onChange={(event) => setDraftField('cacheCreation', event.target.value)}
                placeholder={t('model_prices.optional_price_placeholder')}
                step="0.0001"
              />
            </div>
            <div className={styles.compactEditorActions}>
              <Button
                size="xs"
                variant="ghost"
                iconOnly
                aria-label={t('common.cancel')}
                onClick={closeManualEditor}
              >
                <IconX size={14} />
              </Button>
              <Button size="xs" onClick={() => void handleSaveDraft()}>
                {t('common.save')}
              </Button>
            </div>
            {(modelPrices[draft.model.trim()]?.contextTiers?.length ?? 0) +
              (modelPrices[draft.model.trim()]?.serviceTiers?.length ?? 0) >
            0 ? (
              <div className={styles.tierClearNotice}>
                {t('model_prices.manual_clears_pricing_rules', {
                  count:
                    (modelPrices[draft.model.trim()]?.contextTiers?.length ?? 0) +
                    (modelPrices[draft.model.trim()]?.serviceTiers?.length ?? 0),
                })}
              </div>
            ) : null}
          </div>
        ) : null}

        {loading || usageSummaryLoading ? (
          <div className={styles.emptyState}>{t('common.loading')}</div>
        ) : visibleRows.length === 0 ? (
          <div className={styles.emptyState}>
            {rows.length === 0 && !search && filter === 'all'
              ? t('model_prices.no_current_models')
              : t('model_prices.empty')}
          </div>
        ) : (
          <div className={styles.modelCardsSection}>
            <div className={styles.modelCardsHeader}>
              <div>
                <span className={styles.sectionEyebrow}>{t('model_prices.official_price')}</span>
                <h2>{t('model_prices.current_models_title')}</h2>
                <p>{t('model_prices.current_models_hint')}</p>
              </div>
              <span className={styles.currentCount}>
                {t('model_prices.current_count', { count: currentRows.length })}
              </span>
            </div>
            <div className={styles.modelCardsGrid}>
              {visibleRows.map((row) => {
                const candidates =
                  candidateSets.find(
                    (candidateSet) =>
                      normalizeModelIdentity(candidateSet.model) ===
                      normalizeModelIdentity(row.model)
                  )?.candidates ?? [];
                const candidateGroups = groupModelPriceCandidatesBySource(candidates);
                const requestedCandidateIdentity = selectedCandidates[row.model] || '';
                const selectedCandidate =
                  candidates.find(
                    (candidate) =>
                      getModelPriceCandidateIdentity(candidate) === requestedCandidateIdentity
                  ) ?? candidates[0];
                const selectedCandidateIdentity = selectedCandidate
                  ? getModelPriceCandidateIdentity(selectedCandidate)
                  : '';
                const displayPrice = row.price ?? selectedCandidate?.price;
                const displaySource = row.price?.source ?? selectedCandidate?.price.source;
                const displaySourceModelId =
                  row.price?.sourceModelId ?? selectedCandidate?.sourceModelId;
                const contextTiers = displayPrice?.contextTiers ?? [];
                const serviceTiers = displayPrice?.serviceTiers ?? [];
                const statusKey = row.hasPrice
                  ? row.calls > 0
                    ? 'status_current'
                    : 'status_saved'
                  : candidates.length > 0
                    ? 'status_candidate'
                    : 'status_missing';
                const priceMetrics = [
                  { label: t('usage_stats.model_price_prompt'), value: displayPrice?.prompt },
                  {
                    label: t('usage_stats.model_price_completion'),
                    value: displayPrice?.completion,
                  },
                  { label: t('usage_stats.model_price_cache'), value: displayPrice?.cache },
                  {
                    label: t('usage_stats.model_price_cache_read'),
                    value: displayPrice?.cacheRead,
                  },
                  {
                    label: t('usage_stats.model_price_cache_creation'),
                    value: displayPrice?.cacheCreation,
                  },
                ];

                return (
                  <article
                    key={row.model}
                    className={`${styles.priceCard} ${!row.hasPrice ? styles.priceCardMissing : ''}`}
                  >
                    <div className={styles.priceCardHeader}>
                      <div className={styles.priceCardIdentity}>
                        <span className={styles.statusBadge}>{t(`model_prices.${statusKey}`)}</span>
                        <h3 title={row.model}>{row.model}</h3>
                        <span className={styles.cardCalls}>
                          {row.calls} {t('model_prices.calls')}
                        </span>
                      </div>
                      {row.hasPrice ? (
                        <div className={styles.rowActions}>
                          <button
                            type="button"
                            className={styles.iconAction}
                            title={t('common.edit')}
                            aria-label={t('common.edit')}
                            onClick={() => openManualEditor(row.model, row.price)}
                          >
                            <IconPencil size={14} />
                          </button>
                          <button
                            type="button"
                            className={styles.iconAction}
                            title={t('common.delete')}
                            aria-label={t('common.delete')}
                            onClick={() => void handleDelete(row.model)}
                          >
                            <IconTrash2 size={14} />
                          </button>
                        </div>
                      ) : null}
                    </div>

                    <div className={styles.sourceContent}>
                      <div className={styles.sourceLine}>
                        <span className={styles.sourceLabel}>
                          {t('model_prices.official_price')}
                        </span>
                        <span className={styles.sourceBadge}>
                          {displaySource
                            ? resolvePriceSourceLabel(displaySource, t)
                            : t('model_prices.status_missing')}
                        </span>
                      </div>
                      {displaySourceModelId ? (
                        <span className={styles.sourceModelId} title={displaySourceModelId}>
                          {t('model_prices.source_model')}: {displaySourceModelId}
                        </span>
                      ) : null}
                    </div>

                    <div className={styles.priceMetrics}>
                      {priceMetrics.map((metric) => (
                        <div key={metric.label} className={styles.priceMetric}>
                          <span>{metric.label}</span>
                          <strong>{formatPriceUnit(metric.value)}</strong>
                        </div>
                      ))}
                    </div>

                    {contextTiers.length > 0 || serviceTiers.length > 0 ? (
                      <div className={styles.tierList} aria-label={t('model_prices.pricing_rules')}>
                        {contextTiers.map((tier) => {
                          const effectivePrice = resolveContextTierDisplayPrice(displayPrice, tier);
                          return (
                            <span
                              key={tier.thresholdTokens}
                              className={styles.tierBadge}
                              title={`${t('usage_stats.model_price_prompt')}: ${formatPriceUnit(effectivePrice.prompt)} · ${t('usage_stats.model_price_completion')}: ${formatPriceUnit(effectivePrice.completion)}`}
                            >
                              <strong>{`>${formatContextThreshold(tier.thresholdTokens)}`}</strong>
                              <small>
                                {formatPriceUnit(effectivePrice.prompt)} /{' '}
                                {formatPriceUnit(effectivePrice.completion)}
                              </small>
                            </span>
                          );
                        })}
                        {serviceTiers.map((tier) => {
                          const effectivePrice = resolveServiceTierDisplayPrice(displayPrice, tier);
                          return (
                            <span
                              key={`${tier.mode}:${tier.serviceTier}`}
                              className={styles.tierBadge}
                              title={`${t('usage_stats.model_price_prompt')}: ${formatPriceUnit(effectivePrice.prompt)} · ${t('usage_stats.model_price_completion')}: ${formatPriceUnit(effectivePrice.completion)}`}
                            >
                              <strong>{formatServiceTierRule(tier)}</strong>
                              <small>
                                {formatPriceUnit(effectivePrice.prompt)} /{' '}
                                {formatPriceUnit(effectivePrice.completion)}
                              </small>
                            </span>
                          );
                        })}
                      </div>
                    ) : null}

                    {!row.hasPrice ? (
                      <div className={styles.missingPriceNotice}>
                        <span>
                          {candidates.length > 0
                            ? t('model_prices.needs_confirmation')
                            : t('model_prices.no_price')}
                        </span>
                        {candidates.length > 0 && selectedCandidate ? (
                          <div className={styles.candidateControl}>
                            <select
                              value={selectedCandidateIdentity}
                              onChange={(event) =>
                                setSelectedCandidates((previous) => ({
                                  ...previous,
                                  [row.model]: event.target.value,
                                }))
                              }
                              aria-label={t('model_prices.candidate_select')}
                            >
                              {candidateGroups.map((group) => (
                                <optgroup
                                  key={group.source}
                                  label={resolvePriceSourceLabel(group.source, t)}
                                >
                                  {group.candidates.map((candidate) => {
                                    const identity = getModelPriceCandidateIdentity(candidate);
                                    return (
                                      <option key={identity} value={identity}>
                                        {`${candidate.sourceModelId} · ${Math.round(candidate.score * 100)}%`}
                                      </option>
                                    );
                                  })}
                                </optgroup>
                              ))}
                            </select>
                            <button
                              type="button"
                              className={styles.confirmButton}
                              onClick={() =>
                                void handleConfirmCandidate(row.model, selectedCandidate)
                              }
                            >
                              <span>{t('model_prices.confirm_candidate')}</span>
                            </button>
                          </div>
                        ) : (
                          <button
                            type="button"
                            className={styles.confirmButton}
                            onClick={() => openManualEditor(row.model)}
                          >
                            <span>{t('model_prices.add_manual')}</span>
                          </button>
                        )}
                      </div>
                    ) : null}
                  </article>
                );
              })}
            </div>
          </div>
        )}

        {syncResult?.unmatched?.length ? (
          <div className={styles.unmatchedBlock}>
            <strong>{t('model_prices.unmatched_title')}</strong>
            <span>{syncResult.unmatched.join(', ')}</span>
          </div>
        ) : null}
      </section>
    </div>
  );
}
