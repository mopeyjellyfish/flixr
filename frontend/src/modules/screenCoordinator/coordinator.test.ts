import { describe, expect, it, vi } from 'vitest';
import type { ScreenCommand, ScreenPresence } from '../../core/screens';
import { ScreenCoordinator } from './coordinator';

class Socket {
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  sent: string[] = [];
  readyState: number = WebSocket.CONNECTING;
  send(value: string) { this.sent.push(value); }
  open() { this.readyState = WebSocket.OPEN; this.onopen?.(); }
  close() { this.readyState = WebSocket.CLOSED; this.onclose?.(); }
}

it('coordinates receiver commands and visible disconnect state', async () => {
  const socket = new Socket();
  const command = vi.fn<(command: ScreenCommand) => void>();
  const screen: ScreenPresence = { id: 'screen-1', name: 'Living room', state: 'available' };
  const coordinator = new ScreenCoordinator({
    list: async () => [screen],
    advertise: async () => ({ screen, ticket: 'ticket' }),
    authorize: async () => ({ token: 'token' }),
    socket: () => socket as unknown as WebSocket,
  });
  coordinator.onCommand(command);
  await coordinator.receive('Living room');
  socket.open();
  expect(coordinator.current().status).toBe('receiving');
  await coordinator.refresh();
  expect(coordinator.current().status).toBe('receiving');
  socket.onmessage?.({ data: JSON.stringify({ version: 1, type: 'play', catalog_id: 'film-1', position_ms: 20 }) });
  expect(command).toHaveBeenCalledWith({ version: 1, type: 'play', catalog_id: 'film-1', position_ms: 20 });
  socket.onclose?.();
  expect(coordinator.current()).toEqual({ status: 'disconnected', screens: [screen], message: 'Screen connection lost. Reconnect this screen to continue.' });
});

describe('controller', () => {
  it('connects only after the socket opens so the first command is delivered', async () => {
    const socket = new Socket();
    const screen: ScreenPresence = { id: 'screen-1', name: 'Living room', state: 'available' };
    const coordinator = new ScreenCoordinator({ list: async () => [screen], advertise: vi.fn(), authorize: async () => ({ token: 'token' }), socket: () => socket as unknown as WebSocket });
    const connecting = coordinator.connect(screen);
    await Promise.resolve();
    expect(coordinator.current().status).toBe('connecting');
    socket.open();
    await connecting;
    coordinator.command({ version: 1, type: 'play', catalog_id: 'film-1', position_ms: 4200 });
    expect(socket.sent).toEqual([JSON.stringify({ version: 1, type: 'play', catalog_id: 'film-1', position_ms: 4200 })]);
  });

  it('ignores stale async results and close handlers after replacement', async () => {
    const createdSockets: Socket[] = [];
    const screens: ScreenPresence[] = [
      { id: 'old', name: 'Old', state: 'available' },
      { id: 'new', name: 'New', state: 'available' },
    ];
    const authorizations: Array<(value: { token: string }) => void> = [];
    const coordinator = new ScreenCoordinator({
      list: async () => screens,
      advertise: vi.fn(),
      authorize: () => new Promise((resolve) => authorizations.push(resolve)),
      socket: () => { const socket = new Socket(); createdSockets.push(socket); return socket as unknown as WebSocket; },
    });
    const oldConnect = coordinator.connect(screens[0]);
    const newConnect = coordinator.connect(screens[1]);
    authorizations[1]({ token: 'new' });
    await Promise.resolve();
    const newSocket = createdSockets[0];
    newSocket.open();
    await newConnect;
    authorizations[0]({ token: 'old' });
    await oldConnect;
    expect(coordinator.current()).toEqual({ status: 'connected', screen: screens[1], screens: [] });
    coordinator.disconnect();
    newSocket.onclose?.();
    expect(coordinator.current()).toEqual({ status: 'idle', screens: [] });
    expect(await oldConnect).toBe(false);
  });
});
