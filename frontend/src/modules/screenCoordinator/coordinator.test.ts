import { describe, expect, it, vi } from 'vitest';
import type { ScreenCommand, ScreenPresence } from '../../core/screens';
import { ScreenCoordinator } from './coordinator';

class Socket {
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  sent: string[] = [];
  send(value: string) { this.sent.push(value); }
  close() { this.onclose?.(); }
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
  socket.onopen?.();
  expect(coordinator.current().status).toBe('receiving');
  socket.onmessage?.({ data: JSON.stringify({ version: 1, type: 'play', catalog_id: 'film-1', position_ms: 20 }) });
  expect(command).toHaveBeenCalledWith({ version: 1, type: 'play', catalog_id: 'film-1', position_ms: 20 });
  socket.onclose?.();
  expect(coordinator.current()).toEqual({ status: 'disconnected', message: 'Screen connection lost. Reconnect this screen to continue.' });
});

describe('controller', () => {
  it('connects and sends versioned commands', async () => {
    const socket = new Socket();
    const screen: ScreenPresence = { id: 'screen-1', name: 'Living room', state: 'available' };
    const coordinator = new ScreenCoordinator({ list: async () => [screen], advertise: vi.fn(), authorize: async () => ({ token: 'token' }), socket: () => socket as unknown as WebSocket });
    await coordinator.refresh();
    await coordinator.connect(screen);
    socket.onopen?.();
    coordinator.command({ version: 1, type: 'pause' });
    expect(socket.sent).toEqual([JSON.stringify({ version: 1, type: 'pause' })]);
  });
});
