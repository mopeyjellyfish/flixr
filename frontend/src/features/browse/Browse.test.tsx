import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Browse } from './Browse';

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/home'); });
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

  it('renders persistent destinations and the server ordered Home rows', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [{ id: 'film-1', title: 'Arrival', kind: 'film', local_only: false, playable: true, listed: false }] }, { name: 'My List', items: [] }, { name: 'Drama', items: [] }] })));
    render(<Browse onExit={() => undefined} />);
		expect(await screen.findByRole('navigation', { name: 'Main navigation' })).toHaveTextContent('HomeMoviesTV');
		expect(screen.getAllByRole('heading', { level: 2 }).map((heading) => heading.textContent)).toEqual(['Continue Watching', 'New', 'My List', 'Drama']);
		fireEvent.click(screen.getByRole('button', { name: 'Movies' }));
		expect(screen.getByRole('button', { name: 'Movies' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('button', { name: 'Home' })).not.toHaveAttribute('aria-current');
	});

it('persists viewer controls, updates My List, and never offers Play for a demo title', async () => {
  const demo = { id: 'demo-1', title: 'Demo Signal', kind: 'film', local_only: false, playable: false, demo: true, listed: false };
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/preferences/')) return new Response(JSON.stringify({ view: 'grid', sort: 'year' }));
    if (path.includes('/list/')) return new Response(JSON.stringify({ listed: true }));
    if (path.includes('/films/demo-1') || path.includes('/items/demo-1')) return new Response(JSON.stringify(demo));
    return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [demo] }, { name: 'My List', items: [] }] }));
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse onExit={() => undefined} />);
  fireEvent.change(await screen.findByLabelText('View'), { target: { value: 'grid' } });
  await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/catalog/preferences/all', expect.objectContaining({ method: 'PUT' })));
  fireEvent.click(screen.getByTestId('card-demo-1'));
  expect(await screen.findAllByText('Demo title · no media file')).not.toHaveLength(0);
  expect(screen.queryByRole('button', { name: /play demo signal/i })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Add to My List' }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/catalog/list/film/demo-1', expect.objectContaining({ method: 'PUT' })));
});

it('reloads the server viewer model after a preference save and only exposes sort in grid mode', async () => {
  const grid = { preference: { view: 'grid' as const, sort: 'title' as const }, items: [{ id: 'b', title: 'Beta', kind: 'film', local_only: false, listed: false }, { id: 'a', title: 'Alpha', kind: 'film', local_only: false, listed: false }] };
  const rows = { preference: { view: 'rows' as const, sort: 'title' as const }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [grid.items[1], grid.items[0]] }, { name: 'My List', items: [] }] };
  let views = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/preferences/')) return new Response(JSON.stringify({ view: 'rows', sort: 'title' }));
    if (path.includes('/catalog/view')) return new Response(JSON.stringify(views++ ? rows : grid));
    return new Response(JSON.stringify({}));
  });
  render(<Browse mode="movies" onExit={() => undefined} />);
  expect(await screen.findByLabelText('Sort')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('View'), { target: { value: 'rows' } });
  expect(await screen.findByRole('heading', { name: 'New' })).toBeInTheDocument();
  expect(screen.queryByLabelText('Sort')).not.toBeInTheDocument();
  expect(screen.getAllByTestId(/card-/).map((card) => card.dataset.catalogId)).toEqual(['a', 'b']);
});

it('keeps direct search membership and adds one My List card when a title is repeated in rows', async () => {
  window.history.replaceState({}, '', '/search?q=demo');
  const demo = { id: 'demo-1', title: 'Demo Signal', kind: 'film', local_only: false, playable: false, demo: true, listed: true };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/search')) return new Response(JSON.stringify({ items: [demo] }));
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [demo] }, { name: 'Drama', items: [demo] }, { name: 'My List', items: [demo] }] }));
    if (path.includes('/catalog/films/demo-1')) return new Response(JSON.stringify(demo));
    if (path.includes('/catalog/list/')) return new Response(JSON.stringify({ listed: false }));
    return new Response(JSON.stringify(demo));
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse mode="search" onExit={() => undefined} />);
  fireEvent.click(await screen.findByTestId('card-demo-1'));
  expect(await screen.findByRole('button', { name: 'Remove from My List' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /play demo signal/i })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Remove from My List' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add to My List' })).toBeInTheDocument());
});

it('adds a repeated title to My List only once', async () => {
  const title = { id: 'film-1', title: 'Signal', kind: 'film', local_only: false, listed: false };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [title] }, { name: 'Drama', items: [title] }, { name: 'My List', items: [] }] }));
    if (path.includes('/catalog/films/film-1') || path.includes('/catalog/items/film-1')) return new Response(JSON.stringify(title));
    return new Response(JSON.stringify({ listed: true }));
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse onExit={() => undefined} />);
  fireEvent.click((await screen.findAllByTestId('card-film-1'))[0]);
  fireEvent.click(await screen.findByRole('button', { name: 'Add to My List' }));
  await waitFor(() => expect(within(screen.getByRole('region', { name: 'My List' })).getAllByTestId('card-film-1')).toHaveLength(1));
});
});
