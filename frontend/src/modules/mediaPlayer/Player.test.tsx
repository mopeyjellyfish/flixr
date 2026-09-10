import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Player } from './Player';
import { screenCoordinator } from '../screenCoordinator/runtime';

const hls = vi.hoisted(() => ({ error: undefined as undefined | ((event: unknown, data: unknown) => void), frag: undefined as undefined | ((event: unknown, data: unknown) => void), manifest: undefined as undefined | ((event: unknown, data: unknown) => void), attached: 0, destroyed: 0, imported: 0, removed: 0, released: 0, ended: 0, bufferedTransfer: false, mediaSourceTransfer: false, configs: [] as Array<Record<string, unknown>>, supported: true, waitForImport: false, releaseImport: undefined as undefined | (() => void) }));
vi.mock('hls.js', async () => {
  if (hls.waitForImport) await new Promise<void>((resolve) => { hls.releaseImport = resolve; });
  hls.imported += 1;
  class FakeHls {
    static Events = { ERROR: 'error', FRAG_LOADED: 'fragLoaded', MANIFEST_PARSED: 'manifestParsed' };
    static ErrorTypes = { NETWORK_ERROR: 'networkError' };
    static isSupported() { return hls.supported; }
    private media: HTMLMediaElement | null = null;
    constructor(config: Record<string, unknown>) { hls.configs.push(config); }
    on(event: string, handler: (event: unknown, data: unknown) => void) {
      if (event === 'error') hls.error = handler;
      if (event === 'fragLoaded') hls.frag = handler;
      if (event === 'manifestParsed') hls.manifest = handler;
    }
    loadSource() { /* observable through attachment */ }
    stopLoad() { /* preserves the attached media while recovery prepares */ }
    attachMedia(data: HTMLMediaElement | { media: HTMLMediaElement }) { this.media = 'media' in data ? data.media : data; hls.attached += 1; }
    transferMedia() {
      const media = this.media;
      this.media = null;
      let buffered = hls.bufferedTransfer;
      const buffer = { get updating() { return false; }, buffered: { get length() { return buffered ? 1 : 0; }, start: () => 0, end: () => 20 }, remove: () => { hls.removed += 1; buffered = false; } };
      const sourceBuffers = [buffer];
      const mediaSource = hls.mediaSourceTransfer ? {
        readyState: 'open', sourceBuffers,
        removeSourceBuffer: (candidate: typeof buffer) => { sourceBuffers.splice(sourceBuffers.indexOf(candidate), 1); hls.released += 1; },
        endOfStream: () => { hls.ended += 1; },
      } : null;
      return media ? { media, mediaSource, tracks: hls.bufferedTransfer ? { video: { buffer } } : {} } : null;
    }
    destroy() { hls.destroyed += 1; if (this.media) { this.media.removeAttribute('src'); this.media.load(); this.media = null; } }
  }
  return { default: FakeHls };
});

beforeEach(() => {
  hls.error = undefined;
  hls.frag = undefined;
  hls.manifest = undefined;
  hls.attached = 0;
  hls.destroyed = 0;
  hls.imported = 0;
  hls.removed = 0;
  hls.released = 0;
  hls.ended = 0;
  hls.bufferedTransfer = false;
  hls.mediaSourceTransfer = false;
  hls.configs = [];
  hls.supported = true;
  hls.waitForImport = false;
  hls.releaseImport = undefined;
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('');
});
afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: undefined });
});

const assessedItem = {
  id: 'film-1', title: 'Blue Horizon', kind: 'film', local_only: true,
  container: 'mov,mp4,m4a,3gp,3g2,mj2', video_codec: 'h264', video_profile: 'High', video_level: 12,
  width: 320, height: 180, bitrate: 5968, frame_rate_milli: 24000, bit_depth: 8,
  audio: [{ codec: 'aac', profile: 'LC', channels: 2, sample_rate: 48000, bitrate: 2323 }],
};

it('posts the exact per-title source and compatibility evidence', async () => {
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
  Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo: vi.fn().mockResolvedValue({ supported: true }) } });
  const requests: Array<{ path: string; body?: string }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    requests.push({ path, body: init?.body as string | undefined });
    if (path.endsWith('/catalog/items/film-1')) return new Response(JSON.stringify(assessedItem));
    return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);

  await waitFor(() => expect(requests.some(({ path }) => path.endsWith('/playback/plans'))).toBe(true));
  const posted = JSON.parse(requests.find(({ path }) => path.endsWith('/playback/plans'))!.body!);
  expect(posted).toEqual({
    catalog_id: 'film-1',
    continue_watching_intent: 'user',
    quality: { mode: 'auto', max_video_bitrate: 2_500_000, max_width: 1280, max_height: 720 },
    capabilities: expect.objectContaining({
      supports_direct: true,
      supports_remux: true,
      supports_transcode: true,
      max_width: 320,
      max_height: 180,
      max_frame_rate_milli: 24000,
      max_bit_depth: 8,
      max_audio_channels: 2,
    }),
  });
});

it('keeps an explicit source version through initial planning and lease recovery', async () => {
  const planBodies: Array<Record<string, unknown>> = [];
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      planBodies.push(JSON.parse(String(init?.body)));
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'direct' }, version: { id: 'source-4k', label: '4K', edition_id: 'film-1', selected: true, available: true }, session_id: `session-${plans}`, media_url: `/media-${plans}.mp4`, heartbeat_url: `/api/v1/playback/sessions/session-${plans}/heartbeat`, stop_url: `/stop-${plans}`, resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/session-1/heartbeat')) return new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 });
    if (path.endsWith('/heartbeat')) return new Response(JSON.stringify({ expires_at: 9999999999 }));
    return new Response(JSON.stringify(path.includes('/catalog/items/') ? assessedItem : { stopped: true }));
  });

  render(<Player catalogID="film-1" versionID="source-4k" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/media-1.mp4'));
  fireEvent.pause(video);
  await waitFor(() => expect(planBodies).toHaveLength(2));
  expect(planBodies.map((body) => body.version_id)).toEqual(['source-4k', 'source-4k']);
});

it('requires a viewer action before using an alternative version', async () => {
  const planBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) {
      const body = JSON.parse(String(init?.body));
      planBodies.push(body);
      if (body.version_id === 'source-4k') return new Response(JSON.stringify({ error: { code: 'playback_version_unavailable', requested_version_id: 'source-4k', alternatives: [{ id: 'source-1080', label: '1080p · H.264', edition_id: 'film-1', selected: false, available: true }] } }), { status: 409 });
      return new Response(JSON.stringify({ plan: { kind: 'direct' }, version: { id: 'source-1080', label: '1080p · H.264', edition_id: 'film-1', selected: true, available: true }, session_id: 'fallback', media_url: '/fallback.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" versionID="source-4k" onExit={() => undefined} />);

  expect(await screen.findByRole('alert')).toHaveTextContent(/selected version is no longer available/i);
  expect(planBodies).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: /play 1080p.*instead/i }));
  await waitFor(() => expect(planBodies).toHaveLength(2));
  expect(planBodies[1]).toMatchObject({ catalog_id: 'film-1', version_id: 'source-1080' });
});

it.each(['playback_unsupported', 'ffmpeg_unavailable'] as const)('offers an explicit Original retry when Auto cannot use a capped rendition (%s)', async (code) => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const planBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 3840, height: 2160, hdr: 'smpte2084', bitrate: 20_000_000 }));
    if (path.endsWith('/playback/plans')) {
      const body = JSON.parse(String(init?.body));
      planBodies.push(body);
      if (body.quality.mode === 'auto') return new Response(JSON.stringify({ error: { code } }), { status: 422 });
      return new Response(JSON.stringify({ plan: { kind: 'direct', width: 3840, height: 2160, video_bitrate: 20_000_000 }, session_id: 'original', media_url: '/original-4k.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  expect(await screen.findByRole('alert')).toHaveTextContent(code === 'ffmpeg_unavailable' ? /FFmpeg is unavailable/i : /not compatible/i);
  fireEvent.click(screen.getByRole('button', { name: 'Try Original quality' }));
  await waitFor(() => expect(planBodies).toHaveLength(2));
  expect(planBodies[1]).toMatchObject({ quality: { mode: 'original' } });
  expect(document.querySelector('video')).toHaveAttribute('src', '/original-4k.mp4');
  expect(localStorage.getItem('flixr.playback.quality')).toBe('original');
});

it('changes quality through a prepared replacement while preserving position and rate intent', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const qualityBodies: Array<Record<string, unknown>> = [];
  const handoffBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720, video_bitrate: 2_500_000 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480, video_bitrate: 1_000_000 }, session_id: 'saver', media_url: '/saver.m3u8', handoff_url: '/api/v1/playback/handoffs/handoff-1', resume_ms: 23_000, stream_offset_ms: 23_000, expires_at: 9999999999 }));
    }
    if (path.endsWith('/playback/handoffs/handoff-1')) handoffBodies.push(JSON.parse(String(init?.body)));
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  video.currentTime = 23;
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Playback speed' }), { target: { value: '1.5' } });
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(qualityBodies).toHaveLength(1));
  expect(qualityBodies[0]).toMatchObject({ position_ms: 23_000, smooth_handoff: true, quality: { mode: 'data_saver', max_video_bitrate: 1_000_000, max_width: 854, max_height: 480 } });
  await waitFor(() => expect(video).toHaveAttribute('src', '/saver.m3u8'));
  expect(handoffBodies).toEqual([]);
  fireEvent.canPlay(video);
  await waitFor(() => expect(handoffBodies).toEqual([{ attached: true }]));
  fireEvent.loadedMetadata(video);
  expect(video.playbackRate).toBe(1.5);
});

it('aborts an unusable quality candidate and restores the retained source', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const handoffs: boolean[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480 }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/timeout', resume_ms: 10_000, stream_offset_ms: 10_000, expires_at: 9999999999 }));
    if (path.endsWith('/playback/handoffs/timeout')) handoffs.push(JSON.parse(String(init?.body)).attached);
    return new Response(JSON.stringify({ attached: handoffs.at(-1) }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  let displayingFullscreen = true;
  Object.defineProperty(video, 'webkitDisplayingFullscreen', { configurable: true, get: () => displayingFullscreen });
  vi.mocked(HTMLMediaElement.prototype.load).mockImplementation(() => { displayingFullscreen = false; });
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(video).toHaveAttribute('src', '/candidate.m3u8'));

  await act(async () => { await vi.advanceTimersByTimeAsync(8_100); });
  await waitFor(() => expect(handoffs).toEqual([false]));
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  fireEvent.canPlay(video);
  expect((video as HTMLVideoElement & { webkitDisplayingFullscreen: boolean }).webkitDisplayingFullscreen).toBe(true);
});

