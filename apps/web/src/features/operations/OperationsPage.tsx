import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { LineSeriesOption } from 'echarts/charts';
import type {
  GridComponentOption,
  LegendComponentOption,
  TooltipComponentOption,
} from 'echarts/components';
import type { ComposeOption } from 'echarts/core';
import { EChartsView } from '@/components/charts/EChartsView';
import { Button } from '@/components/ui/Button';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { usageServiceApi, type ServerOperationsSnapshot } from '@/services/api/usageService';
import { useAuthStore, useThemeStore } from '@/stores';
import {
  appendOperationsSample,
  OPERATIONS_HISTORY_LIMIT,
  OPERATIONS_POLL_INTERVAL_MS,
} from './operationsModel';
import styles from './OperationsPage.module.scss';

type OperationsChartOption = ComposeOption<
  GridComponentOption | LegendComponentOption | LineSeriesOption | TooltipComponentOption
>;

interface ChartTheme {
  axis: string;
  line: string;
  split: string;
  tooltipBackground: string;
  tooltipBorder: string;
  tooltipText: string;
  cpu: string;
  memory: string;
  receive: string;
  transmit: string;
}

interface MetricCardProps {
  label: string;
  value: string;
  detail: string;
  secondary?: string;
  percent?: number;
  tone: 'cpu' | 'memory' | 'disk' | 'network';
}

const chartThemes: Record<'light' | 'dark', ChartTheme> = {
  light: {
    axis: '#64748b',
    line: '#cbd5e1',
    split: '#e2e8f0',
    tooltipBackground: '#ffffff',
    tooltipBorder: '#cbd5e1',
    tooltipText: '#0f172a',
    cpu: '#2563eb',
    memory: '#0f766e',
    receive: '#7c3aed',
    transmit: '#b45309',
  },
  dark: {
    axis: '#94a3b8',
    line: '#475569',
    split: '#26364f',
    tooltipBackground: '#111c2e',
    tooltipBorder: '#475569',
    tooltipText: '#f8fafc',
    cpu: '#60a5fa',
    memory: '#2dd4bf',
    receive: '#a78bfa',
    transmit: '#fbbf24',
  },
};

const clampPercent = (value: number) => Math.min(100, Math.max(0, value || 0));

const formatPercent = (value: number) => `${clampPercent(value).toFixed(1)}%`;

const formatBytes = (value: number): string => {
  if (!Number.isFinite(value) || value <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const unitIndex = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const scaled = value / 1024 ** unitIndex;
  const digits = unitIndex > 0 && scaled < 10 ? 1 : 0;
  return `${scaled.toFixed(digits)} ${units[unitIndex]}`;
};

const formatRate = (value: number) => `${formatBytes(value)}/s`;

const formatDuration = (
  value: number,
  labels: { day: string; hour: string; minute: string; second: string }
): string => {
  const seconds = Math.max(0, Math.floor(value || 0));
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  if (days > 0) return `${days}${labels.day} ${hours}${labels.hour}`;
  if (hours > 0) return `${hours}${labels.hour} ${minutes}${labels.minute}`;
  if (minutes > 0) return `${minutes}${labels.minute}`;
  return `${seconds}${labels.second}`;
};

const formatTimeLabel = (timestampMs: number, locale: string) =>
  new Date(timestampMs).toLocaleTimeString(locale, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });

const buildBaseChartOption = (
  samples: ServerOperationsSnapshot[],
  locale: string,
  theme: ChartTheme,
  valueFormatter: (value: number) => string
): OperationsChartOption => ({
  animation: false,
  backgroundColor: 'transparent',
  grid: { top: 18, right: 16, bottom: 42, left: 12, containLabel: true },
  legend: {
    bottom: 0,
    icon: 'roundRect',
    itemHeight: 3,
    itemWidth: 18,
    textStyle: { color: theme.axis, fontSize: 12 },
  },
  tooltip: {
    trigger: 'axis',
    confine: true,
    backgroundColor: theme.tooltipBackground,
    borderColor: theme.tooltipBorder,
    borderWidth: 1,
    textStyle: { color: theme.tooltipText },
    valueFormatter: (value) => valueFormatter(Number(value)),
  },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    data: samples.map((sample) => formatTimeLabel(sample.timestampMs, locale)),
    axisLabel: { color: theme.axis, fontSize: 11, hideOverlap: true },
    axisLine: { lineStyle: { color: theme.line } },
    axisTick: { show: false },
  },
  yAxis: {
    type: 'value',
    axisLabel: { color: theme.axis, formatter: (value: number) => valueFormatter(value) },
    axisLine: { show: false },
    axisTick: { show: false },
    splitLine: { lineStyle: { color: theme.split, type: 'dashed' } },
  },
});

