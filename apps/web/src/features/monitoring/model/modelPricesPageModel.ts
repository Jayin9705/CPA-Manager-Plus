import type { ModelPrice, ModelPriceContextTier, ModelPriceServiceTier } from '@/utils/usage';
import type {
  ModelPriceUsageSummaryResponse,
  ModelPriceSyncCandidate,
  ModelPriceSyncCandidateSet,
} from '@/services/api/usageService';

export type ModelPriceFilter = 'all' | 'missing' | 'saved' | 'candidates';

export type PriceDraft = {
  model: string;
  prompt: string;
  completion: string;
  cache: string;
  cacheRead: string;
  cacheCreation: string;
};

export type ModelPriceRow = {
  model: string;
  calls: number;
  requestedCalls: number;
  resolvedCalls: number;
  hasPrice: boolean;
  price?: ModelPrice;
  candidateCount: number;
};

export type ModelPriceSummary = {
  total: number;
  saved: number;
  missing: number;
  candidates: number;
};

const OFFICIAL_PRICE_SOURCES = new Set(['litellm', 'official', 'official-site', 'official_site']);
const ROUTER_PRICE_PREFIXES = new Set([
  'azure',
  'azure_ai',
  'vertex_ai',
  'bedrock',
  'bedrock_converse',
  'openrouter',
  'together_ai',
  'deepinfra',
  'fireworks_ai',
  'groq',
  'perplexity',
  'novita',
  'cerebras',
  'sambanova',
]);
const OFFICIAL_VENDOR_PREFIXES = new Set([
  'ai21',
  'amazon',
  'anthropic',
  'cohere',
  'deepseek',
  'google',
  'meta',
  'meta-llama',
  'minimax',
  'mistral',
  'moonshot',
  'openai',
  'qwen',
  'xai',
  'zhipu',
]);

const cleanModelSegments = (value: string): string[] => {
  const segments = value
    .trim()
    .split('/')
    .map((segment) => segment.trim())
    .filter(Boolean);
  if (segments[0]?.toLowerCase() === 'models') {
    segments.shift();
  }
  return segments;
};

export const normalizeModelIdentity = (value: string): string => {
  const segments = cleanModelSegments(value);
  const model = segments[segments.length - 1] || value.trim();
  return model.toLowerCase().replace(/[^a-z0-9]+/g, '');
};

const normalizeModelSlug = (value: string): string => {
  const segments = cleanModelSegments(value);
  const model = segments[segments.length - 1] || value.trim();
  return model
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
};

const isVersionedModelAlias = (base: string, candidate: string): boolean => {
  const baseSlug = normalizeModelSlug(base);
  const candidateSlug = normalizeModelSlug(candidate);
  if (!baseSlug || baseSlug === candidateSlug || !candidateSlug.startsWith(`${baseSlug}-`)) {
    return false;
  }
  const suffix = candidateSlug.slice(baseSlug.length + 1);
  return /^(?:v)?\d/.test(suffix);
};

export const isOfficialPriceSource = (source: string | undefined): boolean =>
  OFFICIAL_PRICE_SOURCES.has(
    String(source || '')
      .trim()
      .toLowerCase()
  );

export const isOfficialModelPrice = (
  price: ModelPrice | undefined,
  fallbackModelId = ''
): boolean => {
  if (!price || !isOfficialPriceSource(price.source)) return false;
  const modelId = String(price.sourceModelId || fallbackModelId).trim();
  if (!modelId) return false;
  const segments = cleanModelSegments(modelId);
  if (segments.length === 0) return false;
  const prefix = segments[0].toLowerCase();
  if (segments.length > 1 && ROUTER_PRICE_PREFIXES.has(prefix)) return false;
  return segments.length === 1 || OFFICIAL_VENDOR_PREFIXES.has(prefix);
};

const modelDisplayPriority = (model: string): number => {
  const segments = cleanModelSegments(model);
  if (segments.length === 0) return 0;
  if (segments.length === 1) return 3;
  return OFFICIAL_VENDOR_PREFIXES.has(segments[0].toLowerCase()) ? 2 : 1;
};