it('restores the retained source when a usable candidate cannot confirm commit', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const handoffs: boolean[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480 }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/uncertain', resume_ms: 10_000, stream_offset_ms: 10_000, expires_at: 9999999999 }));
    if (path.endsWith('/playback/handoffs/uncertain')) {
      const attached = JSON.parse(String(init?.body)).attached as boolean;
      handoffs.push(attached);
      if (attached) return new Response(JSON.stringify({ error: { code: 'request_failed' } }), { status: 503 });
      return new Response(JSON.stringify({ attached: false }));
    }
    return new Response(JSON.stringify({ accepted: true }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(video).toHaveAttribute('src', '/candidate.m3u8'));
  fireEvent.canPlay(video);

  await waitFor(() => expect(handoffs).toEqual([true, true, false]));
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  fireEvent.canPlay(video);
});

it('honors a pause made while an HLS quality candidate waits for readiness', async () => {
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480 }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/pause', resume_ms: 10_000, stream_offset_ms: 10_000, expires_at: 9999999999 }));
    if (path.endsWith('/playback/handoffs/pause')) return new Response(JSON.stringify({ attached: JSON.parse(String(init?.body)).attached }));
    return new Response(JSON.stringify({ accepted: true }));
  });
  let paused = false;
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(async () => { paused = false; });
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => { paused = true; });
  const listen = vi.spyOn(screenCoordinator, 'onCommand');
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  await waitFor(() => expect(hls.attached).toBe(1));
  fireEvent.loadedMetadata(video);
  await waitFor(() => expect(play).toHaveBeenCalledTimes(1));
  fireEvent.playing(video);
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(hls.attached).toBe(2));
  act(() => { listen.mock.calls[0][0]({ version: 1, type: 'pause' }); });
  fireEvent.canPlay(video);

  await waitFor(() => expect(screen.getByRole('button', { name: 'Play' })).toBeVisible());
  expect(play).toHaveBeenCalledTimes(1);
});

it('does not queue startup fallback while a paused Auto candidate waits for readiness', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  localStorage.setItem('flixr.playback.quality', 'data_saver');
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const qualityBodies: Array<Record<string, unknown>> = [];
  const listen = vi.spyOn(screenCoordinator, 'onCommand');
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480 }, session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720 }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/paused-auto', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/playback/handoffs/paused-auto')) return new Response(JSON.stringify({ attached: JSON.parse(String(init?.body)).attached }));
    return new Response(JSON.stringify({ accepted: true }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/saver.m3u8'));
  fireEvent.loadedMetadata(video);
  act(() => { listen.mock.calls[0][0]({ version: 1, type: 'pause' }); });
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'auto' } });
  await waitFor(() => expect(video).toHaveAttribute('src', '/candidate.m3u8'));
  await act(async () => { await vi.advanceTimersByTimeAsync(3_100); });
  fireEvent.loadedData(video);

  await waitFor(() => expect(qualityBodies).toHaveLength(1));
  await act(async () => { await vi.advanceTimersByTimeAsync(100); });
  expect(qualityBodies).toHaveLength(1);
});

it('does not commit native handoff from stale readyState before the candidate source loads', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const handoffs: boolean[] = [];
  let currentSource = new URL('/old.m3u8', window.location.href).href;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'old', media_url: '/old.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/native-source', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/playback/handoffs/native-source')) handoffs.push(JSON.parse(String(init?.body)).attached);
    return new Response(JSON.stringify({ attached: handoffs.at(-1) }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  Object.defineProperty(video, 'currentSrc', { configurable: true, get: () => currentSource });
  Object.defineProperty(video, 'readyState', { configurable: true, value: video.HAVE_FUTURE_DATA });
  await waitFor(() => expect(video).toHaveAttribute('src', '/old.m3u8'));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(video).toHaveAttribute('src', '/candidate.m3u8'));
  fireEvent.abort(video);
  expect(handoffs).toEqual([]);

  currentSource = new URL('/candidate.m3u8', window.location.href).href;
  fireEvent.loadedData(video);
  await waitFor(() => expect(handoffs).toEqual([true]));
});

it('positions a transferred HLS candidate before checking its source-relative buffer', async () => {
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  hls.bufferedTransfer = true;
  const handoffs: boolean[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480 }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/position', resume_ms: 25_000, stream_offset_ms: 25_000, expires_at: 9999999999 }));
    if (path.endsWith('/playback/handoffs/position')) handoffs.push(JSON.parse(String(init?.body)).attached);
    return new Response(JSON.stringify({ attached: handoffs.at(-1) }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(hls.attached).toBe(1));
  video.currentTime = 25;
  Object.defineProperty(video, 'buffered', { configurable: true, value: { length: 1, start: () => 0, end: () => 2 } });
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });

  await waitFor(() => expect(hls.attached).toBe(2));
  expect(video.currentTime).toBe(0);
  await waitFor(() => expect(handoffs).toEqual([true]));
});

it('resumes at the latest position after quality preparation while the old source advances', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  let resolveQuality: ((response: Response) => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Promise<Response>((resolve) => { resolveQuality = resolve; });
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  Object.defineProperty(video, 'paused', { configurable: true, value: false });
  video.currentTime = 23;
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(resolveQuality).toBeTypeOf('function'));
  video.currentTime = 28;
  await act(async () => { resolveQuality?.(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 23_000, stream_offset_ms: 23_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(video).toHaveAttribute('src', '/saver.m3u8'));
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(5);
});

it('hands pending HLS quality intent to direct playback without resetting fullscreen', async () => {
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  hls.mediaSourceTransfer = true;
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const pending: Array<(response: Response) => void> = [];
  const qualityBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      return new Promise<Response>((resolve) => { pending.push(resolve); });
    }
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(hls.attached).toBe(1));
  let displayingFullscreen = true;
  Object.defineProperty(video, 'webkitDisplayingFullscreen', { configurable: true, get: () => displayingFullscreen });
  vi.mocked(HTMLMediaElement.prototype.load).mockImplementation(() => { displayingFullscreen = false; });
  let paused = false;
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  fireEvent.playing(video);
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
  fireEvent.pause(video);
  await waitFor(() => expect(screen.getByRole('button', { name: 'Play' })).toBeVisible());
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(pending).toHaveLength(1));
  fireEvent.click(screen.getByRole('button', { name: 'Play' }));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'original' } });
  await act(async () => { pending[0](new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }))); });
  await waitFor(() => expect(pending).toHaveLength(2));
  expect(qualityBodies[1]).toMatchObject({ quality: { mode: 'original' } });
  expect(Number(qualityBodies[1].observation)).toBeGreaterThan(Number(qualityBodies[0].observation));
  play.mockClear();
  await act(async () => { pending[1](new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'original', media_url: '/original.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }))); });
  await waitFor(() => expect(video).toHaveAttribute('src', '/original.mp4'));
  fireEvent.loadedMetadata(video);
  expect(play).toHaveBeenCalledOnce();
  expect((video as HTMLVideoElement & { webkitDisplayingFullscreen: boolean }).webkitDisplayingFullscreen).toBe(true);
  expect(hls.released).toBe(1);
  expect(hls.ended).toBe(1);
});

it('aborts a pending quality request and bounds Back navigation', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  let qualitySignal: AbortSignal | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualitySignal = init?.signal ?? undefined;
      return new Promise<Response>(() => undefined);
    }
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  const onExit = vi.fn();
  render(<Player catalogID="film-1" onExit={onExit} />);
  await waitFor(() => expect(document.querySelector('video')).toHaveAttribute('src', '/balanced.m3u8'));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(qualitySignal).toBeDefined());
  vi.useFakeTimers();
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  expect(qualitySignal?.aborted).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });
  expect(onExit).toHaveBeenCalledOnce();
});

it('aborts a never-settling handoff resolution and bounds Back navigation', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  let handoffSignal: AbortSignal | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/stalled', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/playback/handoffs/stalled')) {
      handoffSignal = init?.signal ?? undefined;
      return new Promise<Response>(() => undefined);
    }
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  const onExit = vi.fn();
  render(<Player catalogID="film-1" onExit={onExit} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(video).toHaveAttribute('src', '/candidate.m3u8'));
  fireEvent.loadedData(video);
  await waitFor(() => expect(handoffSignal).toBeDefined());

  vi.useFakeTimers();
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  expect(handoffSignal?.aborted).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });
  expect(onExit).toHaveBeenCalledOnce();
});

it('times out ambiguous handoff requests, restores the retained source, and releases queued seek', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const handoffBodies: boolean[] = [];
  const seekBodies: number[] = [];
  const listen = vi.spyOn(screenCoordinator, 'onCommand');
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'candidate', media_url: '/candidate.m3u8', handoff_url: '/api/v1/playback/handoffs/ambiguous', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/playback/handoffs/ambiguous')) {
      handoffBodies.push(JSON.parse(String(init?.body)).attached);
      return new Promise<Response>(() => undefined);
    }
    if (path.endsWith('/seek')) {
      seekBodies.push(JSON.parse(String(init?.body)).position_ms);
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'seek', media_url: '/seek.m3u8', resume_ms: 30_000, stream_offset_ms: 30_000, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(video).toHaveAttribute('src', '/candidate.m3u8'));
  fireEvent.loadedData(video);
  await waitFor(() => expect(handoffBodies).toEqual([true]));
  act(() => { listen.mock.calls[0][0]({ version: 1, type: 'seek', position_ms: 30_000 }); });

  await act(async () => { await vi.advanceTimersByTimeAsync(6_100); });
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  fireEvent.loadedData(video);
  await waitFor(() => expect(seekBodies).toEqual([30_000]));
  expect(handoffBodies).toEqual([true, true, false]);
});

it('does not launch quality preparation after Back wins a pending capability check', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  let itemCalls = 0;
  let qualityCalls = 0;
  let resolveDetail: ((response: Response) => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) {
      itemCalls += 1;
      if (itemCalls === 1) return new Response(JSON.stringify(assessedItem));
      return new Promise<Response>((resolve) => { resolveDetail = resolve; });
    }
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'original', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) qualityCalls += 1;
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  const onExit = vi.fn();
  render(<Player catalogID="film-1" onExit={onExit} />);
  await waitFor(() => expect(document.querySelector('video')).toHaveAttribute('src', '/film.mp4'));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(resolveDetail).toBeTypeOf('function'));
  vi.useFakeTimers();
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });
  expect(onExit).toHaveBeenCalledOnce();
  await act(async () => { resolveDetail?.(new Response(JSON.stringify(assessedItem))); await Promise.resolve(); });
  expect(qualityCalls).toBe(0);
});

it('discards a quality response that arrives after completion starts', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  let resolveQuality: ((response: Response) => void) | undefined;
  let qualitySignal: AbortSignal | undefined;
  const stops: string[] = [];
  const handoffs: boolean[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'original', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualitySignal = init?.signal ?? undefined;
      return new Promise<Response>((resolve) => { resolveQuality = resolve; });
    }
    if (path.endsWith('/next?include_specials=false')) return new Response(JSON.stringify({ state: 'end' }));
    if (path.endsWith('/playback/handoffs/late')) handoffs.push(JSON.parse(String(init?.body)).attached);
    if (path.endsWith('/stop')) stops.push(path);
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/film.mp4'));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(resolveQuality).toBeTypeOf('function'));
  vi.useFakeTimers();
  fireEvent.ended(video);
  expect(qualitySignal?.aborted).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });
  await act(async () => { resolveQuality?.(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'late-quality', media_url: '/late.m3u8', handoff_url: '/api/v1/playback/handoffs/late', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }))); await Promise.resolve(); });
  expect(video).toHaveAttribute('src', '/film.mp4');
  expect(handoffs).toEqual([false]);
  expect(stops.some((path) => path.includes('late-quality'))).toBe(false);
});

