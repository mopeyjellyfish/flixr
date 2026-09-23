import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { api } from '../../api/client';
import type { PlaybackActivityPage } from '../../core/api';
import { PlaybackActivity } from './Playback';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const page = {
  capacity: { max_generations: 2, starting: 0, active_sessions: 1, generation_bytes: 1024, global_bytes: 2048, cache_bytes: 512, generations: [] },
  sessions: [{ owner_handle: 'owner-safe', catalog_id: 'film-1', title: 'Arrival', kind: 'direct' as const, reason: 'Original media is compatible', audio_stream_index: 2, quality_mode: 'original' as const, width: 1920, height: 1080, started_at: 1, device: 'Unknown device' }],
};

it('shows safe direct playback details and stops only after confirmation', async () => {
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    if (String(input).includes('/stop') && init?.method === 'POST') return new Response(JSON.stringify({ stopped: true }));
    return new Response(JSON.stringify(fetcher.mock.calls.some(([, options]) => options?.method === 'POST') ? { ...page, sessions: [], capacity: { ...page.capacity, active_sessions: 0 } } : page));
  });
  const confirm = vi.fn().mockResolvedValue(true);
  render(<PlaybackActivity confirm={confirm} />);

  expect(await screen.findByRole('heading', { name: /active playback/i })).toBeVisible();
  expect(screen.getByText('Arrival')).toBeVisible();
  expect(screen.getByText(/Unknown device/)).toBeVisible();
  expect(document.body).not.toHaveTextContent('owner-safe');
  fireEvent.click(screen.getByRole('button', { name: /stop arrival/i }));
  await vi.waitFor(() => expect(confirm).toHaveBeenCalled());
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/playback/sessions/owner-safe/stop', expect.objectContaining({ method: 'POST' })));
  expect(await screen.findByText(/no playback sessions are active/i)).toBeVisible();
});

it('appends a page and ignores an older refresh response', async () => {
  let resolveOld!: (page: PlaybackActivityPage) => void;
  const old = new Promise<PlaybackActivityPage>((resolve) => { resolveOld = resolve; });
  const activity = vi.spyOn(api, 'playbackActivity');
  activity.mockImplementationOnce(() => old);
  activity.mockResolvedValueOnce({ ...page, next_cursor: 'next' });
  activity.mockResolvedValueOnce({ ...page, sessions: [{ ...page.sessions[0], owner_handle: 'second', title: 'Second' }] });

  const { rerender } = render(<PlaybackActivity confirm={vi.fn().mockResolvedValue(false)} />);
  rerender(<PlaybackActivity confirm={vi.fn().mockResolvedValue(false)} refreshKey={1} />);
  expect(await screen.findByText('Arrival')).toBeVisible();

  await act(async () => {
    resolveOld({ ...page, sessions: [{ ...page.sessions[0], title: 'Old response' }] });
    await old;
  });
  expect(screen.getByText('Arrival')).toBeVisible();
  expect(screen.queryByText('Old response')).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: /load more/i }));
  expect(await screen.findByText('Second')).toBeVisible();
  const items = within(screen.getByRole('list', { name: /active playback sessions/i }));
  expect(items.getByText('Arrival')).toBeVisible();
  expect(items.queryByText('Old response')).not.toBeInTheDocument();
  expect(items.getAllByRole('listitem')).toHaveLength(2);
});

it('does not let load more supersede the full refresh after stop', async () => {
  let resolveRefresh!: (response: Response) => void;
  const refresh = new Promise<Response>((resolve) => { resolveRefresh = resolve; });
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/stop') && init?.method === 'POST') return new Response(JSON.stringify({ stopped: true }));
    if (path.includes('cursor=next')) return new Response(JSON.stringify({ ...page, sessions: [{ ...page.sessions[0], owner_handle: 'second', title: 'Second' }] }));
    if (fetcher.mock.calls.some(([, options]) => options?.method === 'POST')) return refresh;
    return new Response(JSON.stringify({ ...page, next_cursor: 'next' }));
  });
  render(<PlaybackActivity confirm={vi.fn().mockResolvedValue(true)} />);
  expect(await screen.findByText('Arrival')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: /stop arrival/i }));
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/playback/sessions/owner-safe/stop', expect.objectContaining({ method: 'POST' })));
  const loadMore = screen.getByRole('button', { name: /load more/i });
  expect(loadMore).toBeDisabled();
  fireEvent.click(loadMore);
  expect(fetcher.mock.calls.some(([input]) => String(input).includes('cursor=next'))).toBe(false);
  resolveRefresh(new Response(JSON.stringify({ ...page, sessions: [], next_cursor: undefined, capacity: { ...page.capacity, active_sessions: 0 } })));
  expect(await screen.findByText(/no playback sessions are active/i)).toBeVisible();
  expect(screen.queryByText('Arrival')).not.toBeInTheDocument();
});
