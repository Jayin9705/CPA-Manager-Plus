import { describe, expect, it } from 'vitest';
import {
  buildManagerPlaygroundEndpoint,
  buildPlaygroundEndpoint,
  normalizeChatCompletion,
  normalizeImageResults,
} from './playground';

describe('playground API helpers', () => {
  it('builds CPA v1 endpoints from root and v1 base URLs', () => {
    expect(buildPlaygroundEndpoint('http://cpa.local:8317', '/models')).toBe(
      'http://cpa.local:8317/v1/models'
    );
    expect(buildPlaygroundEndpoint('http://cpa.local:8317/v1', 'chat/completions')).toBe(
      'http://cpa.local:8317/v1/chat/completions'
    );
  });

  it('builds Manager playground proxy endpoints', () => {
    expect(
      buildManagerPlaygroundEndpoint('http://manager.local:18317', '/images/generations')
    ).toBe('http://manager.local:18317/v0/management/playground/images/generations');
  });

  it('extracts text from standard and multipart chat content', () => {
    expect(
      normalizeChatCompletion({
        choices: [{ message: { content: [{ type: 'text', text: 'hello ' }, { text: 'world' }] } }],
      }).text
    ).toBe('hello world');
    expect(normalizeChatCompletion({ choices: [{ text: 'fallback' }] }).text).toBe('fallback');
  });

  it('normalizes URL and base64 image results', () => {
    expect(
      normalizeImageResults({
        data: [
          { url: 'https://cdn.example/image.png' },
          { b64_json: 'abc', mime_type: 'image/webp', revised_prompt: 'revised' },
          { revised_prompt: 'missing image data' },
        ],
      })
    ).toEqual([
      {
        url: 'https://cdn.example/image.png',
        b64Json: undefined,
        mimeType: undefined,
        revisedPrompt: undefined,
      },
      { url: undefined, b64Json: 'abc', mimeType: 'image/webp', revisedPrompt: 'revised' },
    ]);
  });
});