it('retries an adaptive ramp without resetting the active media pipeline', async () => {
  localStorage.setItem('flixr.playback.auto-throughput', JSON.stringify({ server: window.location.origin, bitsPerSecond: 900_000, measuredAt: Date.now() }));
  let now = 1_000;
  let qualityCalls = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'low', media_url: '/low.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityCalls += 1;
      if (qualityCalls === 1) return new Response(JSON.stringify({ error: { code: 'playback_prepare_failed' } }), { status: 503 });
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480, video_bitrate: 1_000_000 }, session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  let displayingFullscreen = true;
  Object.defineProperty(video, 'webkitDisplayingFullscreen', { configurable: true, get: () => displayingFullscreen });
  vi.mocked(HTMLMediaElement.prototype.load).mockImplementation(() => { displayingFullscreen = false; });
  vi.spyOn(Date, 'now').mockImplementation(() => now);
  let fragmentID = 0;
  const fragment = () => hls.frag?.({}, { frag: { url: `/fragment-${fragmentID += 1}.m4s`, stats: { loading: { start: 0, end: 160 } } }, payload: new Uint8Array(100_000) });
  for (now of [1_000, 5_000, 9_000, 16_000]) fragment();
  await waitFor(() => expect(qualityCalls).toBe(1));
  for (now of [27_000, 31_000, 35_000, 42_000]) fragment();
  await waitFor(() => expect(qualityCalls).toBe(2));
  await waitFor(() => expect(hls.attached).toBe(2));
  expect((video as HTMLVideoElement & { webkitDisplayingFullscreen: boolean }).webkitDisplayingFullscreen).toBe(true);
});

it('keeps Auto policy aligned with retained Data saver after a failed manual Auto change', async () => {
  localStorage.setItem('flixr.playback.quality', 'data_saver');
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  const qualityBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480, video_bitrate: 1_000_000, audio_bitrate: 96_000 }, session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      if (qualityBodies.length === 1) return new Response(JSON.stringify({ error: { code: 'playback_prepare_failed' } }), { status: 503 });
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720, video_bitrate: 2_500_000, audio_bitrate: 128_000 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'auto' } });
  await waitFor(() => expect(qualityBodies).toHaveLength(1));

  let now = 1_000;
  vi.spyOn(Date, 'now').mockImplementation(() => now);
  let fragmentID = 0;
  const fragment = () => hls.frag?.({}, { frag: { url: `/healthy-${fragmentID += 1}.m4s`, stats: { loading: { start: 0, end: 160 } } }, payload: new Uint8Array(100_000) });
  for (now of [1_000, 5_000, 9_000, 16_000]) fragment();

  await waitFor(() => expect(qualityBodies).toHaveLength(2));
  expect(qualityBodies[1]).toMatchObject({ quality: { mode: 'auto', max_height: 720, max_video_bitrate: 2_500_000 } });
});

it('downshifts a constrained HLS startup before the first playing event', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  const qualityBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720, video_bitrate: 2_500_000, audio_bitrate: 128_000 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480, video_bitrate: 1_000_000, audio_bitrate: 96_000 }, session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  expect(qualityBodies).toEqual([]);
  await act(async () => { await vi.advanceTimersByTimeAsync(3_100); });
  await waitFor(() => expect(qualityBodies).toHaveLength(1));
  expect(qualityBodies[0]).toMatchObject({ smooth_handoff: true, quality: { max_height: 480, max_video_bitrate: 1_000_000 } });
});

it('uses measured constrained startup delivery to skip directly to the safe floor', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  const qualityBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720, video_bitrate: 2_500_000, audio_bitrate: 128_000 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 360, video_bitrate: 500_000, audio_bitrate: 64_000 }, session_id: 'low', media_url: '/low.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  let progress: ((event: { loaded: number }) => void) | undefined;
  const setup = hls.configs[0].xhrSetup as (xhr: { addEventListener: (event: string, listener: (event: { loaded: number }) => void) => void }, url: string, context: { type: string; url: string }) => void;
  vi.spyOn(performance, 'now').mockReturnValueOnce(0).mockReturnValue(3_000);
  setup({ addEventListener: (_event, listener) => { progress = listener; } }, '/initial.m4s', { type: 'media-fragment', url: '/initial.m4s' });
  progress?.({ loaded: 300_000 });
  await act(async () => { await vi.advanceTimersByTimeAsync(3_100); });

  await waitFor(() => expect(qualityBodies).toHaveLength(1));
  expect(qualityBodies[0]).toMatchObject({ quality: { max_height: 360, max_video_bitrate: 500_000 } });
});

it('uses the safe floor for a native HLS startup without fragment telemetry', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const qualityBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720, video_bitrate: 2_500_000, audio_bitrate: 128_000 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 360, video_bitrate: 500_000, audio_bitrate: 64_000 }, session_id: 'low', media_url: '/low.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(document.querySelector('video')).toHaveAttribute('src', '/balanced.m3u8'));
  await act(async () => { await vi.advanceTimersByTimeAsync(3_100); });

  await waitFor(() => expect(qualityBodies).toHaveLength(1));
  expect(qualityBodies[0]).toMatchObject({ quality: { max_height: 360, max_video_bitrate: 500_000 } });
});

it('measures in-flight HLS fragment delivery before the fragment completes', async () => {
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  const qualityBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, width: 1920, height: 1080, bitrate: 8_000_000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 720, video_bitrate: 2_500_000, audio_bitrate: 128_000 }, session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      qualityBodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ plan: { kind: 'transcode', height: 480, video_bitrate: 1_000_000 }, session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ accepted: true }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));

  let elapsed = 0;
  let wallNow = 0;
  vi.spyOn(performance, 'now').mockImplementation(() => elapsed);
  vi.spyOn(Date, 'now').mockImplementation(() => wallNow);
  const setup = hls.configs[0].xhrSetup as (xhr: { addEventListener: (event: string, listener: (event: { loaded: number }) => void) => void }, url: string, context: { type: string; url: string }) => void;
  for (const now of [1_000, 3_000, 5_000, 7_000, 9_000]) {
    let progress: ((event: { loaded: number }) => void) | undefined;
    const xhrProbe = { addEventListener: (event: string, listener: (event: { loaded: number }) => void) => { if (event === 'progress') progress = listener; } };
    elapsed = now - 1_000;
    setup(xhrProbe, `/segment-${now}.m4s`, { type: 'media-fragment', url: `/segment-${now}.m4s` });
    elapsed = now;
    wallNow = now;
    progress?.({ loaded: 100_000 });
  }
  await waitFor(() => expect(qualityBodies).toHaveLength(1));
  expect(qualityBodies[0]).toMatchObject({ quality: { max_height: 480 } });
});

it('serializes quality, seek and audio replacements against the latest session', async () => {
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  const operations: string[] = [];
  let resolveQuality: ((response: Response) => void) | undefined;
  let resolveSeek: ((response: Response) => void) | undefined;
  let resolveAudio: ((response: Response) => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120_000, width: 1920, height: 1080, bitrate: 8_000_000, audio: [{ index: 0, codec: 'aac' }, { index: 1, codec: 'aac' }] }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode', audio_stream_index: 0 }, audio_tracks: [{ index: 0, codec: 'aac' }, { index: 1, codec: 'aac' }], session_id: 'balanced', media_url: '/balanced.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/quality')) {
      operations.push('quality');
      return new Promise<Response>((resolve) => { resolveQuality = resolve; });
    }
    if (path.endsWith('/seek')) {
      operations.push(`seek:${path}`);
      return new Promise<Response>((resolve) => { resolveSeek = resolve; });
    }
    if (path.endsWith('/audio')) {
      operations.push(`audio:${path}`);
      return new Promise<Response>((resolve) => { resolveAudio = resolve; });
    }
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/balanced.m3u8'));
  Object.defineProperty(video, 'paused', { configurable: true, value: false });
  fireEvent.playing(video);
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Streaming quality' }), { target: { value: 'data_saver' } });
  await waitFor(() => expect(resolveQuality).toBeTypeOf('function'));
  fireEvent.change(screen.getByRole('slider', { name: 'Seek' }), { target: { value: '30000' } });
  fireEvent.change(screen.getByDisplayValue('Unknown language'), { target: { value: 'embedded:1' } });
  expect(operations).toEqual(['quality']);
  await act(async () => { resolveQuality?.(new Response(JSON.stringify({ plan: { kind: 'transcode', audio_stream_index: 0 }, audio_tracks: [{ index: 0, codec: 'aac' }, { index: 1, codec: 'aac' }], session_id: 'saver', media_url: '/saver.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }))); });
  await waitFor(() => expect(resolveSeek).toBeTypeOf('function'));
  expect(operations[1]).toContain('seek:/api/v1/playback/sessions/saver/seek');
  await act(async () => { resolveSeek?.(new Response(JSON.stringify({ plan: { kind: 'transcode', audio_stream_index: 0 }, audio_tracks: [{ index: 0, codec: 'aac' }, { index: 1, codec: 'aac' }], session_id: 'seeked', media_url: '/seeked.m3u8', resume_ms: 30_000, stream_offset_ms: 30_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(resolveAudio).toBeTypeOf('function'));
  expect(operations[2]).toContain('audio:/api/v1/playback/sessions/seeked/audio');
  await act(async () => { resolveAudio?.(new Response(JSON.stringify({ plan: { kind: 'transcode', audio_stream_index: 1 }, audio_tracks: [{ index: 0, codec: 'aac' }, { index: 1, codec: 'aac' }], session_id: 'audio', media_url: '/audio.m3u8', resume_ms: 30_000, stream_offset_ms: 30_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(video).toHaveAttribute('src', '/audio.m3u8'));
});

it('does not create a playback session after unmount while capability assessment is pending', async () => {
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
  let finishAssessment: ((value: MediaCapabilitiesDecodingInfo) => void) | undefined;
  const decodingInfo = vi.fn()
    .mockImplementationOnce(() => new Promise<MediaCapabilitiesDecodingInfo>((resolve) => { finishAssessment = resolve; }))
    .mockResolvedValue({ supported: true, smooth: true, powerEfficient: true, keySystemAccess: null });
  Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });
  const requests: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    requests.push(path);
    if (path.endsWith('/catalog/items/film-1')) return new Response(JSON.stringify(assessedItem));
    return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
  });
  const { unmount } = render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(finishAssessment).toBeTypeOf('function'));

  unmount();
  await act(async () => { finishAssessment!({ supported: true, smooth: true, powerEfficient: true, keySystemAccess: null }); });

  await waitFor(() => expect(decodingInfo).toHaveBeenCalled());
  expect(requests.some((path) => path.endsWith('/playback/plans'))).toBe(false);
});

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

it('bounds a hung final heartbeat before stopping an explicit player exit', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) return new Promise<Response>(() => undefined);
    return new Response(JSON.stringify({ stopped: true }));
  });
  const exit = vi.fn();
  render(<Player catalogID="film-1" onExit={exit} />);
  await waitFor(() => expect(document.querySelector('video')).toHaveAttribute('src', '/film.mp4'));

  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  expect(calls.filter((path) => path.endsWith('/stop'))).toHaveLength(0);
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });

  await waitFor(() => expect(exit).toHaveBeenCalledOnce());
  expect(calls.filter((path) => path.endsWith('/heartbeat'))).toHaveLength(1);
  expect(calls.filter((path) => path.endsWith('/stop'))).toHaveLength(1);
});

it('bounds a hung final heartbeat before stopping an unmounted player', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) return new Promise<Response>(() => undefined);
    return new Response(JSON.stringify({ stopped: true }));
  });
  const { unmount } = render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(document.querySelector('video')).toHaveAttribute('src', '/film.mp4'));

  unmount();
  expect(calls.filter((path) => path.endsWith('/stop'))).toHaveLength(0);
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });

  await waitFor(() => expect(calls.filter((path) => path.endsWith('/stop'))).toHaveLength(1));
  expect(calls.filter((path) => path.endsWith('/heartbeat'))).toHaveLength(1);
});

