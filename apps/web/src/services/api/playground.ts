import { normalizeModelList, type ModelInfo } from '@/utils/models';
import { apiCallApi, getApiCallErrorMessage, type ApiCallResult } from './apiCall';
import { apiClient } from './client';
import { normalizeApiBase } from '@/utils/connection';
import { isDemoMode } from '@/features/demo/demoMode';

const PLAYGROUND_REQUEST_TIMEOUT_MS = 10 * 60 * 1000;

export interface PlaygroundChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string;
}

export interface PlaygroundChatCompletion {
  text: string;
  raw: Record<string, unknown>;
}

export interface PlaygroundImageResult {
  url?: string;
  b64Json?: string;
  mimeType?: string;
  revisedPrompt?: string;
}

export interface PlaygroundImageGenerationOptions {
  model: string;
  prompt: string;
  n?: number;
  size?: string;
  quality?: string;
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

const readString = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const getResponseBody = (result: ApiCallResult): unknown => {
  if (result.statusCode < 200 || result.statusCode >= 300) {
    throw new Error(getApiCallErrorMessage(result));
  }

  if (result.body !== null) {
    if (typeof result.body !== 'string') return result.body;
    try {
      return JSON.parse(result.body) as unknown;
    } catch {
      throw new Error('The upstream response was not valid JSON');
    }
  }
  const text = result.bodyText.trim();
  if (!text) return {};

  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new Error('The upstream response was not valid JSON');
  }
};

const buildCPAAPIBase = (baseUrl: string): string => {
  const normalized = normalizeApiBase(baseUrl).replace(/\/+$/g, '');
  return normalized.replace(/\/v1(?:\/.*)?$/i, '');
};

export const buildPlaygroundEndpoint = (baseUrl: string, path: string): string => {
  const base = buildCPAAPIBase(baseUrl);
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return base ? `${base}/v1${normalizedPath}` : '';
};

export const buildManagerPlaygroundEndpoint = (baseUrl: string, path: string): string => {
  const base = normalizeApiBase(baseUrl).replace(/\/+$/g, '');
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return base ? `${base}/v0/management/playground${normalizedPath}` : '';
};

const buildRequestHeaders = (apiKey?: string): Record<string, string> => {
  const trimmedApiKey = apiKey?.trim() || '';
  return {
    Accept: 'application/json',
    'Content-Type': 'application/json',
    ...(trimmedApiKey ? { Authorization: `Bearer ${trimmedApiKey}` } : {}),
  };
};

const callJSON = async (payload: {
  method: 'GET' | 'POST';
  url: string;
  apiKey?: string;
  data?: unknown;
  managerBaseUrl?: string;
  managerPath?: string;
}): Promise<unknown> => {
  if (payload.managerBaseUrl && !(__DEMO_SITE__ && isDemoMode())) {
    const endpoint = buildManagerPlaygroundEndpoint(
      payload.managerBaseUrl,
      payload.managerPath || ''
    );
    if (!endpoint) throw new Error('Manager Server address is not configured');

    const response = await apiClient.post<unknown>(endpoint, payload.data, {
      timeout: PLAYGROUND_REQUEST_TIMEOUT_MS,
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        ...(payload.apiKey?.trim() ? { 'X-CPA-API-Key': payload.apiKey.trim() } : {}),
      },
    });
    if (typeof response !== 'string') return response;
    try {
      return JSON.parse(response) as unknown;
    } catch {
      throw new Error('The upstream response was not valid JSON');
    }
  }

  const result = await apiCallApi.request(
    {
      method: payload.method,
      url: payload.url,
      header: buildRequestHeaders(payload.apiKey),
      data: payload.data === undefined ? undefined : JSON.stringify(payload.data),
    },
    { timeout: PLAYGROUND_REQUEST_TIMEOUT_MS }
  );

  return getResponseBody(result);
};

const readChatContent = (content: unknown): string => {
  if (typeof content === 'string') return content.trim();
  if (!Array.isArray(content)) return '';

  return content
    .map((part) => {
      if (typeof part === 'string') return part;
      if (!isRecord(part)) return '';
      if (typeof part.text === 'string') return part.text;
      if (typeof part.content === 'string') return part.content;
      return '';
    })
    .filter((part) => part !== '')
    .join('')
    .trim();
};

export const normalizeChatCompletion = (payload: unknown): PlaygroundChatCompletion => {
  const raw = isRecord(payload) ? payload : {};
  const choices = Array.isArray(raw.choices) ? raw.choices : [];
  const firstChoice = isRecord(choices[0]) ? choices[0] : {};
  const message = isRecord(firstChoice.message) ? firstChoice.message : {};
  const text =
    readChatContent(message.content) ||
    readString(firstChoice.text) ||
    readString(raw.output_text) ||
    readString(message.reasoning_content);

  return { text, raw };
};

export const normalizeImageResults = (payload: unknown): PlaygroundImageResult[] => {
  const data = isRecord(payload) && Array.isArray(payload.data) ? payload.data : [];

  return data.reduce<PlaygroundImageResult[]>((results, item) => {
    if (!isRecord(item)) return results;

    const image: PlaygroundImageResult = {
      url: readString(item.url) || undefined,
      b64Json: readString(item.b64_json) || readString(item.b64Json) || undefined,
      mimeType: readString(item.mime_type) || readString(item.mimeType) || undefined,
      revisedPrompt: readString(item.revised_prompt) || readString(item.revisedPrompt) || undefined,
    };
    if (image.url || image.b64Json) results.push(image);
    return results;
  }, []);
};

export const playgroundApi = {
  async fetchModels(baseUrl: string, apiKey?: string): Promise<ModelInfo[]> {
    const endpoint = buildPlaygroundEndpoint(baseUrl, '/models');
    if (!endpoint) throw new Error('CPA API address is not configured');

    const payload = await callJSON({ method: 'GET', url: endpoint, apiKey });
    return normalizeModelList(payload, { dedupe: true });
  },

  async chat(
    baseUrl: string,
    apiKey: string | undefined,
    model: string,
    messages: PlaygroundChatMessage[],
    managerBaseUrl?: string
  ): Promise<PlaygroundChatCompletion> {
    const endpoint = buildPlaygroundEndpoint(baseUrl, '/chat/completions');
    if (!endpoint) throw new Error('CPA API address is not configured');

    const payload = await callJSON({
      method: 'POST',
      url: endpoint,
      apiKey,
      managerBaseUrl,
      managerPath: '/chat/completions',
      data: {
        model: model.trim(),
        messages,
        stream: false,
      },
    });
    return normalizeChatCompletion(payload);
  },

  async generateImages(
    baseUrl: string,
    apiKey: string | undefined,
    options: PlaygroundImageGenerationOptions,
    managerBaseUrl?: string
  ): Promise<PlaygroundImageResult[]> {
    const endpoint = buildPlaygroundEndpoint(baseUrl, '/images/generations');
    if (!endpoint) throw new Error('CPA API address is not configured');

    const payload = await callJSON({
      method: 'POST',
      url: endpoint,
      apiKey,
      managerBaseUrl,
      managerPath: '/images/generations',
      data: {
        model: options.model.trim(),
        prompt: options.prompt.trim(),
        n: options.n ?? 1,
        ...(options.size ? { size: options.size } : {}),
        ...(options.quality ? { quality: options.quality } : {}),
      },
    });
    return normalizeImageResults(payload);
  },
};
