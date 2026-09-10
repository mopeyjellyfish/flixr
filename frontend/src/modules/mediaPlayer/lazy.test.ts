import { expect, it, vi } from 'vitest';
import { retryableLazy } from './lazy';

it('shares an in-flight load and retries after a rejected lazy asset', async () => {
  const failure = Promise.reject(new Error('asset unavailable'));
  const success = Promise.resolve({ default: 'player' });
  const load = vi.fn().mockReturnValueOnce(failure).mockReturnValueOnce(success);
  const lazy = retryableLazy(load);

  expect(lazy()).toBe(failure);
  expect(lazy()).toBe(failure);
  await expect(failure).rejects.toThrow('asset unavailable');
  await Promise.resolve();
  await expect(lazy()).resolves.toEqual({ default: 'player' });
  expect(load).toHaveBeenCalledTimes(2);
});