it('beacons progress on pagehide for the active plan', async () => {
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: vi.fn(() => true) });
  const beacon = vi.spyOn(navigator, 'sendBeacon');
	const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: 'data:video/mp4;base64,', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 })));
	render(<Player catalogID="film-1" onExit={() => undefined} />);
	await screen.findByRole('button', { name: /back to library/i });
	await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  await waitFor(() => fireEvent(window, new Event('pagehide')));
  expect(beacon).toHaveBeenCalledWith('/heartbeat', expect.any(Blob));
});

it('rejects an HLS plan that resolves after the catalog destination changes', async () => {
  let resolveFirst: ((response: Response) => void) | undefined;
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (!path.endsWith('/playback/plans')) return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
    const catalogID = JSON.parse(String(init?.body)).catalog_id;
    if (catalogID === 'film-1') return new Promise<Response>((resolve) => { resolveFirst = resolve; });
    return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: catalogID, media_url: 'data:video/mp4;base64,', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
  });
  const { rerender } = render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(resolveFirst).toBeTypeOf('function'));
  rerender(<Player catalogID="film-2" onExit={() => undefined} />);
  await waitFor(() => expect(fetcher.mock.calls.some(([, init]) => String(init?.body).includes('film-2'))).toBe(true));
  await act(async () => { resolveFirst?.(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'film-1', media_url: '/stale/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }))); });
  expect(hls.attached).toBe(0);
});
it('sends the compatibility seek position and surfaces a fatal HLS error', async () => {
  const requests: Array<{ path: string; body?: string }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    requests.push({ path, body: init?.body as string | undefined });
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 7_000, stream_offset_ms: 2_000, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 7_000, stream_offset_ms: 2_000, expires_at: 9999999999 }));
    return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  video.currentTime = 5;
  fireEvent.seeked(video);
  await waitFor(() => expect(requests.some((request) => request.path.endsWith('/seek') && JSON.parse(request.body ?? '{}').position_ms === 7_000)).toBe(true));
  await waitFor(() => expect(hls.configs.at(-1)).toMatchObject({ startPosition: 5, maxBufferLength: 20, maxMaxBufferLength: 30 }));
  hls.error?.({}, { fatal: true, type: 'mediaError' });
  expect(await screen.findByRole('alert')).toHaveTextContent(/compatibility stream stopped unexpectedly/i);
});

it('uses native HLS when the coarse MSE probe and hls.js support disagree', async () => {
  const originalMediaSource = globalThis.MediaSource;
  Object.defineProperty(globalThis, 'MediaSource', { configurable: true, value: { isTypeSupported: () => true } });
  hls.supported = false;
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'native', media_url: '/native/master.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    return new Response(JSON.stringify(assessedItem));
  });
  try {
    render(<Player catalogID="film-1" onExit={() => undefined} />);
    await waitFor(() => expect(document.querySelector('video')).toHaveAttribute('src', '/native/master.m3u8'));
    expect(hls.attached).toBe(0);
  } finally {
    Object.defineProperty(globalThis, 'MediaSource', { configurable: true, value: originalMediaSource });
  }
});

it('uses Managed Media Source for adaptive HLS when MediaSource is absent', async () => {
  const originalMediaSource = globalThis.MediaSource;
  const managedGlobal = globalThis as typeof globalThis & { ManagedMediaSource?: typeof MediaSource };
  const originalManaged = managedGlobal.ManagedMediaSource;
  Object.defineProperty(globalThis, 'MediaSource', { configurable: true, value: undefined });
  Object.defineProperty(managedGlobal, 'ManagedMediaSource', { configurable: true, value: { isTypeSupported: () => true } });
  const planBodies: Array<Record<string, unknown>> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    if (String(input).endsWith('/playback/plans')) {
      planBodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'mms', media_url: '/mms/master.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify(assessedItem));
  });
  try {
    render(<Player catalogID="film-1" onExit={() => undefined} />);
    await waitFor(() => expect(hls.attached).toBe(1));
    expect(planBodies[0]).toMatchObject({ quality: { mode: 'auto', max_video_bitrate: 2_500_000, max_width: 1280, max_height: 720 } });
  } finally {
    Object.defineProperty(globalThis, 'MediaSource', { configurable: true, value: originalMediaSource });
    Object.defineProperty(managedGlobal, 'ManagedMediaSource', { configurable: true, value: originalManaged });
  }
});

it('keeps the current stream usable when an HLS seek replacement is refused', async () => {
  let seeks = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) {
      seeks += 1;
      if (seeks === 1) return new Response(JSON.stringify({ error: { code: 'playback_capacity' } }), { status: 503 });
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 5_000, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video')!;

  video.currentTime = 3;
  fireEvent.seeked(video);
  expect(await screen.findByRole('alert')).toHaveTextContent(/playback limit/i);
  expect(document.querySelector('video')).toBe(video);
  expect(screen.queryByText('Playback stopped')).not.toBeInTheDocument();

  video.currentTime = 5;
  fireEvent.seeked(video);
  await waitFor(() => expect(seeks).toBe(2));
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  expect(document.querySelector('video')).toBe(video);
});

it('recovers an expired playback lease from the server-acknowledged position', async () => {
  const calls: string[] = [];
  const planIntents: string[] = [];
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      planIntents.push(JSON.parse(String(init?.body)).continue_watching_intent);
      return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: `session-${plans}`, media_url: `/media-${plans}.mp4`, heartbeat_url: `/api/v1/playback/sessions/session-${plans}/heartbeat`, seek_url: `/seek-${plans}`, stop_url: `/stop-${plans}`, resume_ms: plans === 1 ? 0 : 12_000, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/session-1/heartbeat')) return new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 });
    if (path.endsWith('/heartbeat')) return new Response(JSON.stringify({ expires_at: 9999999999 }));
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/media-1.mp4'));
  fireEvent.pause(video);
  await waitFor(() => expect(video).toHaveAttribute('src', '/media-2.mp4'));
  fireEvent.loadedMetadata(video);

  expect(video.currentTime).toBe(12);
  expect(plans).toBe(2);
  expect(planIntents).toEqual(['user', 'recovery']);
  expect(calls.some((path) => path.endsWith('/session-1/stop'))).toBe(true);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('does not retry after viewer authorization is revoked', async () => {
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/media.mp4', heartbeat_url: '/api/v1/playback/sessions/session-1/heartbeat', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/heartbeat')) return new Response(JSON.stringify({ error: { code: 'profile_required' } }), { status: 403 });
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/media.mp4'));
  fireEvent.pause(video);

  expect(await screen.findByRole('alert')).toHaveTextContent(/choose a profile/i);
  expect(plans).toBe(1);
});

it('replans a fatal HLS network failure and rejects the stale source', async () => {
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: `session-${plans}`, media_url: `/stream-${plans}/manifest.m3u8`, heartbeat_url: `/heartbeat-${plans}`, resume_ms: plans === 1 ? 4_000 : 7_000, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  hls.error?.({}, { fatal: true, type: 'networkError' });

  await waitFor(() => expect(hls.attached).toBe(2));
  expect(plans).toBe(2);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('replans an HLS segment network failure before the media element becomes unusable', async () => {
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: `session-${plans}`, media_url: `/stream-${plans}/manifest.m3u8`, heartbeat_url: `/heartbeat-${plans}`, resume_ms: 6_000, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(hls.attached).toBe(1));
  hls.error?.({}, { fatal: false, type: 'networkError' });
  fireEvent.error(video);

  await waitFor(() => expect(hls.attached).toBe(2));
  expect(plans).toBe(2);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('replans a compatibility stream when the browser reports the segment failure as a media error', async () => {
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: `session-${plans}`, media_url: `/stream-${plans}/manifest.m3u8`, heartbeat_url: `/heartbeat-${plans}`, resume_ms: 6_000, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(hls.attached).toBe(1));
  fireEvent.error(video);

  await waitFor(() => expect(hls.attached).toBe(2));
  expect(plans).toBe(2);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('caps consecutive compatibility stream recovery cycles', async () => {
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: `session-${plans}`, media_url: `/stream-${plans}/manifest.m3u8`, heartbeat_url: `/heartbeat-${plans}`, resume_ms: 6_000, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    hls.error?.({}, { fatal: false, type: 'networkError' });
    await waitFor(() => expect(hls.attached).toBe(attempt + 1));
  }
  hls.error?.({}, { fatal: false, type: 'networkError' });

  expect(await screen.findByRole('alert')).toHaveTextContent(/could not reconnect/i);
  expect(plans).toBe(4);
});

it('does not let a stale successful heartbeat reset the recovery cap', async () => {
  let resolveStaleHeartbeat: ((response: Response) => void) | undefined;
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: `session-${plans}`, media_url: `/stream-${plans}/manifest.m3u8`, heartbeat_url: `/heartbeat-${plans}`, resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/session-1/heartbeat')) return new Promise<Response>((resolve) => { resolveStaleHeartbeat = resolve; });
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(hls.attached).toBe(1));
  fireEvent.pause(video);
  await waitFor(() => expect(resolveStaleHeartbeat).toBeTypeOf('function'));
  hls.error?.({}, { fatal: false, type: 'networkError' });
  await waitFor(() => expect(hls.attached).toBe(2));
  resolveStaleHeartbeat!(new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 })));
  await act(async () => { await Promise.resolve(); });

  for (let attempt = 0; attempt < 2; attempt += 1) {
    hls.error?.({}, { fatal: false, type: 'networkError' });
    await waitFor(() => expect(hls.attached).toBe(attempt + 3));
  }
  hls.error?.({}, { fatal: false, type: 'networkError' });

  expect(await screen.findByRole('alert')).toHaveTextContent(/could not reconnect/i);
  expect(plans).toBe(4);
});