const buildResourceChartOption = (
  samples: ServerOperationsSnapshot[],
  locale: string,
  theme: ChartTheme,
  cpuLabel: string,
  memoryLabel: string
): OperationsChartOption => ({
  ...buildBaseChartOption(samples, locale, theme, (value) => `${value.toFixed(0)}%`),
  yAxis: {
    type: 'value',
    min: 0,
    max: 100,
    interval: 25,
    axisLabel: { color: theme.axis, formatter: (value: number) => `${value}%` },
    axisLine: { show: false },
    axisTick: { show: false },
    splitLine: { lineStyle: { color: theme.split, type: 'dashed' } },
  },
  series: [
    {
      name: cpuLabel,
      type: 'line',
      data: samples.map((sample) => Number(sample.cpu.usagePercent.toFixed(2))),
      lineStyle: { color: theme.cpu, width: 2 },
      itemStyle: { color: theme.cpu },
      areaStyle: { color: theme.cpu, opacity: 0.08 },
      showSymbol: samples.length <= 1,
      symbolSize: 6,
    },
    {
      name: memoryLabel,
      type: 'line',
      data: samples.map((sample) => Number(sample.memory.usagePercent.toFixed(2))),
      lineStyle: { color: theme.memory, width: 2, type: 'dashed' },
      itemStyle: { color: theme.memory },
      areaStyle: { color: theme.memory, opacity: 0.05 },
      showSymbol: samples.length <= 1,
      symbolSize: 6,
    },
  ],
});

const buildNetworkChartOption = (
  samples: ServerOperationsSnapshot[],
  locale: string,
  theme: ChartTheme,
  receiveLabel: string,
  transmitLabel: string
): OperationsChartOption => ({
  ...buildBaseChartOption(samples, locale, theme, formatRate),
  series: [
    {
      name: receiveLabel,
      type: 'line',
      data: samples.map((sample) => Math.round(sample.network.receiveBytesPerSecond)),
      lineStyle: { color: theme.receive, width: 2 },
      itemStyle: { color: theme.receive },
      areaStyle: { color: theme.receive, opacity: 0.08 },
      showSymbol: samples.length <= 1,
      symbolSize: 6,
    },
    {
      name: transmitLabel,
      type: 'line',
      data: samples.map((sample) => Math.round(sample.network.transmitBytesPerSecond)),
      lineStyle: { color: theme.transmit, width: 2, type: 'dashed' },
      itemStyle: { color: theme.transmit },
      showSymbol: samples.length <= 1,
      symbolSize: 6,
    },
  ],
});

function MetricCard({ label, value, detail, secondary, percent, tone }: MetricCardProps) {
  return (
    <section className={`${styles.metricCard} ${styles[tone]}`}>
      <div className={styles.metricHeading}>
        <span className={styles.metricIcon} aria-hidden="true" />
        <h2>{label}</h2>
      </div>
      <div className={styles.metricValue}>{value}</div>
      <p className={styles.metricDetail}>{detail}</p>
      {typeof percent === 'number' ? (
        <div className={styles.progressTrack} aria-hidden="true">
          <span className={styles.progressValue} style={{ width: `${clampPercent(percent)}%` }} />
        </div>
      ) : null}
      {secondary ? <p className={styles.metricSecondary}>{secondary}</p> : null}
    </section>
  );
}

