import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { Player } from './Player';
import { screenCoordinator } from '../screenCoordinator/runtime';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it('sends an absolute remote seek before the compatibility stream offset to the server', async () => {
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
  const listen = vi.spyOn(screenCoordinator, 'onCommand');
	const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
		const path = String(input);
		if (path.includes('/catalog/items/')) return new Response(JSON.stringify({ id: 'film' }));
    if (path.endsWith('/plans') || path.endsWith('/seek')) return new Response(JSON.stringify({
      plan: { kind: 'remux' }, session_id: 'remote', media_url: path.endsWith('/seek') ? '/new.m3u8' : '/old.m3u8',
      heartbeat_url: '/heartbeat', resume_ms: path.endsWith('/seek') ? JSON.parse(String(init?.body)).position_ms : 20_000,
      stream_offset_ms: path.endsWith('/seek') ? 0 : 20_000, expires_at: 9999999999,
    }));
    if (path.endsWith('/heartbeat') || path.endsWith('/stop')) return new Response('{}');
    throw new Error(`Unexpected request ${path}`);
  });
  const { container } = render(<Player catalogID="film" onExit={() => undefined} />);
  await waitFor(() => expect(container.querySelector('video')).toHaveAttribute('src', '/old.m3u8'));
  await act(async () => { listen.mock.calls[0][0]({ version: 1, type: 'seek', position_ms: 1000 }); });
  await waitFor(() => expect(fetcher.mock.calls.some(([path, init]) => String(path).endsWith('/seek') && JSON.parse(String(init?.body)).position_ms === 1000)).toBe(true));
  await waitFor(() => expect(container.querySelector('video')).toHaveAttribute('src', '/new.m3u8'));
});

it('applies the remote start position and attempts playback after metadata is ready', async () => {
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
	vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
		if (String(input).includes('/catalog/items/')) return new Response(JSON.stringify({ id: 'film' }));
		if (String(input).endsWith('/plans')) return new Response(JSON.stringify({ plan: { kind: 'direct' }, session_id: 'remote', media_url: '/media.mp4', heartbeat_url: '/heartbeat', resume_ms: 50_000, stream_offset_ms: 0, expires_at: 9999999999 }));
    if (String(input).endsWith('/heartbeat') || String(input).endsWith('/stop')) return new Response('{}');
    throw new Error(`Unexpected request ${String(input)}`);
  });
  const { container } = render(<Player catalogID="film" startPositionMS={1200} onExit={() => undefined} />);
  const video = container.querySelector('video')!;
  await waitFor(() => expect(video).toHaveAttribute('src', '/media.mp4'));
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(1.2);
  await waitFor(() => expect(play).toHaveBeenCalledOnce());
});
