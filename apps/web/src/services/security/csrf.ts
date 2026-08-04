let csrfToken = '';

export const getCsrfToken = (): string => csrfToken;

export const setCsrfToken = (token: unknown): void => {
  csrfToken = typeof token === 'string' ? token.trim() : '';
};

export const clearCsrfToken = (): void => {
  csrfToken = '';
};

export const csrfHeaders = (method: string): Record<string, string> => {
  if (!['POST', 'PUT', 'PATCH', 'DELETE'].includes(method.toUpperCase()) || !csrfToken) {
    return {};
  }
  return { 'X-CSRF-Token': csrfToken };
};
