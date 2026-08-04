import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { QuotaCard } from './QuotaCard';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => {
      const labels: Record<string, string> = {
        'auth_files.filter_codex': 'Codex',
        'quota_management.weekly_usage_title': 'Weekly usage',
        'quota_management.weekly_usage_tokens': 'Token usage',
        'quota_management.weekly_usage_estimated_cost': 'Estimated price',
        'quota_management.weekly_usage_quota_period': 'Quota week',
        'quota_management.weekly_usage_calendar_period': 'Calendar week',
        'quota_management.weekly_usage_unavailable': 'Weekly usage is unavailable',
      };
      if (key === 'quota_management.weekly_usage_period') {
        return `Period: ${String(options?.start)} to ${String(options?.end)}`;
      }
      return labels[key] ?? key;
    },
    i18n: { resolvedLanguage: 'en-US', language: 'en-US' },
  }),
}));

const getText = (node: ReactTestInstance): string =>
  node.children
    .map((child) => {
      if (typeof child === 'string' || typeof child === 'number') return String(child);
      return getText(child);
    })
    .join('');

describe('QuotaCard weekly usage', () => {
  it('renders token usage in millions and estimated price in dollars', () => {
    let renderer!: ReactTestRenderer;
    act(() => {
      renderer = create(
        <QuotaCard
          item={{ name: 'account.json', type: 'codex', authIndex: 'auth-1' }}
          quota={{ status: 'success' }}
          resolvedTheme="light"
          i18nPrefix="codex_quota"
          cardClassName="codex-card"
          defaultType="codex"
          weeklyUsage={{
            target_key: 'codex:account.json::auth-1',
            period_start_ms: Date.UTC(2026, 7, 1),
            period_end_ms: Date.UTC(2026, 7, 3),
            reset_at_ms: Date.UTC(2026, 7, 8),
            period_source: 'quota_week',
            calls: 3,
            total_tokens: 1_250_000,
            estimated_cost_usd: 2.5,
          }}
          renderQuotaItems={() => <div>quota loaded</div>}
        />
      );
    });

    const text = getText(renderer.root);
    expect(text).toContain('1.25 M');
    expect(text).toContain('≈ $2.50');
    expect(text).toContain('Quota week');

    const summary = renderer.root.find(
      (node) => node.props.role === 'group' && node.props['aria-busy'] === false
    );
    expect(summary.props['aria-label']).toContain('Period:');
  });
});
