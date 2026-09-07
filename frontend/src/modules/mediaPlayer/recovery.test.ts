import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../core/api';
import { PlaybackRecovery, isRetryablePlaybackFailure } from './recovery';

describe('playback recovery policy', () => {
  it('retries interrupted requests, temporary server failures, and expired playback leases', () => {
    expect(isRetryablePlaybackFailure(new ApiError('request_failed', 0))).toBe(true);
    expect(isRetryablePlaybackFailure(new ApiError('request_failed', 503))).toBe(true);
    expect(isRetryablePlaybackFailure(new ApiError('playback_preparing', 503))).toBe(true);
    expect(isRetryablePlaybackFailure(new ApiError('playback_session_invalid', 403))).toBe(true);
  });

  it('does not retry revoked authorization or unsupported media', () => {
    expect(isRetryablePlaybackFailure(new ApiError('profile_required', 403))).toBe(false);
    expect(isRetryablePlaybackFailure(new ApiError('owner_required', 403))).toBe(false);
    expect(isRetryablePlaybackFailure(new ApiError('playback_unsupported', 422))).toBe(false);
    expect(isRetryablePlaybackFailure(new ApiError('ffmpeg_unavailable', 503))).toBe(false);
  });
});

describe('PlaybackRecovery', () => {
  it('caps attempts and lets an online event accelerate a scheduled retry', async () => {
    vi.useFakeTimers();
    const recovery = new PlaybackRecovery([1_000, 2_000]);
    const operation = vi.fn()
      .mockRejectedValueOnce(new ApiError('request_failed', 503))
      .mockRejectedValueOnce(new ApiError('request_failed', 503))
      .mockResolvedValue('recovered');

    const result = recovery.run(operation, () => undefined);
    await vi.waitFor(() => expect(operation).toHaveBeenCalledTimes(1));
    window.dispatchEvent(new Event('online'));
    await vi.waitFor(() => expect(operation).toHaveBeenCalledTimes(2));
    await vi.advanceTimersByTimeAsync(2_000);

    await expect(result).resolves.toBe('recovered');
    expect(operation).toHaveBeenCalledTimes(3);
    vi.useRealTimers();
  });

  it('disposes a create response that arrives after cancellation', async () => {
    let resolveCreate: ((session: { session_id: string }) => void) | undefined;
    const recovery = new PlaybackRecovery([1]);
    const dispose = vi.fn();
    const result = recovery.run(
      () => new Promise<{ session_id: string }>((resolve) => { resolveCreate = resolve; }),
      dispose,
    );
    await vi.waitFor(() => expect(resolveCreate).toBeTypeOf('function'));

    recovery.cancel();
    resolveCreate!({ session_id: 'abandoned-session' });

    await expect(result).resolves.toBeUndefined();
    expect(dispose).toHaveBeenCalledWith({ session_id: 'abandoned-session' });
  });

  it('returns the final classified failure after the bounded attempts', async () => {
    vi.useFakeTimers();
    const recovery = new PlaybackRecovery([1, 1]);
    const failure = new ApiError('request_failed', 503);
    const operation = vi.fn().mockRejectedValue(failure);

    const result = recovery.run(operation, () => undefined);
    const rejected = expect(result).rejects.toBe(failure);
    await vi.runAllTimersAsync();

    await rejected;
    expect(operation).toHaveBeenCalledTimes(3);
    vi.useRealTimers();
  });
});