const chooseDisplayModel = (current: string, candidate: string): string => {
  if (modelDisplayPriority(candidate) > modelDisplayPriority(current)) return candidate;
  if (modelDisplayPriority(candidate) < modelDisplayPriority(current)) return current;
  return candidate.length < current.length ? candidate : current;
};

const officialPricePriority = (modelId: string): number => {
  const segments = cleanModelSegments(modelId);
  if (segments.length === 1) return 3;
  return OFFICIAL_VENDOR_PREFIXES.has(segments[0].toLowerCase()) ? 2 : 1;
};

export const findOfficialModelPrice = (
  model: string,
  prices: Record<string, ModelPrice>
): ModelPrice | undefined => {
  const identity = normalizeModelIdentity(model);
  if (!identity) return undefined;
  return Object.entries(prices)
    .filter(
      ([modelId, price]) =>
        normalizeModelIdentity(modelId) === identity && isOfficialModelPrice(price, modelId)
    )
    .sort(([leftModelId], [rightModelId]) => {
      const priority = officialPricePriority(rightModelId) - officialPricePriority(leftModelId);
      return priority || leftModelId.localeCompare(rightModelId);
    })[0]?.[1];
};

export type ModelPriceCandidateGroup = {
  source: string;
  candidates: ModelPriceSyncCandidate[];
};

export const createEmptyPriceDraft = (): PriceDraft => ({
  model: '',
  prompt: '',
  completion: '',
  cache: '',
  cacheRead: '',
  cacheCreation: '',
});

const createConfiguredDraftValue = (value: number | undefined, configured?: boolean): string =>
  configured || Number(value) > 0 ? String(Number(value) || 0) : '';

export const createPriceDraft = (model: string, price?: ModelPrice): PriceDraft => ({
  model,
  prompt: price ? createConfiguredDraftValue(price.prompt, price.promptConfigured) : '',
  completion: price ? createConfiguredDraftValue(price.completion, price.completionConfigured) : '',
  cache: price ? String(price.cache) : '',
  cacheRead: price ? createConfiguredDraftValue(price.cacheRead, price.cacheReadConfigured) : '',
  cacheCreation: price
    ? createConfiguredDraftValue(price.cacheCreation, price.cacheCreationConfigured)
    : '',
});

export const parsePriceValue = (value: string) => {
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
};

export const buildPriceFromDraft = (draft: PriceDraft): ModelPrice | null => {
  const model = draft.model.trim();
  if (!model) return null;
  const prompt = parsePriceValue(draft.prompt);
  const completion = parsePriceValue(draft.completion);
  const cache = draft.cache.trim() === '' ? prompt : parsePriceValue(draft.cache);
  return {
    prompt,
    completion,
    cache,
    cacheRead: parsePriceValue(draft.cacheRead),
    cacheCreation: parsePriceValue(draft.cacheCreation),
    promptConfigured: draft.prompt.trim() !== '',
    completionConfigured: draft.completion.trim() !== '',
    cacheReadConfigured: draft.cacheRead.trim() !== '',
    cacheCreationConfigured: draft.cacheCreation.trim() !== '',
    source: 'manual',
    contextTiers: [],
    serviceTiers: [],
  };
};

export const applyCandidatePrice = (
  prices: Record<string, ModelPrice>,
  model: string,
  candidate: ModelPriceSyncCandidate
): Record<string, ModelPrice> => {
  if (!isOfficialModelPrice(candidate.price, candidate.sourceModelId)) return prices;
  return {
    ...prices,
    [model]: {
      ...candidate.price,
      source: candidate.price.source,
      sourceModelId: candidate.sourceModelId,
    },
  };
};

