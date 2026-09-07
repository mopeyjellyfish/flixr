import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Player } from './Player';

const hls = vi.hoisted(() => ({ error: undefined as undefined | ((event: unknown, data: { fatal: boolean }) => void), attached: 0, destroyed: 0, imported: 0, waitForImport: false, releaseImport: undefined as undefined | (() => void) }));
vi.mock('hls.js', async () => {
  if (hls.waitForImport) await new Promise<void>((resolve) => { hls.releaseImport = resolve; });
  hls.imported += 1;
  class FakeHls {
    static Events = { ERROR: 'error' };
    static isSupported() { return true; }
    on(_event: string, handler: (event: unknown, data: { fatal: boolean }) => void) { hls.error = handler; }
    loadSource() { /* observable through attachment */ }
    attachMedia() { hls.attached += 1; }
    destroy() { hls.destroyed += 1; }
  }
  return { default: FakeHls };
});

beforeEach(() => {
  hls.error = undefined;
  hls.attached = 0;
  hls.destroyed = 0;
  hls.imported = 0;
  hls.waitForImport = false;
  hls.releaseImport = undefined;
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('');
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it('shows an actionable error when native media playback fails', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/missing.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 })));
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/missing.mp4'));
  fireEvent.error(video);
  expect(await screen.findByRole('alert')).toHaveTextContent(/check that the file is still available/i);
  expect(screen.getByRole('button', { name: /return to your library/i })).toBeVisible();
});

it('sends a final heartbeat before stopping an explicit player exit', async () => {
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: 'data:video/mp4;base64,', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
  });
  const exit = vi.fn();
  const { unmount } = render(<Player catalogID="film-1" onExit={exit} />);
  await screen.findByRole('button', { name: /back to library/i });
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  await waitFor(() => expect(exit).toHaveBeenCalled());
  unmount();
  const heartbeatIndex = calls.findIndex((path) => path.endsWith('/heartbeat'));
  const stopIndex = calls.findIndex((path) => path.endsWith('/stop'));
  expect(heartbeatIndex).toBeGreaterThanOrEqual(0);
  expect(stopIndex).toBeGreaterThan(heartbeatIndex);
  expect(calls.filter((path) => path.endsWith('/stop'))).toHaveLength(1);
});

it('beacons progress on pagehide for the active plan', async () => {
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: vi.fn(() => true) });
  const beacon = vi.spyOn(navigator, 'sendBeacon');
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: 'data:video/mp4;base64,', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 })));
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await screen.findByRole('button', { name: /back to library/i });
  await waitFor(() => fireEvent(window, new Event('pagehide')));
  expect(beacon).toHaveBeenCalledWith('/heartbeat', expect.any(Blob));
});

it('rejects an HLS import that resolves after the catalog destination changes', async () => {
  hls.waitForImport = true;
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (!path.endsWith('/playback/plans')) return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
    const catalogID = JSON.parse(String(init?.body)).catalog_id;
    const kind = catalogID === 'film-1' ? 'transcode' : 'direct';
    return new Response(JSON.stringify({ plan: { kind }, session_id: catalogID, media_url: kind === 'direct' ? 'data:video/mp4;base64,' : '/stale/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
  });
  const { rerender } = render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.releaseImport).toBeTypeOf('function'));
  rerender(<Player catalogID="film-2" onExit={() => undefined} />);
  await waitFor(() => expect(fetcher.mock.calls.some(([, init]) => String(init?.body).includes('film-2'))).toBe(true));
  await act(async () => { hls.releaseImport?.(); });
  await waitFor(() => expect(hls.imported).toBe(1));
  expect(hls.attached).toBe(0);
});
it('sends the compatibility seek position and surfaces a fatal HLS error', async () => {
  const requests: Array<{ path: string; body?: string }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    requests.push({ path, body: init?.body as string | undefined });
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 2_000, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 7_000, stream_offset_ms: 2_000, expires_at: 9999999999 }));
    return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  video.currentTime = 5;
  fireEvent.seeked(video);
  await waitFor(() => expect(requests.some((request) => request.path.endsWith('/seek') && JSON.parse(request.body ?? '{}').position_ms === 7_000)).toBe(true));
  hls.error?.({}, { fatal: true });
  expect(await screen.findByRole('alert')).toHaveTextContent(/compatibility stream stopped unexpectedly/i);
});

it('still stops the server session when saving final progress fails', async () => {
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) throw new TypeError('Temporary connection failure');
    return new Response(JSON.stringify({ stopped: true }));
  });
  const exit = vi.fn();
  render(<Player catalogID="film-1" onExit={exit} />);
  await waitFor(() => expect(document.querySelector('video')).toHaveAttribute('src', '/film.mp4'));
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  await waitFor(() => expect(exit).toHaveBeenCalledOnce());
  expect(calls.filter(path => path.endsWith('/stop'))).toHaveLength(1);
});

it('ignores late media heartbeats while the final stop request is pending', async () => {
  const calls: string[] = [];
  let finishStop: (() => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/stop')) await new Promise<void>((resolve) => { finishStop = resolve; });
    return new Response(JSON.stringify({ stopped: true }));
  });
  const exit = vi.fn();
  render(<Player catalogID="film-1" onExit={exit} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/film.mp4'));
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  await waitFor(() => expect(finishStop).toBeTypeOf('function'));
  // The server may already have revoked the session while its stop response is in flight.
  fireEvent.ended(video);
  fireEvent.pause(video);
  await act(async () => { finishStop!(); });
  expect(exit).toHaveBeenCalledOnce();
  expect(calls.filter(path => path.endsWith('/heartbeat'))).toHaveLength(1);
  expect(calls.filter(path => path.endsWith('/stop'))).toHaveLength(1);
});

it('orders seek, ended, pagehide and final observations without the device clock', async () => {
  const observations: number[] = [];
  let beaconBody: Blob | undefined;
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: (_url: string, body: Blob) => { beaconBody = body; return true; } });
  const plan = { plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.endsWith('/heartbeat') || path.endsWith('/seek')) observations.push(JSON.parse(String(init?.body)).observation);
    return new Response(JSON.stringify(plan));
  });
  const { unmount } = render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video')!;
  vi.spyOn(Date, 'now').mockReturnValue(9999999999999);
  fireEvent.seeked(video);
  await waitFor(() => expect(observations).toHaveLength(1));
  vi.spyOn(Date, 'now').mockReturnValue(1);
  fireEvent.ended(video);
  await waitFor(() => expect(observations).toHaveLength(2));
  fireEvent(window, new Event('pagehide'));
  const beaconText = await new Promise<string>((resolve) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.readAsText(beaconBody!); });
  unmount();
  expect([...observations.slice(0, 2), JSON.parse(beaconText).observation, observations[2]]).toEqual([1, 2, 3, 4]);
});
