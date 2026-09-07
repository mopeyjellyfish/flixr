import { describe, expect, it } from 'vitest';
import { ApiError, messageFor } from './api';

describe('API error messages', () => {
  it('explains credential hashing capacity and owner login rate limits', () => {
    expect(messageFor('credential_busy')).toBe('Flixr is busy securing credentials. Please wait and try again.');
    expect(messageFor('login_rate_limited')).toBe('Too many sign-in attempts. Please wait and try again.');
  });

  it('renders a correlated support ID with request failures', () => {
    expect(new ApiError('request_failed', 500, 'error-123').message).toContain('Support ID: error-123.');
  });
});