it('cancels an in-flight recovery plan on explicit stop and releases its late session', async () => {
  let resolveRecovery: ((response: Response) => void) | undefined;
  const stopped: string[] = [];
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      if (plans === 2) return new Promise<Response>((resolve) => { resolveRecovery = resolve; });
      return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/media-1.mp4', heartbeat_url: '/api/v1/playback/sessions/session-1/heartbeat', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/session-1/heartbeat')) return new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 });
    if (path.endsWith('/stop')) stopped.push(path);
    return new Response(JSON.stringify({ stopped: true }));
  });
  const exit = vi.fn();
  render(<Player catalogID="film-1" onExit={exit} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/media-1.mp4'));
  fireEvent.pause(video);
  await waitFor(() => expect(resolveRecovery).toBeTypeOf('function'));

  vi.useFakeTimers();
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });
  expect(exit).toHaveBeenCalledOnce();
  vi.useRealTimers();
  resolveRecovery!(new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-2', media_url: '/media-2.mp4', heartbeat_url: '/heartbeat-2', resume_ms: 12_000, stream_offset_ms: 0, expires_at: 9999999999 })));

  await waitFor(() => expect(stopped.some((path) => path.endsWith('/session-2/stop'))).toBe(true));
  expect(video).not.toHaveAttribute('src', '/media-2.mp4');
});

it('does not start recovery when a heartbeat fails after unmount', async () => {
  let rejectHeartbeat: ((error: unknown) => void) | undefined;
  let heartbeats = 0;
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: `session-${plans}`, media_url: `/media-${plans}.mp4`, heartbeat_url: `/heartbeat-${plans}`, resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/session-1/heartbeat')) {
      heartbeats += 1;
      if (heartbeats === 1) return new Promise<Response>((_resolve, reject) => { rejectHeartbeat = reject; });
      return new Response(JSON.stringify({ expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true }));
  });

  const { unmount } = render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/media-1.mp4'));
  fireEvent.pause(video);
  await waitFor(() => expect(rejectHeartbeat).toBeTypeOf('function'));

  unmount();
  await act(async () => {
    rejectHeartbeat!(new TypeError('connection lost'));
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });

  expect(plans).toBe(1);
});

it('releases a seek replacement that arrives after explicit stop', async () => {
  let resolveSeek: ((response: Response) => void) | undefined;
  const stopped: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1/manifest.m3u8', heartbeat_url: '/heartbeat-1', seek_url: '/seek-1', stop_url: '/stop-1', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/session-1/seek')) return new Promise<Response>((resolve) => { resolveSeek = resolve; });
    if (path.includes('/playback/sessions/') && path.endsWith('/stop')) stopped.push(path);
    if (path.endsWith('/stop-1')) stopped.push(path);
    return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
  });
  const exit = vi.fn();
  render(<Player catalogID="film-1" onExit={exit} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video')!;
  video.currentTime = 3;
  fireEvent.seeked(video);
  await waitFor(() => expect(resolveSeek).toBeTypeOf('function'));

  vi.useFakeTimers();
  fireEvent.click(screen.getByRole('button', { name: /back to library/i }));
  await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });
  expect(exit).toHaveBeenCalledOnce();
  vi.useRealTimers();
  resolveSeek!(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2/manifest.m3u8', heartbeat_url: '/heartbeat-2', seek_url: '/seek-2', stop_url: '/api/v1/playback/sessions/session-2/stop', resume_ms: 3_000, stream_offset_ms: 3_000, expires_at: 9999999999 })));

  await waitFor(() => expect(stopped.some((path) => path.includes('/session-2/stop'))).toBe(true));
  expect(hls.attached).toBe(1);
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

it('orders seek and ended observations without the device clock and suppresses later writes', async () => {
  const observations: number[] = [];
  let beaconBody: Blob | undefined;
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: (_url: string, body: Blob) => { beaconBody = body; return true; } });
  const plan = { plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream/master.m3u8', heartbeat_url: '/heartbeat', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.endsWith('/heartbeat')) {
      observations.push(JSON.parse(String(init?.body)).observation);
      return new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 }));
    }
    if (path.endsWith('/seek')) observations.push(JSON.parse(String(init?.body)).observation);
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'end_of_series' }));
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
  expect(beaconBody).toBeUndefined();
  unmount();
  expect(observations).toEqual([1, 2]);
});

it('counts down to the server-selected next episode after durable completion', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'next', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true }, selected_version_id: 'series-4k' }));
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  const advance = vi.fn();
  render(<Player catalogID="episode-1" onAdvance={advance} onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  expect(await screen.findByRole('heading', { name: /next episode/i })).toBeVisible();
  expect(screen.getByText(/second signal/i)).toBeVisible();
  expect(calls.findIndex((path) => path.includes('/next?'))).toBeGreaterThan(calls.findIndex((path) => path.endsWith('/heartbeat')));
  for (let second = 0; second < 11; second += 1) {
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
  }
  expect(advance).toHaveBeenCalledOnce();
  expect(advance).toHaveBeenCalledWith('episode-2', 'automatic', 'series-4k');
});

it('waits for a viewer to choose a next-episode version when the selected series copy is missing', async () => {
  const advance = vi.fn();
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'version_unavailable', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true }, requested_version_id: 'series-4k', alternatives: [{ id: 'series-1080', label: '1080p · H.264', edition_id: 'series-1', selected: false, available: true }] }));
    return new Response(JSON.stringify(path.includes('/catalog/items/') ? assessedItem : { accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="episode-1" versionID="series-4k" onAdvance={advance} onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);

  expect(await screen.findByRole('heading', { name: /choose a version for the next episode/i })).toBeVisible();
  expect(advance).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Play 1080p · H.264' }));
  await waitFor(() => expect(advance).toHaveBeenCalledWith('episode-2', 'user', 'series-1080'));
});

it('does not resolve the next episode when durable completion is rejected', async () => {
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) return new Response(JSON.stringify({ accepted: false, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'next', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true } }));
    return new Response(JSON.stringify({ stopped: true }));
  });
  render(<Player catalogID="episode-1" onAdvance={() => undefined} onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  expect(await screen.findByRole('alert')).toHaveTextContent(/completion could not be saved/i);
  expect(calls.some((path) => path.includes('/next?'))).toBe(false);
});

it('suppresses ordinary heartbeats once durable completion starts', async () => {
  let releaseEnded: (() => void) | undefined;
  const heartbeatBodies: Array<{ ended?: boolean }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) {
      heartbeatBodies.push(JSON.parse(String(init?.body)) as { ended?: boolean });
      await new Promise<void>((resolve) => { releaseEnded = resolve; });
      return new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 }));
    }
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'end_of_series' }));
    return new Response(JSON.stringify({ stopped: true }));
  });
  render(<Player catalogID="episode-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await waitFor(() => expect(releaseEnded).toBeTypeOf('function'));
  fireEvent.ended(video);
  fireEvent.pause(video);
  expect(heartbeatBodies).toEqual([{ ended: true, position_ms: 0, observation: 1 }]);
  await act(async () => { releaseEnded?.(); });
  expect(await screen.findByRole('heading', { name: /end of series/i })).toBeVisible();
});

it('waits for durable completion before cancellation stops the session', async () => {
  let releaseEnded: (() => void) | undefined;
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) {
      await new Promise<void>((resolve) => { releaseEnded = resolve; });
      return new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 }));
    }
    return new Response(JSON.stringify({ stopped: true }));
  });
  const exit = vi.fn();
  render(<Player catalogID="episode-1" onExit={exit} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await waitFor(() => expect(releaseEnded).toBeTypeOf('function'));
  fireEvent.click(screen.getByRole('button', { name: /cancel autoplay/i }));
  expect(calls.some((path) => path.endsWith('/stop'))).toBe(false);
  expect(exit).not.toHaveBeenCalled();
  await act(async () => { releaseEnded?.(); });
  await waitFor(() => expect(calls.some((path) => path.endsWith('/stop'))).toBe(true));
  expect(exit).toHaveBeenCalledOnce();
});

it('starts a fresh playback generation after autoplay navigation', async () => {
  const calls: string[] = [];
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) {
      const id = JSON.parse(String(init?.body)).catalog_id as string;
      return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: `session-${id}`, media_url: `/${id}.mp4`, heartbeat_url: `/heartbeat-${id}`, seek_url: `/seek-${id}`, stop_url: `/stop-${id}`, resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'next', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true } }));
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  let rerenderPlayer: (id: string) => void = () => undefined;
  const view = render(<Player catalogID="episode-1" onAdvance={(id) => rerenderPlayer(id)} onExit={() => undefined} />);
  rerenderPlayer = (id) => view.rerender(<Player catalogID={id} onAdvance={(nextID) => rerenderPlayer(nextID)} onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await screen.findByRole('heading', { name: /next episode/i });
  fireEvent.click(screen.getByRole('button', { name: /play now/i }));
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-2.mp4'));
  fireEvent.loadedMetadata(video);
  await waitFor(() => expect(play).toHaveBeenCalledOnce());
  const planCalls = calls.flatMap((path, index) => path.endsWith('/playback/plans') ? [index] : []);
  expect(planCalls).toHaveLength(2);
  expect(calls.findIndex((path) => path.endsWith('/session-episode-1/stop'))).toBeLessThan(planCalls[1]);
});

it('pause cancels a pending countdown and prevents a late advance', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/catalog/items/episode-1')) return new Response(JSON.stringify({ ...assessedItem, id: 'episode-1', kind: 'episode', season: 1, episode: 1 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'next', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true } }));
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  const advance = vi.fn();
  render(<Player catalogID="episode-1" onAdvance={advance} onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await screen.findByRole('heading', { name: /next episode/i });
  fireEvent.click(screen.getByRole('button', { name: /^cancel autoplay$/i }));
  expect(await screen.findByRole('heading', { name: /autoplay paused/i })).toBeVisible();
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
  expect(advance).not.toHaveBeenCalled();
});

it('pause prevents a late advance while the completed session is stopping', async () => {
  let releaseStop: (() => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'next', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true } }));
    if (path.endsWith('/stop')) await new Promise<void>((resolve) => { releaseStop = resolve; });
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  const advance = vi.fn();
  render(<Player catalogID="episode-1" onAdvance={advance} onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await screen.findByRole('heading', { name: /next episode/i });
  fireEvent.click(screen.getByRole('button', { name: /play now/i }));
  await waitFor(() => expect(releaseStop).toBeTypeOf('function'));
  fireEvent.click(screen.getByRole('button', { name: /^cancel autoplay$/i }));
  await act(async () => { releaseStop?.(); });
  expect(await screen.findByRole('heading', { name: /autoplay paused/i })).toBeVisible();
  expect(advance).not.toHaveBeenCalled();
});

