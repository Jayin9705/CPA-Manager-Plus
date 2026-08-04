import { beforeEach, describe, expect, it, vi } from 'vitest';

const axiosMocks = vi.hoisted(() => {
  const requestInterceptor = vi.fn();
  const responseInterceptor = vi.fn();
  const instance = {
    defaults: { timeout: 0 },
    interceptors: {
      request: { use: requestInterceptor },
      response: { use: responseInterceptor },
    },
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
    request: vi.fn(),
  };
  return {
    instance,
    create: vi.fn(() => instance),
    isAxiosError: vi.fn(() => false),
  };
});

vi.mock('axios', () => ({
  default: {
    create: axiosMocks.create,
    isAxiosError: axiosMocks.isAxiosError,
  },
}));

import { usageServiceApi } from './usageService';

describe('usageServiceApi Manager session authentication', () => {
  beforeEach(() => {
    axiosMocks.instance.get.mockReset();
    axiosMocks.instance.post.mockReset();
    axiosMocks.instance.put.mockReset();
    axiosMocks.instance.patch.mockReset();
    axiosMocks.instance.delete.mockReset();
    axiosMocks.instance.request.mockReset();
    axiosMocks.isAxiosError.mockClear();
  });

  it('uses a credentialed client and sends email/password only to the login endpoint', async () => {
    const session = {
      authenticated: true as const,
      user: { id: 7, email: 'admin@example.com' },
      expiresAt: 1_800_000_000_000,
    };
    axiosMocks.instance.post.mockResolvedValueOnce({ data: session });

    await expect(
      usageServiceApi.login('http://manager.local:18317', {
        email: 'admin@example.com',
        password: 'secret-password',
        remember: true,
      })
    ).resolves.toEqual(session);

    expect(axiosMocks.create).toHaveBeenCalledWith({ withCredentials: true });
    expect(axiosMocks.instance.post).toHaveBeenCalledWith(
      'http://manager.local:18317/usage-service/auth/login',
      {
        email: 'admin@example.com',
        password: 'secret-password',
        remember: true,
      },
      expect.any(Object)
    );
  });

  it('restores and revokes the cookie session without a Bearer header', async () => {
    const session = {
      authenticated: true as const,
      user: { id: 7, email: 'admin@example.com' },
      expiresAt: 1_800_000_000_000,
    };
    axiosMocks.instance.get.mockResolvedValueOnce({ data: session });
    axiosMocks.instance.post.mockResolvedValueOnce({ status: 204 });

    await usageServiceApi.getSession('http://manager.local:18317');
    await usageServiceApi.logout('http://manager.local:18317');

    expect(axiosMocks.instance.get).toHaveBeenCalledWith(
      'http://manager.local:18317/usage-service/auth/session',
      expect.not.objectContaining({ headers: expect.anything() })
    );
    expect(axiosMocks.instance.post).toHaveBeenCalledWith(
      'http://manager.local:18317/usage-service/auth/logout',
      undefined,
      expect.not.objectContaining({ headers: expect.anything() })
    );
  });
});
