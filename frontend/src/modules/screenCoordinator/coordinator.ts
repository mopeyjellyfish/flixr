import type { ScreenCommand, ScreenCoordinatorState, ScreenPresence } from '../../core/screens';

type Dependencies = {
  list: () => Promise<ScreenPresence[]>;
  advertise: (name: string) => Promise<{ screen: ScreenPresence; ticket: string }>;
  authorize: (id: string) => Promise<{ token: string }>;
  socket: (path: string) => WebSocket;
};

type Listener = (state: ScreenCoordinatorState) => void;

export class ScreenCoordinator {
  private discovered: ScreenPresence[] = [];
  private state: ScreenCoordinatorState & { screens: ScreenPresence[] } = { status: 'idle', screens: [] };
  private listeners = new Set<Listener>();
  private commandListeners = new Set<(command: ScreenCommand) => void>();
  private connection?: WebSocket;
  private operation = 0;
  private refreshOperation = 0;
  private pendingConnect?: () => void;

  constructor(private readonly dependencies: Dependencies) {}
  current() { return this.state; }
  subscribe(listener: Listener) { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }
  onCommand(listener: (command: ScreenCommand) => void) { this.commandListeners.add(listener); return () => { this.commandListeners.delete(listener); }; }
  private update(state: ScreenCoordinatorState) { this.state = { ...state, screens: this.discovered }; for (const listener of this.listeners) listener(this.state); }
  async refresh() {
    const operation = ++this.refreshOperation;
    const connectionOperation = this.operation;
    try {
      const screens = await this.dependencies.list();
      if (operation === this.refreshOperation && connectionOperation === this.operation) {
        this.discovered = screens;
        this.update(this.connection ? this.state : { status: 'ready', screens });
      }
    } catch {
      if (operation === this.refreshOperation && connectionOperation === this.operation && !this.connection) this.update({ status: 'error', message: 'Screens are unavailable.' });
    }
  }
  async receive(name: string) {
    this.disconnect();
    const operation = ++this.operation;
    try {
      const advertised = await this.dependencies.advertise(name);
      if (operation !== this.operation) return;
      const socket = this.dependencies.socket(`/api/v1/screens/receiver?ticket=${encodeURIComponent(advertised.ticket)}`);
      this.replace(socket);
      socket.onopen = () => { if (this.isCurrent(socket, operation)) this.update({ status: 'receiving', screen: advertised.screen }); };
      socket.onmessage = (event) => {
        if (!this.isCurrent(socket, operation)) return;
        try { const value: unknown = JSON.parse(String(event.data)); if (isCommand(value)) for (const listener of this.commandListeners) listener(value); }
        catch { this.update({ status: 'error', message: 'The screen sent an unsupported message.' }); }
      };
      socket.onclose = () => { if (this.isCurrent(socket, operation)) this.update({ status: 'disconnected', message: 'Screen connection lost. Reconnect this screen to continue.' }); };
    } catch {
      if (operation === this.operation) this.update({ status: 'error', message: 'This device could not become a screen.' });
    }
  }
  async connect(screen: ScreenPresence) {
    this.disconnect();
    const operation = ++this.operation;
    this.update({ status: 'connecting', screen });
    try {
      const session = await this.dependencies.authorize(screen.id);
      if (operation !== this.operation) return false;
      const socket = this.dependencies.socket(`/api/v1/screens/control?token=${encodeURIComponent(session.token)}`);
      this.replace(socket);
      return await new Promise<boolean>((resolve) => {
        let settled = false;
        const timer = window.setTimeout(() => { socket.close(); finish(false); }, 10_000);
        const finish = (connected: boolean) => {
          if (settled) return;
          settled = true;
          window.clearTimeout(timer);
          this.pendingConnect = undefined;
          resolve(connected);
        };
        this.pendingConnect = () => finish(false);
        socket.onopen = () => {
          if (!this.isCurrent(socket, operation)) return finish(false);
          this.update({ status: 'connected', screen });
          finish(true);
        };
        socket.onclose = () => {
          if (!this.isCurrent(socket, operation)) return finish(false);
          this.connection = undefined;
          this.update({ status: 'disconnected', message: 'Remote screen disconnected. Playback remains available here.' });
          finish(false);
        };
      });
    } catch {
      if (operation === this.operation && this.state.status === 'connecting') this.update({ status: 'error', message: 'Remote screen connection was rejected.' });
      return false;
    }
  }
  command(command: ScreenCommand) {
    const socket = this.connection;
    if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(command));
    if (command.type === 'handoff' && socket) this.disconnect();
  }
  disconnect() {
    ++this.operation;
    ++this.refreshOperation;
    this.discovered = [];
    this.pendingConnect?.();
    const socket = this.connection;
    this.connection = undefined;
    if (socket) { socket.onclose = null; socket.onmessage = null; socket.close(); }
    this.update({ status: 'idle' });
  }
  private replace(socket: WebSocket) {
    this.pendingConnect?.();
    const previous = this.connection;
    this.connection = socket;
    if (previous) { previous.onclose = null; previous.onmessage = null; previous.close(); }
  }
  private isCurrent(socket: WebSocket, operation: number) { return this.connection === socket && this.operation === operation; }
}

function isCommand(value: unknown): value is ScreenCommand {
  if (!value || typeof value !== 'object') return false;
  const message = value as Record<string, unknown>;
  if (message.version !== 1) return false;
  if (message.type === 'pause' || message.type === 'handoff') return true;
  if (!Number.isSafeInteger(message.position_ms) || typeof message.position_ms !== 'number' || message.position_ms < 0) return false;
  return message.type === 'seek' || (message.type === 'play' && typeof message.catalog_id === 'string' && message.catalog_id.length > 0);
}
