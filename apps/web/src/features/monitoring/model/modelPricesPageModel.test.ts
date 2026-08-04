import { describe, expect, it } from 'vitest';
import {
  applyCandidatePrice,
  buildPriceFromDraft,
  buildModelPriceRows,
  buildModelPriceSummary,
  buildSyncPriceModelsFromSummary,
  filterModelPriceRows,
  formatContextThreshold,
  formatServiceTierRule,
  getModelPriceCandidateIdentity,
  groupModelPriceCandidatesBySource,
  resolveContextTierDisplayPrice,
  resolveServiceTierDisplayPrice,
} from './modelPricesPageModel';

const usageSummary = {
  sampled_events: 1,
  total_events: 1,
  truncated: false,
  models: [
    {
      model: 'alias-fast',
      calls: 1,
      requested_calls: 1,
      resolved_calls: 0,
    },
    {
      model: 'gpt-5.5',
      calls: 1,
      requested_calls: 0,
      resolved_calls: 1,
    },
  ],
};

describe('modelPricesPageModel', () => {
  it('builds sync models from current usage and removes provider-qualified duplicates', () => {
    expect(
      buildSyncPriceModelsFromSummary(usageSummary, {
        'manual-model': { prompt: 1, completion: 2, cache: 0.5 },
      })
    ).toEqual(['alias-fast', 'gpt-5.5']);
  });

  it('does not show the full price catalog when current usage is unavailable', () => {
    const prices = {
      'manual-model': {
        prompt: 1,
        completion: 2,
        cache: 0.5,
        source: 'litellm',
        sourceModelId: 'manual-model',
      },
    };

    expect(buildSyncPriceModelsFromSummary(null, prices)).toEqual([]);
    expect(buildModelPriceRows(null, prices)).toEqual([]);
  });

  it('marks missing models with candidates before saved rows', () => {
    const rows = buildModelPriceRows(
      usageSummary,
      {
        'gpt-5.5': {
          prompt: 1,
          completion: 2,
          cache: 0.5,
          source: 'litellm',
          sourceModelId: 'gpt-5.5',
        },
      },
      [
        {
          model: 'alias-fast',
          candidates: [
            {
              sourceModelId: 'openai/gpt-5.5',
              score: 0.75,
              reason: 'similar',
              price: {
                prompt: 1,
                completion: 2,
                cache: 0.5,
                source: 'litellm',
                sourceModelId: 'openai/gpt-5.5',
              },
            },
          ],
        },
      ]
    );

    expect(rows[0]).toMatchObject({
      model: 'alias-fast',
      hasPrice: false,
      candidateCount: 1,
      requestedCalls: 1,
    });
    expect(rows[1]).toMatchObject({
      model: 'gpt-5.5',
      calls: 1,
      requestedCalls: 0,
      resolvedCalls: 1,
    });
    expect(buildModelPriceSummary(rows)).toMatchObject({
      total: 2,
      saved: 1,
      missing: 1,
      candidates: 1,
    });
    expect(filterModelPriceRows(rows, 'candidates', '')).toHaveLength(1);
  });

  it('applies a candidate under the local model name', () => {
    const next = applyCandidatePrice({}, 'alias-fast', {
      sourceModelId: 'openai/gpt-5.5',
      score: 0.75,
      reason: 'similar',
      price: {
        prompt: 1,
        completion: 2,
        cache: 0.5,
        source: 'litellm',
        sourceModelId: 'openai/gpt-5.5',
      },
    });

    expect(next['alias-fast']).toMatchObject({
      prompt: 1,
      completion: 2,
      cache: 0.5,
      source: 'litellm',
      sourceModelId: 'openai/gpt-5.5',
    });
  });

  it('aggregates provider variants under one model and prefers the official base price', () => {
    const rows = buildModelPriceRows(
      {
        ...usageSummary,
        models: [
          { model: 'openai/gpt-5.5', calls: 2, requested_calls: 2, resolved_calls: 0 },
          { model: 'gpt-5.5', calls: 3, requested_calls: 3, resolved_calls: 0 },
          { model: 'azure/eu/gpt-5.5', calls: 4, requested_calls: 4, resolved_calls: 0 },
        ],
      },
      {
        'openrouter/gpt-5.5': {
          prompt: 99,
          completion: 99,
          cache: 99,
          source: 'openrouter',
          sourceModelId: 'openrouter/gpt-5.5',
        },
        'azure/eu/gpt-5.5': {
          prompt: 88,
          completion: 88,
          cache: 88,
          source: 'litellm',
          sourceModelId: 'azure/eu/gpt-5.5',
        },
        'gpt-5.5': {
          prompt: 5,
          completion: 30,
          cache: 0.5,
          source: 'litellm',
          sourceModelId: 'gpt-5.5',
        },
      }
    );

    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({
      model: 'gpt-5.5',
      calls: 9,
      requestedCalls: 9,
      hasPrice: true,
      price: {
        prompt: 5,
        completion: 30,
        source: 'litellm',
        sourceModelId: 'gpt-5.5',
      },
    });
  });

  it('folds a versioned resolved alias into the requested model', () => {
    const summary = {
      ...usageSummary,
      models: [
        { model: 'grok-build', calls: 2, requested_calls: 2, resolved_calls: 0 },
        { model: 'grok-build-0.1', calls: 2, requested_calls: 0, resolved_calls: 2 },
      ],
    };

    expect(buildSyncPriceModelsFromSummary(summary, {})).toEqual(['grok-build']);
    expect(buildModelPriceRows(summary, {})).toMatchObject([
      {
        model: 'grok-build',
        calls: 4,
        requestedCalls: 2,
        resolvedCalls: 2,
      },
    ]);
  });

  it('does not expose auth-provider or aggregator prices', () => {
    const rows = buildModelPriceRows(
      {
        ...usageSummary,
        models: [{ model: 'grok-4.5', calls: 1, requested_calls: 1, resolved_calls: 0 }],
      },
      {
        'grok-4.5': {
          prompt: 2,
          completion: 6,
          cache: 0,
          source: 'sub2api-baseline',
          sourceModelId: 'grok-4.5',
        },
        'openrouter/grok-4.5': {
          prompt: 2,
          completion: 6,
          cache: 0,
          source: 'openrouter',
          sourceModelId: 'openrouter/grok-4.5',
        },
      }
    );

    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ model: 'grok-4.5', hasPrice: false });
    expect(rows[0].price).toBeUndefined();
  });

  it('keeps identical source model IDs distinct and groups candidates by source', () => {
    const candidates = [
      {
        sourceModelId: 'openai/gpt-5.5',
        score: 0.94,
        reason: 'same-model-with-provider-prefix',
        price: { prompt: 1, completion: 2, cache: 0.5, source: 'models.dev' },
      },
      {
        sourceModelId: 'openai/gpt-5.5',
        score: 0.94,
        reason: 'same-model-with-provider-prefix',
        price: { prompt: 1.1, completion: 2.1, cache: 0.6, source: 'openrouter' },
      },
    ];

    expect(getModelPriceCandidateIdentity(candidates[0])).not.toBe(
      getModelPriceCandidateIdentity(candidates[1])
    );
    expect(groupModelPriceCandidatesBySource(candidates)).toEqual([
      { source: 'models.dev', candidates: [candidates[0]] },
      { source: 'openrouter', candidates: [candidates[1]] },
    ]);
  });

  it('marks manually entered prices with a manual source', () => {
    expect(
      buildPriceFromDraft({
        model: 'manual-model',
        prompt: '1',
        completion: '2',
        cache: '',
        cacheRead: '',
        cacheCreation: '',
      })
    ).toMatchObject({
      prompt: 1,
      completion: 2,
      cache: 1,
      promptConfigured: true,
      completionConfigured: true,
      cacheReadConfigured: false,
      cacheCreationConfigured: false,
      source: 'manual',
      contextTiers: [],
      serviceTiers: [],
    });
  });

  it('distinguishes blank cache prices from explicitly configured zero prices', () => {
    expect(
      buildPriceFromDraft({
        model: 'gpt-5.6-sol',
        prompt: '0',
        completion: '0',
        cache: '',
        cacheRead: '0',
        cacheCreation: '0',
      })
    ).toMatchObject({
      prompt: 0,
      completion: 0,
      cacheRead: 0,
      cacheCreation: 0,
      promptConfigured: true,
      completionConfigured: true,
      cacheReadConfigured: true,
      cacheCreationConfigured: true,
    });
  });

  it('formats context tier thresholds compactly', () => {
    expect(formatContextThreshold(32_000)).toBe('32K');
    expect(formatContextThreshold(1_000_000)).toBe('1M');
    expect(formatContextThreshold(12_345)).toBe('12,345');
  });

  it('displays inherited tier rates while preserving explicit zero prices', () => {
    expect(
      resolveContextTierDisplayPrice(
        { prompt: 1, completion: 2, cache: 0.5 },
        {
          thresholdTokens: 32_000,
          prompt: 0,
          completion: 0,
          cache: 0,
          promptConfigured: true,
          completionConfigured: false,
        }
      )
    ).toEqual({ prompt: 0, completion: 2 });
  });

  it('displays Fast Mode aliases and inherited service-tier rates', () => {
    const tier = {
      mode: 'fast',
      serviceTier: 'priority',
      prompt: 12.5,
      completion: 0,
      cache: 0,
      promptConfigured: true,
      completionConfigured: false,
    };
    expect(formatServiceTierRule(tier)).toBe('fast/priority');
    expect(resolveServiceTierDisplayPrice({ prompt: 5, completion: 30, cache: 0.5 }, tier)).toEqual(
      {
        prompt: 12.5,
        completion: 30,
      }
    );
  });
});
