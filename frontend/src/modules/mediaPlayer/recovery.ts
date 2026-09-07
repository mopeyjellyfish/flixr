import { ApiError } from '../../core/api';

export class PlaybackNetworkError extends Error {
  constructor() {
    super('The local playback connection was interrupted.');
    this.name = 'PlaybackNetworkError';
  }
}

export function isRetryablePlaybackFailure(error: unknown): boolean {
  if (error instanceof PlaybackNetworkError) return true;
  if (!(error instanceof ApiError)) return false;
  if (error.code === 'playback_session_invalid' || error.code === 'playback_preparing') return true;
  if (error.code === 'request_failed') return error.status === 0 || error.status >= 500;
  return false;
}

export class PlaybackRecovery {
  private generation = 0;
  private wake: (() => void) | undefined;

  constructor(private readonly retryDelays = [750, 1_500, 3_000]) {}

  cancel(): void {
    this.generation += 1;
    this.wake?.();
    this.wake = undefined;
  }

  async run<T>(operation: () => Promise<T>, dispose: (value: T) => void): Promise<T | undefined> {
    const generation = ++this.generation;
    for (let attempt = 0; attempt <= this.retryDelays.length; attempt += 1) {
      if (attempt > 0 && !(await this.wait(this.retryDelays[attempt - 1], generation))) return undefined;
      try {
        const value = await operation();
        if (generation !== this.generation) {
          dispose(value);
          return undefined;
        }
        return value;
      } catch (error: unknown) {
        if (generation !== this.generation) return undefined;
        if (!isRetryablePlaybackFailure(error) || attempt === this.retryDelays.length) throw error;
      }
    }
    return undefined;
  }

  private wait(delay: number, generation: number): Promise<boolean> {
    return new Promise((resolve) => {
      let settled = false;
      const finish = () => {
        if (settled) return;
        settled = true;
        window.clearTimeout(timer);
        window.removeEventListener('online', finish);
        if (this.wake === finish) this.wake = undefined;
        resolve(generation === this.generation);
      };
      const timer = window.setTimeout(finish, delay);
      this.wake = finish;
      window.addEventListener('online', finish, { once: true });
    });
  }
}