export function OperationsPage() {
  const { t, i18n } = useTranslation();
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const apiBase = useAuthStore((state) => state.apiBase);
  const featureAvailability = usePanelFeatureAvailability();
  const serviceBase = featureAvailability.managerServiceBase || apiBase;
  const [samples, setSamples] = useState<ServerOperationsSnapshot[]>([]);
  const [paused, setPaused] = useState(false);
  const [initialLoading, setInitialLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const samplesRef = useRef<ServerOperationsSnapshot[]>([]);
  const inFlightRef = useRef(false);
  const abortRef = useRef<AbortController | null>(null);

  const loadMetrics = useCallback(async () => {
    if (!serviceBase || inFlightRef.current) return;
    inFlightRef.current = true;
    const controller = new AbortController();
    abortRef.current = controller;
    if (samplesRef.current.length === 0) setInitialLoading(true);
    setRefreshing(true);
    try {
      const snapshot = await usageServiceApi.getOperationsMetrics(serviceBase, controller.signal);
      if (controller.signal.aborted) return;
      setSamples((current) => {
        const next = appendOperationsSample(current, snapshot, OPERATIONS_HISTORY_LIMIT);
        samplesRef.current = next;
        return next;
      });
      setError('');
    } catch (loadError) {
      if (controller.signal.aborted) return;
      const message = loadError instanceof Error ? loadError.message : '';
      setError(message || t('operations.load_failed'));
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
      inFlightRef.current = false;
      if (!controller.signal.aborted) {
        setInitialLoading(false);
        setRefreshing(false);
      }
    }
  }, [serviceBase, t]);

  useEffect(() => {
    if (paused || !serviceBase) return;
    void loadMetrics();
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void loadMetrics();
    }, OPERATIONS_POLL_INTERVAL_MS);
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') void loadMetrics();
    };
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [loadMetrics, paused, serviceBase]);

  useEffect(
    () => () => {
      abortRef.current?.abort();
    },
    []
  );

  const latest = samples.length > 0 ? samples[samples.length - 1] : undefined;
  const chartTheme = chartThemes[resolvedTheme];
  const resourceChartOption = useMemo(
    () =>
      buildResourceChartOption(
        samples,
        i18n.language,
        chartTheme,
        t('operations.cpu_series'),
        t('operations.memory_series')
      ),
    [chartTheme, i18n.language, samples, t]
  );
  const networkChartOption = useMemo(
    () =>
      buildNetworkChartOption(
        samples,
        i18n.language,
        chartTheme,
        t('operations.receive_series'),
        t('operations.transmit_series')
      ),
    [chartTheme, i18n.language, samples, t]
  );
  const durationLabels = useMemo(
    () => ({
      day: t('operations.day_short'),
      hour: t('operations.hour_short'),
      minute: t('operations.minute_short'),
      second: t('operations.second_short'),
    }),
    [t]
  );
  const statusTone = error ? 'warning' : paused ? 'paused' : 'live';
  const statusLabel = error
    ? t('operations.status_interrupted')
    : paused
      ? t('operations.status_paused')
      : t('operations.status_live');
  const lastUpdated = latest
    ? new Date(latest.timestampMs).toLocaleTimeString(i18n.language)
    : t('operations.waiting');

  return (
    <div className={styles.page}>
      <header className={styles.pageHeader}>
        <div className={styles.headerIntro}>
          <h1>{t('operations.title')}</h1>
          <p>{t('operations.description')}</p>
          <div className={styles.statusLine} aria-live="polite">
            <span className={`${styles.statusPill} ${styles[statusTone]}`}>
              <span className={styles.statusDot} aria-hidden="true" />
              {statusLabel}
            </span>
            <span>{t('operations.last_updated', { time: lastUpdated })}</span>
            <span className={styles.pollHint}>
              {t('operations.poll_summary', {
                seconds: OPERATIONS_POLL_INTERVAL_MS / 1000,
                minutes: (OPERATIONS_HISTORY_LIMIT * OPERATIONS_POLL_INTERVAL_MS) / 60_000,
              })}
            </span>
          </div>
        </div>
        <div className={styles.headerActions}>
          <Button variant="secondary" onClick={() => void loadMetrics()} disabled={refreshing}>
            {refreshing ? t('operations.refreshing') : t('common.refresh')}
          </Button>
          <Button
            variant={paused ? 'primary' : 'secondary'}
            onClick={() => setPaused((value) => !value)}
          >
            {paused ? t('operations.resume') : t('operations.pause')}
          </Button>
        </div>
      </header>

      {initialLoading && !latest ? (
        <section className={styles.loadingState} aria-label={t('operations.loading')}>
          <LoadingSpinner size={28} />
          <p>{t('operations.loading')}</p>
        </section>
      ) : null}

      {!initialLoading && !latest ? (
        <section className={styles.errorState} role="alert">
          <h2>{t('operations.load_failed')}</h2>
          <p>{error || t('operations.load_failed_hint')}</p>
          <Button onClick={() => void loadMetrics()}>{t('common.retry')}</Button>
        </section>
      ) : null}

      {latest ? (
        <>
          {error ? (
            <div className={styles.warningBanner} role="status">
              <strong>{t('operations.data_delayed')}</strong>
              <span>{error}</span>
            </div>
          ) : null}

          <div className={styles.metricGrid}>
            <MetricCard
              label={t('operations.metric_cpu')}
              value={formatPercent(latest.cpu.usagePercent)}
              detail={t('operations.load_average', {
                load1: latest.cpu.load1.toFixed(2),
                load5: latest.cpu.load5.toFixed(2),
                load15: latest.cpu.load15.toFixed(2),
              })}
              secondary={t('operations.cpu_cores_value', { count: latest.cpu.cores })}
              percent={latest.cpu.usagePercent}
              tone="cpu"
            />
            <MetricCard
              label={t('operations.metric_memory')}
              value={formatPercent(latest.memory.usagePercent)}
              detail={t('operations.used_of_total', {
                used: formatBytes(latest.memory.usedBytes),
                total: formatBytes(latest.memory.totalBytes),
              })}
              secondary={t('operations.available_value', {
                value: formatBytes(latest.memory.availableBytes),
              })}
              percent={latest.memory.usagePercent}
              tone="memory"
            />
            <MetricCard
              label={t('operations.metric_disk')}
              value={formatPercent(latest.disk.usagePercent)}
              detail={t('operations.used_of_total', {
                used: formatBytes(latest.disk.usedBytes),
                total: formatBytes(latest.disk.totalBytes),
              })}
              secondary={t('operations.root_available', {
                mount: latest.disk.mountPoint,
                value: formatBytes(latest.disk.availableBytes),
              })}
              percent={latest.disk.usagePercent}
              tone="disk"
            />
            <MetricCard
              label={t('operations.metric_network')}
              value={formatRate(
                latest.network.receiveBytesPerSecond + latest.network.transmitBytesPerSecond
              )}
              detail={t('operations.network_receive', {
                value: formatRate(latest.network.receiveBytesPerSecond),
              })}
              secondary={t('operations.network_transmit', {
                value: formatRate(latest.network.transmitBytesPerSecond),
              })}
              tone="network"
            />
          </div>

          <div className={styles.chartGrid}>
            <section className={styles.panel}>
              <div className={styles.panelHeader}>
                <div>
                  <h2>{t('operations.resource_trend_title')}</h2>
                  <p>{t('operations.resource_trend_desc')}</p>
                </div>
              </div>
              <EChartsView
                option={resourceChartOption}
                className={styles.chart}
                ariaLabel={t('operations.resource_chart_label')}
              />
            </section>

            <section className={styles.panel}>
              <div className={styles.panelHeader}>
                <div>
                  <h2>{t('operations.network_trend_title')}</h2>
                  <p>{t('operations.network_trend_desc')}</p>
                </div>
              </div>
              <EChartsView
                option={networkChartOption}
                className={styles.chart}
                ariaLabel={t('operations.network_chart_label')}
              />
            </section>
          </div>

          <section className={`${styles.panel} ${styles.detailsPanel}`}>
            <div className={styles.panelHeader}>
              <div>
                <h2>{t('operations.server_details_title')}</h2>
                <p>{t('operations.server_details_desc')}</p>
              </div>
            </div>
            <dl className={styles.detailsGrid}>
              <div className={styles.detailItem}>
                <dt>{t('operations.hostname')}</dt>
                <dd>{latest.host.hostname || '-'}</dd>
              </div>
              <div className={styles.detailItem}>
                <dt>{t('operations.operating_system')}</dt>
                <dd>{latest.host.operatingSystem || '-'}</dd>
              </div>
              <div className={styles.detailItem}>
                <dt>{t('operations.architecture')}</dt>
                <dd>{latest.host.architecture || '-'}</dd>
              </div>
              <div className={styles.detailItem}>
                <dt>{t('operations.cpu_cores')}</dt>
                <dd>{latest.cpu.cores.toLocaleString(i18n.language)}</dd>
              </div>
              <div className={styles.detailItem}>
                <dt>{t('operations.system_uptime')}</dt>
                <dd>{formatDuration(latest.host.uptimeSeconds, durationLabels)}</dd>
              </div>
              <div className={styles.detailItem}>
                <dt>{t('operations.service_uptime')}</dt>
                <dd>{formatDuration(latest.process.uptimeSeconds, durationLabels)}</dd>
              </div>
              <div className={styles.detailItem}>
                <dt>{t('operations.process_memory')}</dt>
                <dd>{formatBytes(latest.process.residentMemoryBytes)}</dd>
              </div>
              <div className={styles.detailItem}>
                <dt>{t('operations.goroutines')}</dt>
                <dd>{latest.process.goroutines.toLocaleString(i18n.language)}</dd>
              </div>
            </dl>
            <p className={styles.sourceNote}>{t('operations.source_note')}</p>
          </section>
        </>
      ) : null}
    </div>
  );
}