it('ignores a next-episode response that completes after autoplay is cancelled', async () => {
  let resolveNext: ((response: Response) => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Promise<Response>((resolve) => { resolveNext = resolve; });
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  const advance = vi.fn();
  const exit = vi.fn();
  render(<Player catalogID="episode-1" onAdvance={advance} onExit={exit} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await waitFor(() => expect(resolveNext).toBeTypeOf('function'));
  fireEvent.click(screen.getByRole('button', { name: /cancel autoplay/i }));
  await waitFor(() => expect(exit).toHaveBeenCalledOnce());
  await act(async () => { resolveNext?.(new Response(JSON.stringify({ state: 'next', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true } }))); });
  expect(advance).not.toHaveBeenCalled();
});

it('shows an explicit end state when the current sequence has no next episode', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'end_of_series' }));
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="episode-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  expect(await screen.findByRole('heading', { name: /end of series/i })).toBeVisible();
  expect(screen.getByRole('button', { name: /return to your library/i })).toBeVisible();
});

it('shows an actionable error when next-episode resolution fails', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) return new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ error: { code: 'catalog_query_failed' } }), { status: 500 });
    return new Response(JSON.stringify({ stopped: true }));
  });
  render(<Player catalogID="episode-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  expect(await screen.findByRole('alert')).toHaveTextContent(/catalog could not be read/i);
});

it('does not advance when stopping the completed session fails', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/catalog/items/episode-1')) return new Response(JSON.stringify({ ...assessedItem, id: 'episode-1', kind: 'episode', season: 1, episode: 1 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/episode-1.mp4', heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/heartbeat')) return new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'next', episode: { id: 'episode-2', title: 'Second Signal', kind: 'episode', season: 1, episode: 2, local_only: true, playable: true } }));
    return new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 });
  });
  const advance = vi.fn();
  render(<Player catalogID="episode-1" onAdvance={advance} onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await screen.findByRole('heading', { name: /next episode/i });
  fireEvent.click(screen.getByRole('button', { name: /play now/i }));
  expect(await screen.findByRole('alert')).toHaveTextContent(/session expired/i);
  expect(advance).not.toHaveBeenCalled();
});

it('locks audio selection while completion and next-episode resolution use the current session', async () => {
  let releaseCompletion: (() => void) | undefined;
  let resolveNext: ((response: Response) => void) | undefined;
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({
      plan: { kind: 'direct', audio_stream_index: 1 }, session_id: 'session-1', media_url: '/episode-1.mp4',
      heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999,
      audio_tracks: [{ index: 1, codec: 'aac', language: 'eng' }, { index: 2, codec: 'aac', language: 'fra' }],
    }));
    if (path.endsWith('/heartbeat')) {
      await new Promise<void>((resolve) => { releaseCompletion = resolve; });
      return new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 }));
    }
    if (path.includes('/next?')) return new Promise<Response>((resolve) => { resolveNext = resolve; });
    return new Response(JSON.stringify({ stopped: true }));
  });
  render(<Player catalogID="episode-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.ended(video);
  await waitFor(() => expect(releaseCompletion).toBeTypeOf('function'));

  const audio = screen.getByRole('combobox', { name: /audio track/i });
  expect(audio).toBeDisabled();
  audio.removeAttribute('disabled');
  fireEvent.change(audio, { target: { value: 'embedded:2' } });
  expect(calls.some((path) => path.endsWith('/audio'))).toBe(false);
  audio.setAttribute('disabled', '');

  await act(async () => { releaseCompletion?.(); });
  await waitFor(() => expect(resolveNext).toBeTypeOf('function'));
  expect(audio).toBeDisabled();
  audio.removeAttribute('disabled');
  fireEvent.change(audio, { target: { value: 'embedded:2' } });
  expect(calls.some((path) => path.endsWith('/audio'))).toBe(false);

  await act(async () => { resolveNext?.(new Response(JSON.stringify({ state: 'end_of_series' }))); });
  expect(await screen.findByRole('heading', { name: /end of series/i })).toBeVisible();
});

it('discards an in-flight audio replacement when completion wins', async () => {
  let resolveAudio: ((response: Response) => void) | undefined;
  const calls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    calls.push(path);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({
      plan: { kind: 'direct', audio_stream_index: 1 }, session_id: 'session-1', media_url: '/episode-1.mp4',
      heartbeat_url: '/heartbeat-1', seek_url: '/seek-1', stop_url: '/stop-1', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999,
      audio_tracks: [{ index: 1, codec: 'aac', language: 'eng' }, { index: 2, codec: 'aac', language: 'fra' }],
    }));
    if (path.endsWith('/audio')) return new Promise<Response>((resolve) => { resolveAudio = resolve; });
    if (path.endsWith('/heartbeat')) return new Response(JSON.stringify({ accepted: true, expires_at: 9999999999 }));
    if (path.includes('/next?')) return new Response(JSON.stringify({ state: 'end_of_series' }));
    return new Response(JSON.stringify({ stopped: true }));
  });
  const view = render(<Player catalogID="episode-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/episode-1.mp4'));
  fireEvent.change(screen.getByRole('combobox', { name: /audio track/i }), { target: { value: 'embedded:2' } });
  await waitFor(() => expect(resolveAudio).toBeTypeOf('function'));

  fireEvent.ended(video);
  expect(calls.some((path) => path.endsWith('/heartbeat'))).toBe(false);
  await act(async () => { resolveAudio?.(new Response(JSON.stringify({
    plan: { kind: 'direct', audio_stream_index: 2 }, session_id: 'session-2', media_url: '/episode-1-french.mp4',
    heartbeat_url: '/heartbeat-2', seek_url: '/seek-2', stop_url: '/stop-2', resume_ms: 10_000, stream_offset_ms: 0, expires_at: 9999999999,
    audio_tracks: [{ index: 1, codec: 'aac', language: 'eng' }, { index: 2, codec: 'aac', language: 'fra' }],
  }))); });

  expect(await screen.findByRole('heading', { name: /end of series/i })).toBeVisible();
  expect(calls.some((path) => path.includes('session-1/heartbeat'))).toBe(true);
  expect(calls.some((path) => path.includes('session-1/next'))).toBe(true);
  expect(calls.some((path) => path.includes('session-2/heartbeat') || path.includes('session-2/next'))).toBe(false);
  expect(calls.some((path) => path.includes('session-2/stop'))).toBe(true);
  expect(screen.queryByRole('heading', { name: /playback stopped/i })).toBeNull();

  view.unmount();
  await waitFor(() => expect(calls.some((path) => path.includes('session-1/stop'))).toBe(true));
});

