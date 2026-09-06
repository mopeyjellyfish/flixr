import type { ScreenCommand, ScreenCoordinatorState, ScreenPresence } from '../../core/screens';

type Dependencies = {
  list: () => Promise<ScreenPresence[]>;
  advertise: (name: string) => Promise<{ screen: ScreenPresence; ticket: string }>;
  authorize: (id: string) => Promise<{ token: string }>;
  socket: (path: string) => WebSocket;
};

type Listener = (state: ScreenCoordinatorState) => void;

export class ScreenCoordinator {
  private state: ScreenCoordinatorState = { status: 'idle' };
  private listeners = new Set<Listener>();
  private commandListeners = new Set<(command: ScreenCommand) => void>();
  private connection?: WebSocket;

  constructor(private readonly dependencies: Dependencies) {}
  current() { return this.state; }
  subscribe(listener: Listener) { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }
  onCommand(listener: (command: ScreenCommand) => void) { this.commandListeners.add(listener); return () => { this.commandListeners.delete(listener); }; }
  private update(state: ScreenCoordinatorState) { this.state = state; for (const listener of this.listeners) listener(state); }
  async refresh() { try { this.update({ status: 'ready', screens: await this.dependencies.list() }); } catch { this.update({ status: 'error', message: 'Screens are unavailable.' }); } }
  async receive(name: string) {
    try {
      const advertised = await this.dependencies.advertise(name);
      const socket = this.dependencies.socket(`/api/v1/screens/receiver?ticket=${encodeURIComponent(advertised.ticket)}`);
      this.replace(socket);
      socket.onopen = () => this.update({ status: 'receiving', screen: advertised.screen });
      socket.onmessage = (event) => { try { const value: unknown = JSON.parse(String(event.data)); if (isCommand(value)) for (const listener of this.commandListeners) listener(value); } catch { this.update({ status: 'error', message: 'The screen sent an unsupported message.' }); } };
      socket.onclose = () => this.update({ status: 'disconnected', message: 'Screen connection lost. Reconnect this screen to continue.' });
    } catch { this.update({ status: 'error', message: 'This device could not become a screen.' }); }
  }
  async connect(screen: ScreenPresence) {
    this.update({ status: 'connecting', screen });
    try {
      const session = await this.dependencies.authorize(screen.id);
      const socket = this.dependencies.socket(`/api/v1/screens/control?token=${encodeURIComponent(session.token)}`);
      this.replace(socket);
      socket.onopen = () => this.update({ status: 'connected', screen });
      socket.onclose = () => this.update({ status: 'disconnected', message: 'Remote screen disconnected. Playback remains available here.' });
    } catch { this.update({ status: 'error', message: 'Remote screen connection was rejected.' }); }
  }
  command(command: ScreenCommand) {
    const socket = this.connection;
    if (socket?.readyState === WebSocket.OPEN || socket?.readyState === undefined) socket?.send(JSON.stringify(command));
    if (command.type === 'handoff' && socket) {
      socket.onclose = null;
      socket.close();
      this.connection = undefined;
      this.update({ status: 'idle' });
    }
  }
  disconnect() { this.connection?.close(); this.connection = undefined; this.update({ status: 'idle' }); }
  private replace(socket: WebSocket) { this.connection?.close(); this.connection = socket; }
}

function isCommand(value: unknown): value is ScreenCommand {
  if (!value || typeof value !== 'object') return false;
  const message = value as Record<string, unknown>;
  return message.version === 1 && (message.type === 'play' || message.type === 'pause' || message.type === 'seek' || message.type === 'handoff');
}