const deduplicateModelNames = (models: string[]): string[] => {
  const byIdentity = new Map<string, string>();
  models.forEach((model) => {
    const normalized = model.trim();
    const identity = normalizeModelIdentity(normalized);
    if (!identity) return;
    const current = byIdentity.get(identity);
    byIdentity.set(identity, current ? chooseDisplayModel(current, normalized) : normalized);
  });
  return Array.from(byIdentity.values()).sort((left, right) => left.localeCompare(right));
};

const deduplicateUsageModelNames = (items: ModelPriceUsageSummaryResponse['models']): string[] => {
  const requestedModels = items
    .filter((item) => Number(item.requested_calls) > 0)
    .map((item) => item.model)
    .filter(Boolean);
  const resolvedOnlyModels = items
    .filter((item) => Number(item.requested_calls) <= 0)
    .map((item) => item.model)
    .filter(
      (model) => !requestedModels.some((requested) => isVersionedModelAlias(requested, model))
    );
  return deduplicateModelNames([...requestedModels, ...resolvedOnlyModels]);
};

export const getModelPriceCandidateSource = (candidate: ModelPriceSyncCandidate) =>
  candidate.price.source?.trim() || 'sync';

export const getModelPriceCandidateIdentity = (candidate: ModelPriceSyncCandidate) =>
  JSON.stringify([getModelPriceCandidateSource(candidate), candidate.sourceModelId]);

export const groupModelPriceCandidatesBySource = (
  candidates: ModelPriceSyncCandidate[]
): ModelPriceCandidateGroup[] => {
  const groups = new Map<string, ModelPriceSyncCandidate[]>();
  candidates.forEach((candidate) => {
    const source = getModelPriceCandidateSource(candidate);
    const group = groups.get(source);
    if (group) {
      group.push(candidate);
      return;
    }
    groups.set(source, [candidate]);
  });
  return Array.from(groups, ([source, sourceCandidates]) => ({
    source,
    candidates: sourceCandidates,
  }));
};

export const buildSyncPriceModelsFromSummary = (
  summary: ModelPriceUsageSummaryResponse | null,
  _prices: Record<string, ModelPrice>
) => {
  if (!summary) return [];
  return deduplicateUsageModelNames(summary.models ?? []);
};

export const buildCandidateMap = (candidateSets: ModelPriceSyncCandidateSet[] = []) => {
  const map = new Map<string, ModelPriceSyncCandidate[]>();
  candidateSets.forEach((set) => {
    const identity = normalizeModelIdentity(set.model || '');
    const candidates = (Array.isArray(set.candidates) ? set.candidates : []).filter((candidate) =>
      isOfficialModelPrice(candidate.price, candidate.sourceModelId)
    );
    if (!identity || candidates.length === 0) return;
    const existing = map.get(identity) ?? [];
    const seen = new Set(existing.map((candidate) => candidate.sourceModelId));
    map.set(
      identity,
      existing.concat(candidates.filter((candidate) => !seen.has(candidate.sourceModelId)))
    );
  });
  return map;
};