it('labels audio tracks and preserves source time when changing tracks', async () => {
  const requests: Array<{ path: string; body?: string }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    requests.push({ path, body: init?.body as string | undefined });
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({
      plan: { kind: 'direct', audio_stream_index: 1 }, session_id: 'session-1', media_url: '/original.mp4',
      heartbeat_url: '/heartbeat-1', seek_url: '/seek-1', stop_url: '/stop-1', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999,
      audio_tracks: [
        { index: 1, codec: 'aac', language: 'eng', title: 'English', default: true },
        { index: 3, codec: 'aac', title: 'Director Commentary' },
        { index: 4, codec: 'aac', language: 'not_a_language' },
      ],
    }));
    if (path.endsWith('/audio')) return new Response(JSON.stringify({
      plan: { kind: 'transcode', audio_stream_index: 3 }, session_id: 'session-2', media_url: '/selected.m3u8',
      heartbeat_url: '/heartbeat-2', seek_url: '/seek-2', stop_url: '/stop-2', resume_ms: 12_500, stream_offset_ms: 10_000, expires_at: 9999999999,
      audio_tracks: [
        { index: 1, codec: 'aac', language: 'eng', title: 'English', default: true },
        { index: 3, codec: 'aac', title: 'Director Commentary' },
        { index: 4, codec: 'aac', language: 'not_a_language' },
      ],
    }));
    return new Response(JSON.stringify({ stopped: true }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/original.mp4'));
  fireEvent.click(screen.getByText('Settings'));
  expect(screen.getByRole('option', { name: /English.*Default/i })).toBeVisible();
  expect(screen.getByRole('option', { name: /Director Commentary.*Unknown language/i })).toBeVisible();
  expect(screen.getByRole('option', { name: 'not_a_language' })).toBeVisible();
  video.currentTime = 12.5;
  fireEvent.change(screen.getByRole('combobox', { name: /audio track/i }), { target: { value: 'embedded:3' } });
  await waitFor(() => expect(hls.attached).toBe(1));
  await waitFor(() => expect(screen.getByRole('combobox', { name: /audio track/i })).toBeEnabled());
  const switchRequest = requests.find((request) => request.path.endsWith('/audio'));
  expect(switchRequest?.body).toContain('"audio_stream_index":3');
  expect(switchRequest?.body).toContain('"position_ms":12500');
  expect(switchRequest?.body).toContain('"observation":1');
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(2.5);
});

it('enables Play after a paused HLS audio replacement without a new canplay event', async () => {
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  const audioTracks = [
    { index: 1, codec: 'aac', language: 'eng', default: true },
    { index: 2, codec: 'aac', language: 'fra' },
  ];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({
      plan: { kind: 'remux', audio_stream_index: 1 }, session_id: 'session-1', media_url: '/english.m3u8',
      resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999, audio_tracks: audioTracks,
    }));
    if (path.endsWith('/audio')) return new Response(JSON.stringify({
      plan: { kind: 'remux', audio_stream_index: 2 }, session_id: 'session-2', media_url: '/french.m3u8',
      resume_ms: 1_000, stream_offset_ms: 1_000, expires_at: 9999999999, audio_tracks: audioTracks,
    }));
    return new Response(JSON.stringify({ accepted: true, stopped: true, expires_at: 9999999999 }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(hls.attached).toBe(1));
  let paused = false;
  vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.loadedMetadata(video);
  fireEvent.playing(video);
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
  fireEvent.pause(video);
  expect(screen.getByRole('button', { name: 'Play' })).toBeEnabled();

  fireEvent.change(screen.getByRole('combobox', { name: /audio track/i }), { target: { value: 'embedded:2' } });
  await waitFor(() => expect(hls.attached).toBe(2));
  await waitFor(() => expect(screen.getByRole('combobox', { name: /audio track/i })).toBeEnabled());

  expect(screen.getByRole('button', { name: 'Play' })).toBeEnabled();
});

it('keeps the active source when an audio change fails', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({
      plan: { kind: 'direct', audio_stream_index: 1 }, session_id: 'session-1', media_url: '/original.mp4',
      heartbeat_url: '/heartbeat', seek_url: '/seek', stop_url: '/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999,
      audio_tracks: [{ index: 1, codec: 'aac', language: 'eng', default: true }, { index: 2, codec: 'aac', language: 'fra' }],
    }));
    if (path.endsWith('/audio')) return new Response(JSON.stringify({ error: { code: 'playback_capacity' } }), { status: 503 });
    return new Response(JSON.stringify({ stopped: true }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/original.mp4'));
  fireEvent.change(screen.getByRole('combobox', { name: /audio track/i }), { target: { value: 'embedded:2' } });
  expect(await screen.findByRole('alert')).toHaveTextContent(/playback limit/i);
  expect(video).toHaveAttribute('src', '/original.mp4');
});

it('ignores a retired session heartbeat after audio replacement', async () => {
  let finishHeartbeat: ((value: Response) => void) | undefined;
  const original = {
    plan: { kind: 'direct', audio_stream_index: 1 }, session_id: 'session-1', media_url: '/original.mp4',
    resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999,
    audio_tracks: [{ index: 1, codec: 'aac', language: 'eng' }, { index: 2, codec: 'aac', language: 'fra' }],
  };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify(original));
    if (path.endsWith('/audio')) return new Response(JSON.stringify({ ...original, plan: { kind: 'remux', audio_stream_index: 2 }, session_id: 'session-2', media_url: '/selected.m3u8' }));
    if (path.includes('session-1/heartbeat')) return await new Promise<Response>((resolve) => { finishHeartbeat = resolve; });
    return new Response(JSON.stringify({ stopped: true }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/original.mp4'));
  fireEvent.pause(video);
  await waitFor(() => expect(finishHeartbeat).toBeTypeOf('function'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Audio track' }), { target: { value: 'embedded:2' } });
  await waitFor(() => expect(hls.attached).toBe(1));
  await act(async () => { finishHeartbeat!(new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 })); });
  expect(screen.queryByRole('heading', { name: 'Playback stopped' })).toBeNull();
  expect(screen.getByRole('combobox', { name: 'Audio track' })).toHaveValue('embedded:2');
});

it('switches native subtitle tracks without replacing the media session', async () => {
  const requests: Array<{ path: string; body?: string }> = [];
  const initial = {
    plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4',
    resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999,
    subtitle_url: '/api/v1/playback/sessions/session-1/subtitle.vtt?index=2&external=false',
    selected_subtitle: { index: 2, codec: 'subrip', language: 'eng', default: true },
    subtitle_tracks: [
      { index: 2, codec: 'subrip', language: 'eng', default: true },
      { index: 3, codec: 'webvtt', language: 'fra', forced: true, sdh: true, external: true },
    ],
  };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    requests.push({ path, body: init?.body as string | undefined });
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify(initial));
    if (path.endsWith('/subtitle')) return new Response(JSON.stringify({ ...initial, subtitle_url: undefined, selected_subtitle: undefined }));
    return new Response(JSON.stringify({ stopped: true }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/film.mp4'));
  expect(video.querySelector('track')).toHaveAttribute('src', initial.subtitle_url);
  expect(screen.getByRole('combobox', { name: 'Subtitles' })).toHaveValue('embedded:2');

  fireEvent.change(screen.getByRole('combobox', { name: 'Subtitles' }), { target: { value: 'off' } });

  await waitFor(() => expect(requests.some(({ path }) => path.endsWith('/subtitle'))).toBe(true));
  expect(JSON.parse(requests.find(({ path }) => path.endsWith('/subtitle'))!.body!)).toEqual({ mode: 'off' });
  expect(video).toHaveAttribute('src', '/film.mp4');
  expect(video.querySelector('track')).toBeNull();
  expect(requests.filter(({ path }) => path.endsWith('/playback/plans'))).toHaveLength(1);
});

it('ignores a delayed subtitle failure from a retired session', async () => {
  let finishSubtitle: ((value: Response) => void) | undefined;
  const subtitle = { index: 2, codec: 'subrip', language: 'eng', default: true };
  const plan = (session: string) => ({
    plan: { kind: 'transcode' }, session_id: session, media_url: `/${session}/manifest.m3u8`,
    resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999,
    subtitle_url: `/api/v1/playback/sessions/${session}/subtitle.vtt?index=2&external=false`,
    selected_subtitle: subtitle, subtitle_tracks: [subtitle],
  });
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify(plan('session-1')));
    if (path.endsWith('/subtitle')) return await new Promise<Response>((resolve) => { finishSubtitle = resolve; });
    if (path.endsWith('/seek')) return new Response(JSON.stringify(plan('session-2')));
    return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(hls.attached).toBe(1));
  fireEvent.change(screen.getByRole('combobox', { name: 'Subtitles' }), { target: { value: 'off' } });
  await waitFor(() => expect(finishSubtitle).toBeTypeOf('function'));
  video.currentTime = 1;
  fireEvent.seeked(video);
  await waitFor(() => expect(video.querySelector('track')).toHaveAttribute('src', expect.stringContaining('session-2')));
  await act(async () => { finishSubtitle!(new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 })); });

  expect(screen.queryByRole('alert')).toBeNull();
  expect(screen.getByRole('combobox', { name: 'Subtitles' })).toBeEnabled();
  expect(screen.getByRole('combobox', { name: 'Subtitles' })).toHaveValue('embedded:2');
});

it('reattaches source-relative captions across repeated HLS seeks', async () => {
  const seekPositions: number[] = [];
  const subtitle = { index: 2, codec: 'subrip', language: 'eng' };
  const plan = (session: string, offset: number, resume: number) => ({
    plan: { kind: 'transcode' }, session_id: session, media_url: `/${session}/manifest.m3u8`,
    resume_ms: resume, stream_offset_ms: offset, expires_at: 9999999999,
    subtitle_url: `/api/v1/playback/sessions/${session}/subtitle.vtt?index=2&external=false`,
    selected_subtitle: subtitle, subtitle_tracks: [subtitle],
  });
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify(plan('session-1', 2_000, 2_000)));
    if (path.endsWith('/seek')) {
      const position = JSON.parse(init?.body as string).position_ms as number;
      seekPositions.push(position);
      return new Response(JSON.stringify(position === 7_000 ? plan('session-2', 7_000, 7_000) : plan('session-2', 7_000, position)));
    }
    return new Response(JSON.stringify({ stopped: true, expires_at: 9999999999 }));
  });

  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video.querySelector('track')).toHaveAttribute('src', expect.stringContaining('session-1')));
  video.currentTime = 5;
  fireEvent.seeked(video);
  await waitFor(() => expect(video.querySelector('track')).toHaveAttribute('src', expect.stringContaining('session-2')));
  fireEvent.seeked(video);
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(0);

  video.currentTime = 2;
  fireEvent.seeked(video);
  await waitFor(() => expect(seekPositions).toEqual([7_000, 9_000]));
  expect(video.querySelector('track')).toHaveAttribute('src', expect.stringContaining('session-2'));
});


it('starts ordinary user-requested playback once metadata is ready without a second Play click', async () => {
  const start = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-auto', media_url: '/auto.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
  });
  const { container } = render(<Player catalogID="film-1" onExit={() => undefined} />);
  const element = container.querySelector('video')!;
  await waitFor(() => expect(element.getAttribute('src')).toBe('/auto.mp4'));
  fireEvent.loadedMetadata(element);
  await waitFor(() => expect(start).toHaveBeenCalledTimes(1));
  fireEvent.loadedMetadata(element);
  expect(start).toHaveBeenCalledTimes(1);
});

it('uses the bundled HLS engine even when the browser also advertises native HLS', async () => {
 vi.stubGlobal('MediaSource',{isTypeSupported:()=>true});
 vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
 vi.spyOn(globalThis,'fetch').mockImplementation(async input=> {
  const path=String(input);
  if(path.includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
  if(path.endsWith('/playback/plans')) return new Response(JSON.stringify({plan:{kind:'transcode'},session_id:'engine-session',media_url:'/manifest.m3u8',resume_ms:0,stream_offset_ms:0,expires_at:9999999999}));
  return new Response(JSON.stringify({accepted:true}));
 });
 render(<Player catalogID="film-1" onExit={()=>{}} />);
 try { await waitFor(()=>expect(hls.attached).toBe(1)); }
 finally { vi.unstubAllGlobals(); }
});

it('accumulates fifteen-second direct seek steps before the browser emits timeupdate', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'direct-step', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video=document.querySelector('video') as HTMLVideoElement;
  await waitFor(()=>expect(video).toHaveAttribute('src','/film.mp4'));
  fireEvent.loadedMetadata(video); fireEvent.playing(video);
  const forward=screen.getByRole('button',{name:'Forward 15 seconds'});
  await act(async()=>{fireEvent.click(forward);});
  expect(video.currentTime).toBe(15);
  await act(async()=>{fireEvent.click(forward);});
  expect(video.currentTime).toBe(30);
});

it('preserves native video fullscreen through a source-changing seek', async () => {
  vi.stubGlobal('MediaSource', undefined);
  vi.stubGlobal('ManagedMediaSource', undefined);
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }));
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/stream-1.m3u8'));
  fireEvent.loadedMetadata(video);
  fireEvent.canPlay(video);
  fireEvent.playing(video);
  let displayingFullscreen = true;
  Object.defineProperty(video, 'webkitDisplayingFullscreen', { configurable: true, get: () => displayingFullscreen });
  vi.mocked(HTMLMediaElement.prototype.load).mockImplementation(() => { displayingFullscreen = false; });

  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));

  await waitFor(() => expect(video).toHaveAttribute('src', '/stream-2.m3u8'));
  expect((video as HTMLVideoElement & { webkitDisplayingFullscreen: boolean }).webkitDisplayingFullscreen).toBe(true);
});

it('preserves native video fullscreen while an expired seek recovers', async () => {
  vi.stubGlobal('MediaSource', undefined);
  vi.stubGlobal('ManagedMediaSource', undefined);
  vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
  let plans = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: `session-${plans}`, media_url: `/stream-${plans}.m3u8`, resume_ms: plans === 1 ? 0 : 15_000, stream_offset_ms: plans === 1 ? 0 : 15_000, expires_at: 9999999999 }));
    }
    if (path.endsWith('/seek')) return new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 });
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/stream-1.m3u8'));
  fireEvent.loadedMetadata(video);
  fireEvent.canPlay(video);
  fireEvent.playing(video);
  let displayingFullscreen = true;
  Object.defineProperty(video, 'webkitDisplayingFullscreen', { configurable: true, get: () => displayingFullscreen });
  vi.mocked(HTMLMediaElement.prototype.load).mockImplementation(() => { displayingFullscreen = false; });

  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));

  await waitFor(() => expect(video).toHaveAttribute('src', '/stream-2.m3u8'));
  expect((video as HTMLVideoElement & { webkitDisplayingFullscreen: boolean }).webkitDisplayingFullscreen).toBe(true);
});

