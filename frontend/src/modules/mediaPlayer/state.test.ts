import { describe, expect, it } from 'vitest';
import { initialPlayerState, playerReducer } from './state';

describe('media player state', () => {
  it('handles load, play, pause, seek, progress, and error without a page component', () => {
    let state = initialPlayerState;
    state = playerReducer(state, { type: 'load', source: '/media', resumeMs: 1200 });
    expect(state).toMatchObject({ status: 'loading', source: '/media', positionMs: 1200 });
    state = playerReducer(state, { type: 'play' });
    state = playerReducer(state, { type: 'progress', positionMs: 1500 });
    state = playerReducer(state, { type: 'pause' });
    state = playerReducer(state, { type: 'seek', positionMs: 3000 });
    expect(state).toMatchObject({ status: 'paused', positionMs: 3000 });
    state = playerReducer(state, { type: 'error', message: 'FFmpeg is unavailable.' });
    expect(state).toMatchObject({ status: 'error', message: 'FFmpeg is unavailable.' });
  });
});