export const buildModelPriceRows = (
  summary: ModelPriceUsageSummaryResponse | null,
  prices: Record<string, ModelPrice>,
  candidateSets: ModelPriceSyncCandidateSet[] = []
): ModelPriceRow[] => {
  const rowMap = new Map<string, ModelPriceRow>();
  const candidateMap = buildCandidateMap(candidateSets);

  const ensureRow = (model: string): ModelPriceRow | undefined => {
    const identity = normalizeModelIdentity(model);
    if (!identity) return undefined;
    const existing = rowMap.get(identity);
    if (existing) {
      existing.model = chooseDisplayModel(existing.model, model.trim());
      return existing;
    }
    const price = findOfficialModelPrice(model, prices);
    const row: ModelPriceRow = {
      model: model.trim(),
      calls: 0,
      requestedCalls: 0,
      resolvedCalls: 0,
      hasPrice: Boolean(price),
      price,
      candidateCount: candidateMap.get(identity)?.length ?? 0,
    };
    rowMap.set(identity, row);
    return row;
  };

  if (!summary) return [];

  summary.models?.forEach((item) => {
    if (!item.model) return;
    const row = ensureRow(item.model);
    if (!row) return;
    row.calls += Number(item.calls) || 0;
    row.requestedCalls += Number(item.requested_calls) || 0;
    row.resolvedCalls += Number(item.resolved_calls) || 0;
  });

  const requestedRows = Array.from(rowMap.entries()).filter(([, row]) => row.requestedCalls > 0);
  Array.from(rowMap.entries()).forEach(([identity, row]) => {
    if (row.requestedCalls > 0) return;
    const requestedRow = requestedRows.find(([, candidate]) =>
      isVersionedModelAlias(candidate.model, row.model)
    );
    if (!requestedRow) return;
    const [, base] = requestedRow;
    base.calls += row.calls;
    base.resolvedCalls += row.resolvedCalls;
    base.candidateCount = Math.max(base.candidateCount, row.candidateCount);
    if (!base.price && row.price) {
      base.price = row.price;
      base.hasPrice = true;
    }
    rowMap.delete(identity);
  });

  return Array.from(rowMap.values()).sort(
    (left, right) =>
      Number(left.hasPrice) - Number(right.hasPrice) ||
      right.candidateCount - left.candidateCount ||
      right.calls - left.calls ||
      left.model.localeCompare(right.model)
  );
};

export const buildModelPriceSummary = (rows: ModelPriceRow[]): ModelPriceSummary => {
  const saved = rows.filter((row) => row.hasPrice).length;
  const candidates = rows.filter((row) => !row.hasPrice && row.candidateCount > 0).length;
  return {
    total: rows.length,
    saved,
    missing: rows.length - saved,
    candidates,
  };
};

export const filterModelPriceRows = (
  rows: ModelPriceRow[],
  filter: ModelPriceFilter,
  search: string
) => {
  const query = search.trim().toLowerCase();
  return rows.filter((row) => {
    if (filter === 'missing' && row.hasPrice) return false;
    if (filter === 'saved' && !row.hasPrice) return false;
    if (filter === 'candidates' && (row.hasPrice || row.candidateCount === 0)) return false;
    if (!query) return true;
    return (
      row.model.toLowerCase().includes(query) ||
      row.price?.sourceModelId?.toLowerCase().includes(query) ||
      row.price?.source?.toLowerCase().includes(query)
    );
  });
};

export const formatPriceUnit = (value: number | undefined) => {
  const num = Number(value);
  return Number.isFinite(num) ? `$${num.toFixed(4)}/1M` : '--';
};

export const resolveContextTierDisplayPrice = (
  price: ModelPrice | undefined,
  tier: ModelPriceContextTier
) => ({
  prompt: tier.promptConfigured ? tier.prompt : price?.prompt,
  completion: tier.completionConfigured ? tier.completion : price?.completion,
});

export const resolveServiceTierDisplayPrice = (
  price: ModelPrice | undefined,
  tier: ModelPriceServiceTier
) => ({
  prompt: tier.promptConfigured ? tier.prompt : price?.prompt,
  completion: tier.completionConfigured ? tier.completion : price?.completion,
});

export const formatServiceTierRule = (tier: ModelPriceServiceTier) => {
  const mode = tier.mode.trim();
  const serviceTier = tier.serviceTier.trim();
  if (!mode) return serviceTier || '--';
  if (!serviceTier || mode.toLowerCase() === serviceTier.toLowerCase()) return mode;
  return `${mode}/${serviceTier}`;
};

export const formatContextThreshold = (value: number) => {
  const tokens = Number(value);
  if (!Number.isFinite(tokens) || tokens <= 0) return '--';
  if (tokens >= 1_000_000 && tokens % 1_000_000 === 0) return `${tokens / 1_000_000}M`;
  if (tokens >= 1_000 && tokens % 1_000 === 0) return `${tokens / 1_000}K`;
  return tokens.toLocaleString('en-US', { maximumFractionDigits: 0 });
};
