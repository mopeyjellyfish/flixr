export type ScreenPresence = {
  id: string;
  name: string;
  state: 'available' | 'playing' | 'paused';
  catalog_id?: string;
  position_ms?: number;
};

export type ScreenCommand =
  | { version: 1; type: 'play'; catalog_id: string; position_ms: number }
  | { version: 1; type: 'pause' }
  | { version: 1; type: 'seek'; position_ms: number }
  | { version: 1; type: 'handoff' };

export type ScreenCoordinatorState =
  | { status: 'idle' }
  | { status: 'ready'; screens: ScreenPresence[] }
  | { status: 'connecting'; screen: ScreenPresence }
  | { status: 'connected'; screen: ScreenPresence }
  | { status: 'receiving'; screen: ScreenPresence }
  | { status: 'disconnected'; message: string }
  | { status: 'error'; message: string };