it('preserves the active media pipeline through a bundled HLS replacement seek', async () => {
  vi.stubGlobal('MediaSource', { isTypeSupported: () => true });
  hls.bufferedTransfer = true;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }));
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(hls.attached).toBe(1));
  fireEvent.loadedMetadata(video);
  fireEvent.canPlay(video);
  fireEvent.playing(video);
  Object.defineProperty(video, 'paused', { configurable: true, value: false });
  video.currentTime = 5;
  vi.mocked(HTMLMediaElement.prototype.play).mockClear();
  let displayingFullscreen = true;
  Object.defineProperty(video, 'webkitDisplayingFullscreen', { configurable: true, get: () => displayingFullscreen });
  vi.mocked(HTMLMediaElement.prototype.load).mockImplementation(() => { displayingFullscreen = false; });

  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));

  await waitFor(() => expect(hls.attached).toBe(2));
  expect(hls.removed).toBe(1);
  expect(video.currentTime).toBe(0);
  expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
  expect((video as HTMLVideoElement & { webkitDisplayingFullscreen: boolean }).webkitDisplayingFullscreen).toBe(true);
  fireEvent.seeked(video);
  video.currentTime = 3;
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(3);
  expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
});

it('shows and coalesces the latest seek intent while stale playback is paused', async () => {
  const seekRequests: Array<{ position: number; resolve: (response: Response) => void }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) {
      const position = JSON.parse(String(init?.body)).position_ms as number;
      return new Promise<Response>((resolve) => { seekRequests.push({ position, resolve }); });
    }
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(hls.attached).toBe(1));
  let paused = false;
  const pause = vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.playing(video);

  const forward = screen.getByRole('button', { name: 'Forward 15 seconds' });
  fireEvent.click(forward);
  await waitFor(() => expect(seekRequests).toHaveLength(1));
  expect(pause).toHaveBeenCalled();
  expect(screen.getByRole('slider', { name: 'Seek' })).toHaveAttribute('aria-valuetext', '0:15 of 2:00');
  expect(document.querySelector('.player-loading')?.textContent).toBe('');
  expect(document.querySelector('.player-status')).toHaveTextContent(/^Loading$/);
  fireEvent.click(forward);

  await act(async () => { seekRequests[0].resolve(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(seekRequests).toHaveLength(2));
  expect(seekRequests[1].position).toBe(30_000);
  expect(hls.attached).toBe(1);

  await act(async () => { seekRequests[1].resolve(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-3', media_url: '/stream-3.m3u8', resume_ms: 30_000, stream_offset_ms: 30_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(hls.attached).toBe(2));
  expect(document.querySelector('.player-loading')?.textContent).toBe('');
  expect(document.querySelector('.player-status')).toHaveTextContent(/^Loading$/);
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(0);
  expect(play).toHaveBeenCalledOnce();
});

it('keeps the last usable replacement when the latest coalesced seek fails', async () => {
  const seekRequests: Array<{ position: number; resolve: (response: Response) => void; reject: (error: Error) => void }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Promise<Response>((resolve, reject) => {
      seekRequests.push({ position: JSON.parse(String(init?.body)).position_ms, resolve, reject });
    });
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  let paused = false;
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.playing(video);
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));

  await waitFor(() => expect(seekRequests).toHaveLength(1));
  await act(async () => { seekRequests[0].resolve(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(seekRequests).toHaveLength(2));
  await act(async () => { seekRequests[1].reject(new Error('replacement failed')); });

  await waitFor(() => expect(hls.attached).toBe(2));
  fireEvent.loadedMetadata(video);
  fireEvent.canPlay(video);
  expect(screen.queryByText('Seeking…')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Forward 15 seconds' })).toBeEnabled();
  expect(play).toHaveBeenCalledOnce();
});

it('restores an acknowledged retained position when the latest coalesced seek fails', async () => {
  const seekRequests: Array<{ position: number; resolve: (response: Response) => void; reject: (error: Error) => void }> = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Promise<Response>((resolve, reject) => {
      seekRequests.push({ position: JSON.parse(String(init?.body)).position_ms, resolve, reject });
    });
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  let paused = false;
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.playing(video);
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));

  await waitFor(() => expect(seekRequests).toHaveLength(1));
  await act(async () => { seekRequests[0].resolve(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 15_000, stream_offset_ms: 0, expires_at: 9999999999 }))); });
  await waitFor(() => expect(seekRequests).toHaveLength(2));
  await act(async () => { seekRequests[1].reject(new Error('latest seek failed')); });

  await waitFor(() => expect(video.currentTime).toBe(15));
  expect(hls.attached).toBe(1);
  expect(play).toHaveBeenCalledOnce();
  expect(screen.getByRole('slider', { name: 'Seek' })).toHaveAttribute('aria-valuetext', '0:15 of 2:00');
});

it('recovers an expired latest seek without attaching the superseded session', async () => {
  const seekRequests: Array<{ position: number; resolve: (response: Response) => void }> = [];
  const planIntents: string[] = [];
  let resolveRecovery: ((response: Response) => void) | undefined;
  let planRequests = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) {
      planRequests += 1;
      planIntents.push(JSON.parse(String(init?.body)).continue_watching_intent);
      const recovered = planRequests > 1;
      if (recovered) return new Promise<Response>((resolve) => { resolveRecovery = resolve; });
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/seek')) return new Promise<Response>((resolve) => { seekRequests.push({ position: JSON.parse(String(init?.body)).position_ms, resolve }); });
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  let paused = false;
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.playing(video);
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));

  await waitFor(() => expect(seekRequests).toHaveLength(1));
  await act(async () => { seekRequests[0].resolve(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(seekRequests).toHaveLength(2));
  await act(async () => { seekRequests[1].resolve(new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 })); });

  await waitFor(() => expect(planRequests).toBe(2));
  expect(play).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
  expect(screen.getByRole('button', { name: 'Play' })).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Play' }));
  expect(screen.getByRole('button', { name: 'Pause' })).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));
  expect(screen.getByRole('slider', { name: 'Seek' })).toHaveAttribute('aria-valuetext', '0:45 of 2:00');
  await act(async () => { resolveRecovery?.(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-3', media_url: '/stream-3.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(seekRequests).toHaveLength(3));
  expect(hls.attached).toBe(1);
  expect(seekRequests[2].position).toBe(45_000);
  await act(async () => { seekRequests[2].resolve(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-3', media_url: '/stream-3.m3u8', resume_ms: 45_000, stream_offset_ms: 15_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(hls.attached).toBe(2));
  await waitFor(() => expect(screen.queryByText('Seeking…')).not.toBeInTheDocument());
  fireEvent.loadedMetadata(video);
  expect(planIntents).toEqual(['user', 'recovery']);
  expect(play).toHaveBeenCalledOnce();
});

it('does not replay the cleared source when expired-seek recovery fails', async () => {
  let planRequests = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) {
      planRequests += 1;
      if (planRequests > 1) return new Response(JSON.stringify({ error: { code: 'playback_unsupported' } }), { status: 422 });
      return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    }
    if (path.endsWith('/seek')) return new Response(JSON.stringify({ error: { code: 'playback_session_invalid' } }), { status: 403 });
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  let paused = false;
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.playing(video);
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));

  expect(await screen.findByRole('alert')).toHaveTextContent(/not compatible/i);
  expect(play).not.toHaveBeenCalled();
  expect(screen.queryByText('Seeking…')).not.toBeInTheDocument();
});

it('honors pause and speed changes made while a replacement seek is pending', async () => {
  let resolveSeek: ((response: Response) => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Promise<Response>((resolve) => { resolveSeek = resolve; });
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(hls.attached).toBe(1));
  let paused = false;
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.playing(video);
  fireEvent.click(screen.getByText('Settings'));
  fireEvent.change(screen.getByRole('combobox', { name: 'Playback speed' }), { target: { value: '1.5' } });
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));
  await waitFor(() => expect(resolveSeek).toBeTypeOf('function'));
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }));

  await act(async () => { resolveSeek?.(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(hls.attached).toBe(2));
  expect(video.playbackRate).toBe(1.5);
  fireEvent.loadedMetadata(video);
  expect(video.playbackRate).toBe(1.5);
  expect(screen.getByRole('combobox', { name: 'Playback speed' })).toHaveValue('1.5');
  expect(play).not.toHaveBeenCalled();
});

it('honors a remote pause while a replacement seek is pending', async () => {
  let resolveSeek: ((response: Response) => void) | undefined;
  const listen = vi.spyOn(screenCoordinator, 'onCommand');
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ ...assessedItem, duration_ms: 120000 }));
    if (path.endsWith('/playback/plans')) return new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-1', media_url: '/stream-1.m3u8', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (path.endsWith('/seek')) return new Promise<Response>((resolve) => { resolveSeek = resolve; });
    return new Response(JSON.stringify({ stopped: true, accepted: true, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  await waitFor(() => expect(hls.attached).toBe(1));
  const video = document.querySelector('video') as HTMLVideoElement;
  let paused = false;
  vi.spyOn(video, 'pause').mockImplementation(() => { paused = true; });
  const play = vi.spyOn(video, 'play').mockImplementation(async () => { paused = false; });
  Object.defineProperty(video, 'paused', { configurable: true, get: () => paused });
  fireEvent.playing(video);
  fireEvent.click(screen.getByRole('button', { name: 'Forward 15 seconds' }));
  await waitFor(() => expect(resolveSeek).toBeTypeOf('function'));
  act(() => { listen.mock.calls[0][0]({ version: 1, type: 'pause' }); });

  await act(async () => { resolveSeek?.(new Response(JSON.stringify({ plan: { kind: 'transcode' }, session_id: 'session-2', media_url: '/stream-2.m3u8', resume_ms: 15_000, stream_offset_ms: 15_000, expires_at: 9999999999 }))); });
  await waitFor(() => expect(hls.attached).toBe(2));
  fireEvent.loadedMetadata(video);
  expect(screen.getByRole('button', { name: 'Play' })).toBeVisible();
  expect(play).not.toHaveBeenCalled();
});

it('auto-hides pointer-focused controls but preserves keyboard-focused controls', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).includes('/catalog/items/')) return new Response(JSON.stringify(assessedItem));
    return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'session-1', media_url: '/film.mp4', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 }));
  });
  render(<Player catalogID="film-1" onExit={() => undefined} />);
  const stage = document.querySelector('main.player') as HTMLElement;
  const video = document.querySelector('video') as HTMLVideoElement;
  await waitFor(() => expect(video).toHaveAttribute('src', '/film.mp4'));
  fireEvent.playing(video);
  const mute = screen.getByRole('button', { name: 'Mute' });
  fireEvent.pointerDown(mute);
  mute.focus();
  fireEvent.pointerMove(stage);
  await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
  expect(stage).toHaveClass('controls-hidden');

  fireEvent.pointerMove(stage);
  Object.defineProperty(video, 'paused', { configurable: true, value: false });
  fireEvent.waiting(video);
  await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
  expect(stage).not.toHaveClass('controls-hidden');

  fireEvent.timeUpdate(video);
  fireEvent.pointerMove(stage);
  await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
  expect(stage).toHaveClass('controls-hidden');

  fireEvent.pointerMove(stage);
  fireEvent.pause(video);
  await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
  expect(stage).not.toHaveClass('controls-hidden');

  fireEvent.playing(video);
  fireEvent.keyDown(stage, { key: 'Tab' });
  mute.focus();
  await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
  expect(stage).not.toHaveClass('controls-hidden');
});
