export type PlayerStatus = 'idle' | 'loading' | 'playing' | 'paused' | 'buffering' | 'error';
export type PlayerState = { status: PlayerStatus; source?: string; positionMs: number; message?: string };
export type PlayerAction =
  | { type: 'load'; source: string; resumeMs: number }
  | { type: 'play' } | { type: 'pause' } | { type: 'buffering' }
  | { type: 'seek' | 'progress'; positionMs: number }
  | { type: 'error'; message: string } | { type: 'stop' };
export const initialPlayerState: PlayerState = { status: 'idle', positionMs: 0 };
export function playerReducer(state: PlayerState, action: PlayerAction): PlayerState {
  switch (action.type) {
    case 'load': return { status: 'loading', source: action.source, positionMs: action.resumeMs };
    case 'play': return { ...state, status: 'playing', message: undefined };
    case 'pause': return { ...state, status: 'paused' };
    case 'buffering': return { ...state, status: 'buffering' };
    case 'seek': case 'progress': return { ...state, positionMs: Math.max(0, action.positionMs) };
    case 'error': return { ...state, status: 'error', message: action.message };
    case 'stop': return initialPlayerState;
  }
}
