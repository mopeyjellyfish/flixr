import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Browse } from './Browse';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe('browse', () => {
  it('virtualizes a 10k logical rail and restores focus after details close', async () => {
    const items = Array.from({ length: 10_000 }, (_, index) => ({ id: `${index}`, title: `Film ${index}`, kind: 'film', local_only: index === 0 }));
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ items, total: 10_000, next: 48 })));
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')); };
    render(<Browse onExit={() => undefined} />);
    const first = await screen.findByTestId('card-0');
    expect(document.querySelectorAll('[data-card]').length).toBeLessThan(30);
    fireEvent.click(first);
    await waitFor(() => expect(screen.getByRole('dialog')).toBeVisible());
    fireEvent.click(screen.getByRole('button', { name: /close details/i }));
    await waitFor(() => expect(first).toHaveFocus());
  });

  it('opens the focal title through a semantic detail action', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ items: [{ id: 'film-1', title: 'Signal', kind: 'film', year: 2024, synopsis: 'A local story.', local_only: true, backdrop: '/artwork/sky' }] })));
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    render(<Browse onExit={() => undefined} />);
    expect(await screen.findByRole('heading', { name: 'Signal' })).toBeInTheDocument();
    expect(screen.getAllByText('2024').length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: /view details for signal/i }));
    await expect(screen.findByRole('dialog')).resolves.toBeVisible();
  });

  it('fetches and renders ordered series seasons and path-free episode metadata', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/catalog/series/series-1')) return new Response(JSON.stringify({ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, seasons: [{ id: 's2', number: 2, episodes: [{ id: 'episode-2', title: 'Second', kind: 'episode', season: 2, episode: 2, local_only: true }] }, { id: 's1', number: 1, episodes: [{ id: 'episode-1', title: 'First', kind: 'episode', season: 1, episode: 1, local_only: false }] }] }));
      return new Response(JSON.stringify({ items: [{ id: 'series-1', title: 'Signal', kind: 'series', local_only: false }] }));
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    render(<Browse onExit={() => undefined} />);
    fireEvent.click(await screen.findByTestId('card-series-1'));
    expect(await screen.findByText('Season 1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /S1 E1 First/i })).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/catalog/series/series-1', expect.anything());
  });

  it('closes an open detail route when the dialog is cancelled', async () => {
    const navigate = vi.fn();
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ items: [{ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }] })));
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')); };
    render(<Browse onExit={() => undefined} onNavigate={navigate} />);
    fireEvent.click(await screen.findByTestId('card-film-1'));
    const dialog = await screen.findByRole('dialog');
    fireEvent(dialog, new Event('cancel', { cancelable: true }));
    await waitFor(() => expect(navigate).toHaveBeenLastCalledWith('/home'));
  });

  it('renders an empty persistent catalog response without crashing', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ items: null, total: 0, next: null })));
    render(<Browse onExit={() => undefined} />);
    expect(await screen.findByRole('heading', { name: /your library is waiting/i })).toBeInTheDocument();
  });

  it('retries the failed request and only fetches non-empty searches', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('{}', { status: 500 })).mockResolvedValueOnce(new Response(JSON.stringify({ items: [] })));
    render(<Browse onExit={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /try again/i }));
    await screen.findByRole('heading', { name: /your library is waiting/i });
    expect(fetcher).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await screen.findByRole('heading', { name: /no matching titles/i });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});
