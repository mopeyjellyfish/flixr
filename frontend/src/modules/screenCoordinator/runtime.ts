import { api } from '../../api/client';
import { ScreenCoordinator } from './coordinator';

function websocket(path: string) {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return new WebSocket(`${protocol}//${window.location.host}${path}`);
}

export const screenCoordinator = new ScreenCoordinator({
  list: async () => (await api.screens()).screens,
  advertise: api.advertiseScreen,
  authorize: api.authorizeScreen,
  socket: websocket,
});
