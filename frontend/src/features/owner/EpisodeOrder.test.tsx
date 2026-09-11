import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EpisodeOrder } from './EpisodeOrder';

const detail = {
  series_id: 'series-1', title: 'Night Relay', order: 'aired' as const, revision: 4, needs_repair: false,
  entries: [{ catalog_id: 'episode-1', title: 'Signal one', season: 1, episode: 1, episode_end: 2, mapping: { position: 1, end_position: 2, season: 1, episode: 1, episode_end: 2, special: false } }],
};

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('EpisodeOrder', () => {
  it('loads only after explicit discovery, previews a provider group, and saves edited numeric spans', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/episode-orders?q=night')) return new Response(JSON.stringify({ series: [{ id: 'series-1', title: 'Night Relay' }], total: 1 }));
      if (path.endsWith('/episode-orders/series-1/groups')) return new Response(JSON.stringify({ groups: [{ id: 'dvd', name: 'DVD order', order: 'dvd' }] }));
      if (path.endsWith('/episode-orders/series-1/preview')) return new Response(JSON.stringify({ ...detail, order: 'dvd' }));
      if (path.endsWith('/episode-orders/series-1') && !init?.method) return new Response(JSON.stringify(detail));
      if (path.endsWith('/episode-orders/series-1') && init?.method === 'PUT') return new Response(JSON.stringify({ ...detail, revision: 5 }));
      return new Response(JSON.stringify({ error: { code: 'request_failed' } }), { status: 500 });
    });
    render(<EpisodeOrder />);
    expect(fetcher).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /edit episode order/i }));
    fireEvent.change(await screen.findByLabelText('Find a series'), { target: { value: 'night' } });
    fireEvent.click(screen.getByRole('button', { name: 'Find series' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Night Relay' }));
    expect(await screen.findByLabelText('Order')).toHaveValue('aired');
    fireEvent.change(screen.getByLabelText('Order'), { target: { value: 'dvd' } });
    fireEvent.click(screen.getByRole('button', { name: /load provider groups/i }));
    fireEvent.change(await screen.findByLabelText('Provider group'), { target: { value: 'dvd' } });
    fireEvent.click(screen.getByRole('button', { name: /preview provider mapping/i }));
    await waitFor(() => expect(screen.getByLabelText('Order')).toHaveValue('dvd'));
    fireEvent.change(screen.getByLabelText('Position for Signal one'), { target: { value: '3' } });
    fireEvent.click(screen.getByRole('button', { name: /save episode order/i }));
    await screen.findByText(/episode order saved/i);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/episode-orders/series-1', expect.objectContaining({ method: 'PUT', body: expect.stringContaining('"position":3') }));
  });

  it('keeps the editable draft after a conflict and cancels without saving', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/episode-orders?q=night')) return new Response(JSON.stringify({ series: [{ id: 'series-1', title: 'Night Relay' }], total: 1 }));
      if (path.endsWith('/episode-orders/series-1') && !init?.method) return new Response(JSON.stringify(detail));
      if (path.endsWith('/episode-orders/series-1') && init?.method === 'PUT') return new Response(JSON.stringify({ error: { code: 'episode_order_conflict' } }), { status: 409 });
      return new Response(JSON.stringify({ error: { code: 'request_failed' } }), { status: 500 });
    });
    render(<EpisodeOrder />);
    fireEvent.click(screen.getByRole('button', { name: /edit episode order/i }));
    fireEvent.change(await screen.findByLabelText('Find a series'), { target: { value: 'night' } });
    fireEvent.click(screen.getByRole('button', { name: 'Find series' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Night Relay' }));
    const position = await screen.findByLabelText('Position for Signal one');
    fireEvent.change(position, { target: { value: '7' } });
    fireEvent.click(screen.getByRole('button', { name: /save episode order/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/changed elsewhere/i);
    expect(position).toHaveValue(7);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel order edit' }));
    expect(screen.queryByLabelText('Position for Signal one')).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  it('loads the next bounded discovery page without duplicating existing series', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('offset=50')) return new Response(JSON.stringify({ series: [{ id: 'series-2', title: 'Night Relay: Later' }], total: 51 }));
      if (path.includes('/episode-orders?q=night')) return new Response(JSON.stringify({ series: [{ id: 'series-1', title: 'Night Relay' }], total: 51, next_offset: 50 }));
      return new Response(JSON.stringify({ error: { code: 'request_failed' } }), { status: 500 });
    });
    render(<EpisodeOrder />);
    fireEvent.click(screen.getByRole('button', { name: /edit episode order/i }));
    fireEvent.change(screen.getByLabelText('Find a series'), { target: { value: 'night' } });
    fireEvent.click(screen.getByRole('button', { name: 'Find series' }));
    await screen.findByRole('button', { name: 'Night Relay' });
    fireEvent.click(screen.getByRole('button', { name: 'Load more matching series' }));
    expect(await screen.findByRole('button', { name: 'Night Relay: Later' })).toBeVisible();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/episode-orders?q=night&offset=50&limit=50', expect.anything());
  });

  it('locks conflicting edits during a save and ignores a response after cancellation', async () => {
    let resolveSave: ((response: Response) => void) | undefined;
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/episode-orders?q=night')) return new Response(JSON.stringify({ series: [{ id: 'series-1', title: 'Night Relay' }], total: 1 }));
      if (path.endsWith('/episode-orders/series-1') && !init?.method) return new Response(JSON.stringify(detail));
      if (path.endsWith('/episode-orders/series-1') && init?.method === 'PUT') return new Promise((resolve) => { resolveSave = resolve; });
      return new Response(JSON.stringify({ error: { code: 'request_failed' } }), { status: 500 });
    });
    render(<EpisodeOrder />);
    fireEvent.click(screen.getByRole('button', { name: /edit episode order/i }));
    fireEvent.change(screen.getByLabelText('Find a series'), { target: { value: 'night' } });
    fireEvent.click(screen.getByRole('button', { name: 'Find series' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Night Relay' }));
    const position = await screen.findByLabelText('Position for Signal one');
    fireEvent.click(screen.getByRole('button', { name: 'Save episode order' }));
    expect(position).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel order edit' }));
    resolveSave?.(new Response(JSON.stringify({ ...detail, revision: 5 })));
    await waitFor(() => expect(screen.queryByText('Episode order saved.')).not.toBeInTheDocument());
    expect(screen.getByRole('button', { name: /edit episode order/i })).toBeVisible();
  });
});
