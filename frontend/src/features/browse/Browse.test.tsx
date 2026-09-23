import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Browse } from './Browse';
import { strictFetch } from '../../test/http';
import workflow from '../../../../docs/features/viewer-experience/workflow-regression.v1.json';

const viewerFetch = (json: unknown) => strictFetch([{ path: /^\/api\/v1\/catalog\/view\?media=(all|film|series)$/, handle: () => ({ json }) }]);
const workflowTask = (id: string) => {
  const task = workflow.tasks.find((candidate) => candidate.id === id);
  if (!task || !('actions' in task)) throw new Error(`Missing action-count fixture for ${id}`);
  return task;
};

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/home'); });
describe('browse', () => {
  it('lets a viewer choose an encoding without exposing duplicate title cards', async () => {
    const navigate = vi.fn();
    const item = {
      id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: false, edition_label: 'Director’s cut',
      versions: [
        { id: 'source-1080', label: '1080p · H.264', edition_id: 'film-1', width: 1920, height: 1080, selected: true, available: true },
        { id: 'source-4k', label: '4K · HDR · HEVC', edition_id: 'film-1', width: 3840, height: 2160, hdr: 'HDR10', selected: false, available: true },
      ],
    };
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/catalog/items/film-1')) return new Response(JSON.stringify(item));
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [item] }] }));
      if (path.includes('/ratings/')) return new Response(JSON.stringify({ rating: null }));
      if (path.endsWith('/screens')) return new Response(JSON.stringify({ screens: [] }));
      return new Response('{}');
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };

    render(<Browse detailID="film-1" onExit={() => undefined} onNavigate={navigate} />);

    const version = await screen.findByRole('combobox', { name: 'Version' });
    expect(version).toHaveValue('source-1080');
    const cards = screen.getAllByTestId('card-film-1');
    expect(cards).toHaveLength(1);
    expect(within(cards[0]).getByText('Director’s cut')).toBeVisible();
    fireEvent.change(version, { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: /Play Signal/ }));
    expect(navigate).toHaveBeenLastCalledWith('/play/film-1?version=auto');
    fireEvent.change(version, { target: { value: 'source-4k' } });
    fireEvent.click(screen.getByRole('button', { name: /Play Signal/ }));
    expect(navigate).toHaveBeenLastCalledWith('/play/film-1?version=source-4k');
  });

  it('leaves version choice on Auto when no explicit preference is persisted', async () => {
    const navigate = vi.fn();
    const item = { id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: false, versions: [
      { id: 'source-1080', label: '1080p · H.264', edition_id: 'film-1', selected: false, available: true },
      { id: 'source-4k', label: '4K · HEVC', edition_id: 'film-1', selected: false, available: true },
    ] };
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/catalog/items/film-1')) return new Response(JSON.stringify(item));
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [item] }] }));
      if (path.includes('/ratings/')) return new Response(JSON.stringify({ rating: null }));
      if (path.endsWith('/screens')) return new Response(JSON.stringify({ screens: [] }));
      return new Response('{}');
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };

    render(<Browse detailID="film-1" onExit={() => undefined} onNavigate={navigate} />);

    expect(await screen.findByRole('combobox', { name: 'Version' })).toHaveValue('');
    fireEvent.click(screen.getByRole('button', { name: /Play Signal/ }));
    expect(navigate).toHaveBeenLastCalledWith('/play/film-1');
  });

  it('virtualizes a 10k logical rail and restores focus after details close', async () => {
    const items = Array.from({ length: 10_000 }, (_, index) => ({ id: `${index}`, title: `Film ${index}`, kind: 'film', local_only: index === 0 }));
    vi.spyOn(globalThis, 'fetch').mockImplementation(viewerFetch({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Films', items }], total: 10_000, next: 48 }));
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')); };
    render(<Browse onExit={() => undefined} />);
    const first = await screen.findByTestId('card-0');
    expect(document.querySelectorAll('[data-card]').length).toBeLessThan(30);
    let actions = 0;
    actions += 1;
    fireEvent.click(first);
    await waitFor(() => expect(screen.getByRole('dialog')).toBeVisible());
    actions += 1;
    fireEvent.click(screen.getByRole('button', { name: /close details/i }));
    await waitFor(() => expect(first).toHaveFocus());
    expect(actions).toBe(workflowTask('return-from-details').actions);
  });

  it('opens the focal title through a semantic detail action', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(viewerFetch({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Films', items: [{ id: 'film-1', title: 'Signal', kind: 'film', year: 2024, synopsis: 'A local story.', local_only: true, backdrop: '/artwork/sky', listed: false }] }] }));
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
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Series', items: [{ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, listed: false }] }] }));
      throw new Error(`Unexpected request ${path}`);
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    render(<Browse onExit={() => undefined} />);
    fireEvent.click(await screen.findByTestId('card-series-1'));
    expect(await screen.findByText('Season 1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /S1 E1 First/i })).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/catalog/series/series-1', expect.anything());
  });

  it('shows a saved alternate order with spans and specials without exposing source-season bulk actions', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/catalog/series/series-1')) return new Response(JSON.stringify({ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, episode_order: 'dvd', order_needs_repair: true, seasons: [{ id: 's1', number: 1, episodes: [
        { id: 'episode-1', title: 'First file', kind: 'episode', season: 1, episode: 1, episode_end: 2, local_only: false, episode_order: { position: 2, end_position: 3, season: 1, episode: 4, episode_end: 5, special: false } },
        { id: 'special-1', title: 'Pilot special', kind: 'episode', season: 0, episode: 1, local_only: false, episode_order: { position: 1, end_position: 1, season: 0, episode: 1, episode_end: 1, special: true } },
        { id: 'episode-unmapped', title: 'Manual file', kind: 'episode', season: 1, episode: 3, local_only: false },
      ] }] }));
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Series', items: [{ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, listed: false }] }] }));
      throw new Error(`Unexpected request ${path}`);
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    render(<Browse onExit={() => undefined} />);
    fireEvent.click(await screen.findByTestId('card-series-1'));
    expect(await screen.findByText(/DVD order/i)).toBeVisible();
    expect(screen.getByText(/order needs repair/i)).toBeVisible();
    expect(screen.getByText(/Special · Pilot special/i)).toBeVisible();
    expect(screen.getByText(/S1 · E4–5/i)).toBeVisible();
    expect(screen.getByText('Manual file')).toBeVisible();
    expect(screen.getAllByText(/source order/i)).toHaveLength(2);
    expect(screen.queryByRole('button', { name: /mark season watched/i })).not.toBeInTheDocument();
  });

  it('uses an owner-repaired aired mapping instead of restoring canonical season controls', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/catalog/series/series-1')) return new Response(JSON.stringify({ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, episode_order: 'aired', seasons: [{ id: 's1', number: 1, episodes: [{ id: 'episode-1', title: 'Repaired file', kind: 'episode', season: 1, episode: 1, local_only: false, episode_order: { position: 1, end_position: 1, season: 2, episode: 1, episode_end: 1, special: false } }] }] }));
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Series', items: [{ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, listed: false }] }] }));
      throw new Error(`Unexpected request ${path}`);
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    render(<Browse onExit={() => undefined} />);
    fireEvent.click(await screen.findByTestId('card-series-1'));
    expect(await screen.findByText('Aired order')).toBeVisible();
    expect(screen.getByText(/S2 · E1 · Repaired file/i)).toBeVisible();
    expect(screen.queryByRole('button', { name: /mark season watched/i })).not.toBeInTheDocument();
  });

  it('keeps repair feedback visible when a selected order has no usable mappings', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/catalog/series/series-1')) return new Response(JSON.stringify({ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, episode_order: 'dvd', order_needs_repair: true, seasons: [{ id: 's1', number: 1, episodes: [{ id: 'episode-1', title: 'Manual file', kind: 'episode', season: 1, episode: 1, local_only: false }] }] }));
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Series', items: [{ id: 'series-1', title: 'Signal', kind: 'series', local_only: false, listed: false }] }] }));
      throw new Error(`Unexpected request ${path}`);
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    render(<Browse onExit={() => undefined} />);
    fireEvent.click(await screen.findByTestId('card-series-1'));
    expect(await screen.findByRole('alert')).toHaveTextContent(/order needs repair/i);
    expect(screen.getByText('Episodes')).toBeVisible();
    expect(screen.queryByText('DVD order')).not.toBeInTheDocument();
  });

  it('closes an open detail route when the dialog is cancelled', async () => {
    const navigate = vi.fn();
    vi.spyOn(globalThis, 'fetch').mockImplementation(viewerFetch({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Films', items: [{ id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: false }] }] }));
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')); };
    render(<Browse onExit={() => undefined} onNavigate={navigate} />);
    fireEvent.click(await screen.findByTestId('card-film-1'));
    const dialog = await screen.findByRole('dialog');
    fireEvent(dialog, new Event('cancel', { cancelable: true }));
    await waitFor(() => expect(navigate).toHaveBeenLastCalledWith('/home'));
  });

  it('renders an empty persistent catalog response without crashing', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(viewerFetch({ preference: { view: 'rows', sort: 'title' }, sections: [] }));
    render(<Browse onExit={() => undefined} />);
    expect(await screen.findByRole('heading', { name: /your library is waiting/i })).toBeInTheDocument();
  });

  it('retries the failed request and uses the route seam for an empty search', async () => {
    let views = 0;
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([{ path: '/api/v1/catalog/view?media=all', handle: () => views++ === 0 ? { status: 500, json: {} } : { json: { preference: { view: 'rows', sort: 'title' }, sections: [] } } }]));
    const { rerender } = render(<Browse onExit={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /try again/i }));
    await screen.findByRole('heading', { name: /your library is waiting/i });
    expect(fetcher).toHaveBeenCalledTimes(2);
    rerender(<Browse mode="search" query="" onExit={() => undefined} />);
    expect(await screen.findByRole('heading', { name: /no matching titles/i })).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('renders persistent destinations and lets the route update the active destination', async () => {
    const viewer = { preference: { view: 'rows' as const, sort: 'title' as const }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [{ id: 'film-1', title: 'Arrival', kind: 'film', local_only: false, playable: true, listed: false }] }, { name: 'My List', items: [] }, { name: 'Drama', items: [] }] };
    let actions = 0;
    const navigate = vi.fn(() => { actions += 1; });
    vi.spyOn(globalThis, 'fetch').mockImplementation(viewerFetch(viewer));
    const { rerender } = render(<Browse onExit={() => undefined} onNavigate={navigate} />);
		expect(await screen.findByRole('navigation', { name: 'Main navigation' })).toHaveTextContent('HomeMoviesTV');
    fireEvent.click(await screen.findByRole('button', { name: 'Play Arrival' }));
    expect(navigate).toHaveBeenLastCalledWith('/play/film-1');
		actions = 0;
		expect((await screen.findAllByRole('heading', { level: 2 })).map((heading) => heading.textContent)).toEqual(['Continue Watching', 'New', 'My List', 'Drama']);
		fireEvent.click(screen.getByRole('button', { name: 'Movies' }));
    expect(navigate).toHaveBeenLastCalledWith('/movies');
    expect(actions).toBe(workflowTask('library-entry').actions);
    rerender(<Browse mode="movies" onExit={() => undefined} onNavigate={navigate} />);
		expect(screen.getByRole('button', { name: 'Movies' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('button', { name: 'Home' })).not.toHaveAttribute('aria-current');
		actions = 0;
		fireEvent.click(screen.getByRole('button', { name: 'TV' }));
		expect(navigate).toHaveBeenLastCalledWith('/tv');
		expect(actions).toBe(workflowTask('media-destination-switching').actions);
		actions = 0;
		fireEvent.click(screen.getByRole('button', { name: 'Search' }));
		expect(navigate).toHaveBeenLastCalledWith('/search');
		expect(actions).toBe(workflowTask('search').actions);
	});

it('persists viewer controls, updates My List, and never offers Play for a demo title', async () => {
  const demo = { id: 'demo-1', title: 'Demo Signal', kind: 'film', local_only: false, playable: false, demo: true, listed: false };
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/preferences/')) return new Response(JSON.stringify({ view: 'grid', sort: 'year' }));
    if (path.includes('/list/')) return new Response(JSON.stringify({ listed: true }));
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [demo] }, { name: 'My List', items: [] }] }));
    throw new Error(`Unexpected request ${path}`);
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
    throw new Error(`Unexpected request ${path}`);
  });
  render(<Browse mode="movies" onExit={() => undefined} />);
  expect(await screen.findByLabelText('Sort')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('View'), { target: { value: 'rows' } });
  expect(await screen.findByRole('heading', { name: 'New' })).toBeInTheDocument();
  expect(screen.queryByLabelText('Sort')).not.toBeInTheDocument();
  expect(screen.getAllByTestId(/card-/).map((card) => card.dataset.catalogId)).toEqual(['a', 'b']);
});

it('uses the current search route query for direct search membership', async () => {
  const demo = { id: 'demo-1', title: 'Demo Signal', kind: 'film', local_only: false, playable: false, demo: true, listed: true };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('state_id=demo-1')) return new Response(JSON.stringify({ state: { listed: true, continue_watching_dismissed: false } }));
    if (path.includes('/catalog/search')) return new Response(JSON.stringify({ items: [demo] }));
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [demo] }, { name: 'Drama', items: [demo] }, { name: 'My List', items: [demo] }] }));
    if (path.includes('/catalog/list/')) return new Response(JSON.stringify({ listed: false }));
    throw new Error(`Unexpected request ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse mode="search" query="demo" onExit={() => undefined} />);
  fireEvent.click(await screen.findByTestId('card-demo-1'));
  expect(await screen.findByRole('button', { name: 'Remove from My List' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /play demo signal/i })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Remove from My List' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add to My List' })).toBeInTheDocument());
});

it('adds a repeated title to My List only once', async () => {
  const title = { id: 'film-1', title: 'Signal', kind: 'film', local_only: false, listed: false };
  let listed = false;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [{ ...title, listed }] }, { name: 'Drama', items: [{ ...title, listed }] }, { name: 'My List', items: listed ? [{ ...title, listed }] : [] }] }));
    if (path.includes('/catalog/list/')) { listed = true; return new Response(JSON.stringify({ listed })); }
    throw new Error(`Unexpected request ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse onExit={() => undefined} />);
  fireEvent.click((await screen.findAllByTestId('card-film-1'))[0]);
  fireEvent.click(await screen.findByRole('button', { name: 'Add to My List' }));
  await waitFor(() => expect(within(screen.getByRole('region', { name: 'My List' })).getAllByTestId('card-film-1')).toHaveLength(1));
});

it('keeps the newest search response when an older request finishes last', async () => {
  let resolveOld: ((response: Response) => void) | undefined;
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [] }));
    if (path.includes('q=old')) return new Promise<Response>((resolve) => { resolveOld = resolve; });
    if (path.includes('q=new')) return new Response(JSON.stringify({ items: [{ id: 'new', title: 'New result', kind: 'film', local_only: true }] }));
    throw new Error(`Unexpected request ${path}`);
  });
  const { rerender } = render(<Browse mode="search" query="old" onExit={() => undefined} />);
  await waitFor(() => expect(fetcher.mock.calls.some(([input]) => String(input).includes('q=old'))).toBe(true));
  rerender(<Browse mode="search" query="new" onExit={() => undefined} />);
  expect(await screen.findByText('New result')).toBeInTheDocument();
  await act(async () => { resolveOld?.(new Response(JSON.stringify({ items: [{ id: 'old', title: 'Old result', kind: 'film', local_only: true }] }))); });
  expect(screen.queryByText('Old result')).not.toBeInTheDocument();
});

it('keeps a valid catalog visible when a My List write fails', async () => {
  const title = { id: 'film-1', title: 'Signal', kind: 'film', local_only: false, listed: false };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [title] }, { name: 'My List', items: [] }] }));
    if (path.includes('/catalog/list/')) return new Response(JSON.stringify({ error: { code: 'catalog_list_failed' } }), { status: 500 });
    throw new Error(`Unexpected request ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse onExit={() => undefined} />);
  fireEvent.click(await screen.findByTestId('card-film-1'));
  fireEvent.click(await screen.findByRole('button', { name: 'Add to My List' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(/could not update My List/i);
  expect(screen.getAllByRole('heading', { name: 'Signal' })).not.toHaveLength(0);
  expect(screen.queryByRole('heading', { name: /catalog unavailable/i })).not.toBeInTheDocument();
});

it('hides a Continue Watching title with a sibling action and restores it with Undo', async () => {
  const title = { id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: true, continue_watching_dismissed: false };
  let dismissed = false;
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/continue-watching/film/film-1')) {
      dismissed = init?.method === 'DELETE';
      return new Response(JSON.stringify({ dismissed }));
    }
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({
      preference: { view: 'rows', sort: 'title' },
      sections: [
        { name: 'Continue Watching', items: dismissed ? [] : [{ ...title, continue_watching_dismissed: dismissed }] },
        { name: 'New', items: [{ ...title, continue_watching_dismissed: dismissed }] },
        { name: 'My List', items: [{ ...title, continue_watching_dismissed: dismissed }] },
      ],
    }));
    throw new Error(`Unexpected request ${path}`);
  });
  render(<Browse onExit={() => undefined} />);
  const row = await screen.findByRole('region', { name: 'Continue Watching' });
  const card = within(row).getByTestId('card-film-1');
  expect(within(card).queryByRole('button')).not.toBeInTheDocument();

  fireEvent.click(within(row).getByRole('button', { name: 'Hide Signal from Continue Watching' }));

  expect(await screen.findByRole('status')).toHaveTextContent('Signal hidden from Continue Watching');
  expect(within(row).queryByTestId('card-film-1')).not.toBeInTheDocument();
  expect(within(screen.getByRole('region', { name: 'My List' })).getByTestId('card-film-1')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
  await waitFor(() => expect(within(row).getByTestId('card-film-1')).toBeInTheDocument());
  expect(fetcher).toHaveBeenCalledWith('/api/v1/catalog/continue-watching/film/film-1', expect.objectContaining({ method: 'DELETE' }));
  expect(fetcher).toHaveBeenCalledWith('/api/v1/catalog/continue-watching/film/film-1', expect.objectContaining({ method: 'PUT' }));
});

it('offers explicit Continue Watching restore from a dismissed title detail', async () => {
  const title = { id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: false, continue_watching_dismissed: true };
  let dismissed = true;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/continue-watching/film/film-1')) {
      dismissed = init?.method === 'DELETE';
      return new Response(JSON.stringify({ dismissed }));
    }
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [
      { name: 'Continue Watching', items: dismissed ? [] : [{ ...title, continue_watching_dismissed: false }] },
      { name: 'New', items: [{ ...title, continue_watching_dismissed: dismissed }] },
      { name: 'My List', items: [] },
    ] }));
    throw new Error(`Unexpected request ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse onExit={() => undefined} />);
  fireEvent.click(within(await screen.findByRole('region', { name: 'New' })).getByTestId('card-film-1'));
  fireEvent.click(await screen.findByRole('button', { name: 'Restore to Continue Watching' }));
  await waitFor(() => expect(within(screen.getByRole('region', { name: 'Continue Watching' })).getByTestId('card-film-1')).toBeInTheDocument());
});

it('carries Continue Watching dismissal state into search details', async () => {
  const title = { id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: false, continue_watching_dismissed: true };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('state_id=film-1')) return new Response(JSON.stringify({ state: { listed: false, continue_watching_dismissed: true } }));
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [title] }] }));
    if (path.includes('/catalog/search')) return new Response(JSON.stringify({ items: [{ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }] }));
    throw new Error(`Unexpected request ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse mode="search" query="Signal" onExit={() => undefined} />);
  fireEvent.click(within(await screen.findByRole('region', { name: 'Search results' })).getByTestId('card-film-1'));
  expect(await screen.findByRole('button', { name: 'Restore to Continue Watching' })).toBeInTheDocument();
});

it('keeps the title visible when hiding Continue Watching fails', async () => {
  const title = { id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: false };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/continue-watching/')) return new Response(JSON.stringify({ error: { code: 'catalog_continue_watching_failed' } }), { status: 500 });
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [title] }, { name: 'New', items: [title] }, { name: 'My List', items: [] }] }));
    throw new Error(`Unexpected request ${path}`);
  });
  render(<Browse onExit={() => undefined} />);
  const row = await screen.findByRole('region', { name: 'Continue Watching' });
  fireEvent.click(within(row).getByRole('button', { name: 'Hide Signal from Continue Watching' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('could not update Continue Watching');
  expect(within(row).getByTestId('card-film-1')).toBeInTheDocument();
});

it('keeps the newest direct detail when an older item request finishes last', async () => {
  let resolveOld: ((response: Response) => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [] }));
    if (path.includes('/catalog/items/old')) return new Promise<Response>((resolve) => { resolveOld = resolve; });
    if (path.includes('/catalog/items/new')) return new Response(JSON.stringify({ id: 'new', title: 'New detail', kind: 'film', local_only: true }));
    throw new Error(`Unexpected request ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  const { rerender } = render(<Browse detailID="old" onExit={() => undefined} />);
  await waitFor(() => expect(resolveOld).toBeTypeOf('function'));
  rerender(<Browse detailID="new" onExit={() => undefined} />);
  expect(await screen.findByRole('heading', { name: 'New detail' })).toBeInTheDocument();
  await act(async () => { resolveOld?.(new Response(JSON.stringify({ id: 'old', title: 'Old detail', kind: 'film', local_only: true }))); });
  expect(screen.queryByRole('heading', { name: 'Old detail' })).not.toBeInTheDocument();
});

it('reconciles My List and Continue Watching state when a deep detail loads before the viewer model', async () => {
  let resolveViewer: ((response: Response) => void) | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/items/film-1')) return new Response(JSON.stringify({ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }));
    if (path.includes('/catalog/view')) return new Promise<Response>((resolve) => { resolveViewer = resolve; });
    throw new Error(`Unexpected request ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse detailID="film-1" onExit={() => undefined} />);
  expect(await screen.findByRole('button', { name: 'Add to My List' })).toBeInTheDocument();
  await act(async () => { resolveViewer?.(new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'My List', items: [{ id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: true, continue_watching_dismissed: true }] }] }))); });
  expect(await screen.findByRole('button', { name: 'Remove from My List' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Restore to Continue Watching' })).toBeInTheDocument();
});

it('appends a bounded section page without duplicates or moving keyboard focus', async () => {
  const first = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false };
  const second = { id: 'b', title: 'Beta', kind: 'film', local_only: true, listed: false };
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('section=New')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [first, second] }] }));
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [first], next_cursor: 'page-2' }] }));
    throw new Error(`Unexpected request ${path}`);
  });
  render(<Browse onExit={() => undefined} />);
  const card = await screen.findByTestId('card-a');
  card.focus();
  fireEvent.click(screen.getByRole('button', { name: 'Load more New' }));
  expect(await screen.findByTestId('card-b')).toBeInTheDocument();
  expect(screen.getAllByTestId('card-a')).toHaveLength(1);
  expect(card).toHaveFocus();
  expect(fetcher.mock.calls.some(([input]) => String(input).includes('section=New') && String(input).includes('cursor=page-2'))).toBe(true);
});

it('keeps an existing section visible and retries a failed next page', async () => {
  const first = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false };
  let attempts = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('section=New')) {
      attempts += 1;
      if (attempts === 1) return new Response('{}', { status: 500 });
      return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [{ id: 'b', title: 'Beta', kind: 'film', local_only: true, listed: false }] }] }));
    }
    return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [first], next_cursor: 'page-2' }] }));
  });
  render(<Browse onExit={() => undefined} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Load more New' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('More titles could not be loaded');
  expect(screen.getByTestId('card-a')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Retry New' }));
  expect(await screen.findByTestId('card-b')).toBeInTheDocument();
});

it('aborts an obsolete viewer request and search does not preload a viewer model', async () => {
  let initialSignal: AbortSignal | undefined;
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/view')) {
      initialSignal = init?.signal ?? undefined;
      return new Promise<Response>(() => undefined);
    }
    if (path.includes('/catalog/search')) return new Response(JSON.stringify({ items: [] }));
    throw new Error(`Unexpected request ${path}`);
  });
  const { rerender } = render(<Browse onExit={() => undefined} />);
  await waitFor(() => expect(initialSignal).toBeDefined());
  rerender(<Browse mode="search" query="signal" onExit={() => undefined} />);
  await waitFor(() => expect(initialSignal?.aborted).toBe(true));
  await waitFor(() => expect(fetcher.mock.calls.some(([input]) => String(input).includes('/catalog/search'))).toBe(true));
  expect(fetcher.mock.calls.filter(([input]) => String(input).includes('/catalog/view'))).toHaveLength(1);
});

it('does not let a completed preference save replace a newer destination', async () => {
  let resolvePreference: ((response: Response) => void) | undefined;
  const filmModel = { preference: { view: 'grid', sort: 'title' }, items: [{ id: 'film', title: 'Film destination', kind: 'film', local_only: true, listed: false }] };
  const seriesModel = { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [{ id: 'series', title: 'Series destination', kind: 'series', local_only: true, listed: false }] }] };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/preferences/film') && init?.method === 'PUT') return new Promise<Response>((resolve) => { resolvePreference = resolve; });
    if (path.includes('media=film')) return new Response(JSON.stringify(filmModel));
    if (path.includes('media=series')) return new Response(JSON.stringify(seriesModel));
    throw new Error(`Unexpected request ${init?.method ?? 'GET'} ${path}`);
  });
  const { rerender } = render(<Browse mode="movies" onExit={() => undefined} />);
  fireEvent.change(await screen.findByLabelText('View'), { target: { value: 'rows' } });
  await waitFor(() => expect(resolvePreference).toBeTypeOf('function'));
  rerender(<Browse mode="tv" onExit={() => undefined} />);
  expect(await screen.findAllByText('Series destination')).not.toHaveLength(0);
  await act(async () => { resolvePreference?.(new Response(JSON.stringify({ view: 'rows', sort: 'title' }))); });
  await waitFor(() => expect(screen.getAllByText('Series destination')).not.toHaveLength(0));
  expect(screen.queryByText('Film destination')).not.toBeInTheDocument();
});
it('keeps focus when the last page finishes after keyboard-style activation', async () => {
  const first = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).includes('section=New')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [{ id: 'b', title: 'Beta', kind: 'film', local_only: true, listed: false }] }] }));
    return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [first], next_cursor: 'last-page' }] }));
  });
  render(<Browse onExit={() => undefined} />);
  const load = await screen.findByRole('button', { name: 'Load more New' });
  load.focus();
  fireEvent.click(load);
  await screen.findByTestId('card-b');
  expect(screen.getByRole('button', { name: 'All New loaded' })).toHaveFocus();
});

it('rejects a late old-sort page after changing the preference', async () => {
  let resolveOld: ((response: Response) => void) | undefined;
  let oldSignal: AbortSignal | null | undefined;
  let views = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/preferences/')) return new Response(JSON.stringify({ view: 'grid', sort: 'year' }));
    if (path.includes('cursor=old-page')) {
      oldSignal = init?.signal;
      return new Promise<Response>((resolve) => { resolveOld = resolve; });
    }
    if (path.includes('/catalog/view')) return new Response(JSON.stringify(views++ === 0
      ? { preference: { view: 'grid', sort: 'title' }, items: [{ id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false }], next_cursor: 'old-page' }
      : { preference: { view: 'grid', sort: 'year' }, items: [{ id: 'b', title: 'Beta', kind: 'film', local_only: true, listed: false }], next_cursor: 'new-page' }));
    throw new Error(`Unexpected ${path}`);
  });
  render(<Browse mode="movies" onExit={() => undefined} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Load more Titles' }));
  await waitFor(() => expect(resolveOld).toBeTypeOf('function'));
  fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'year' } });
  expect(await screen.findByTestId('card-b')).toBeInTheDocument();
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => { resolveOld?.(new Response(JSON.stringify({ preference: { view: 'grid', sort: 'title' }, items: [{ id: 'old', title: 'Old sort', kind: 'film', local_only: true, listed: false }] }))); });
  expect(screen.queryByTestId('card-old')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Load more Titles' })).toBeEnabled();
});

it('keeps authoritative My List state for a detail beyond the loaded page', async () => {
  const deep = { id: 'deep', title: 'Deep title', kind: 'film', local_only: true, listed: false };
  let listed = false;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('state_id=deep')) return new Response(JSON.stringify({ state: { listed: false, continue_watching_dismissed: false } }));
    if (path.includes('/catalog/items/deep')) return new Response(JSON.stringify(deep));
    if (path.includes('/catalog/list/film/deep')) { listed = true; return new Response(JSON.stringify({ listed })); }
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [{ id: 'other', title: 'Other', kind: 'film', local_only: true, listed: false }] }, { name: 'My List', items: listed ? [{ ...deep, listed }] : [] }] }));
    throw new Error(`Unexpected ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse detailID="deep" onExit={() => undefined} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add to My List' }));
  expect(await screen.findByRole('button', { name: 'Remove from My List' })).toBeInTheDocument();
  expect(within(screen.getByRole('region', { name: 'My List' })).getByTestId('card-deep')).toBeInTheDocument();
});

it('invalidates pending My List pages after removing a title', async () => {
  const title = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: true };
  let resolvePage: ((response: Response) => void) | undefined;
  let pageSignal: AbortSignal | null | undefined;
  let views = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('state_id=a')) return new Response(JSON.stringify({ state: { listed: true, continue_watching_dismissed: false } }));
    if (path.includes('/catalog/items/a')) return new Response(JSON.stringify(title));
    if (path.includes('/catalog/list/film/a')) return new Response(JSON.stringify({ listed: false }));
    if (path.includes('section=My+List')) { pageSignal = init?.signal; return new Promise<Response>((resolve) => { resolvePage = resolve; }); }
    if (path.includes('/catalog/view')) return new Response(JSON.stringify(views++ === 0
      ? { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [title] }, { name: 'My List', items: [title], next_cursor: 'old' }] }
      : { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [{ ...title, listed: false }] }, { name: 'My List', items: [] }] }));
    throw new Error(`Unexpected ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse onExit={() => undefined} />);
  fireEvent.click(await within(await screen.findByRole('region', { name: 'New' })).findByTestId('card-a'));
  fireEvent.click(screen.getByRole('button', { name: 'Load more My List' }));
  await waitFor(() => expect(resolvePage).toBeTypeOf('function'));
  fireEvent.click(screen.getByRole('button', { name: 'Remove from My List' }));
  await waitFor(() => expect(pageSignal?.aborted).toBe(true));
  await act(async () => { resolvePage?.(new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'My List', items: [title] }] }))); });
  await waitFor(() => expect(within(screen.getByRole('region', { name: 'My List' })).queryByTestId('card-a')).not.toBeInTheDocument());
});

it('does not allow unknown detail membership to be changed before state loads and permits retry', async () => {
  let resolveState: ((response: Response) => void) | undefined;
  let attempts = 0;
  const deep = { id: 'deep', title: 'Deep title', kind: 'film', local_only: true };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('state_id=deep')) {
      attempts += 1;
      if (attempts === 1) return new Promise<Response>((resolve) => { resolveState = resolve; });
      if (attempts === 2) return new Response('{}', { status: 500 });
      return new Response(JSON.stringify({ state: { listed: true, continue_watching_dismissed: true } }));
    }
    if (path.includes('/catalog/items/deep')) return new Response(JSON.stringify(deep));
    if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [{ id: 'other', title: 'Other', kind: 'film', local_only: true, listed: false }] }] }));
    throw new Error(`Unexpected ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse detailID="deep" onExit={() => undefined} />);
  await waitFor(() => expect(resolveState).toBeTypeOf('function'));
  await screen.findByRole('dialog');
  expect(screen.getByRole('button', { name: 'Add to My List' })).toBeDisabled();
  await act(async () => { resolveState?.(new Response('{}', { status: 500 })); });
  expect(await screen.findByRole('alert')).toHaveTextContent('Title state could not be loaded');
  expect(screen.getByRole('button', { name: 'Add to My List' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Retry title state' }));
  await waitFor(() => expect(attempts).toBe(2));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Retry title state' })).toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: 'Retry title state' }));
  expect(await screen.findByRole('button', { name: 'Remove from My List' })).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Restore to Continue Watching' })).toBeInTheDocument();
});

it('does not start an old-cursor page while a mutation refresh is pending', async () => {
  const title = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false };
  let resolveFresh: ((response: Response) => void) | undefined;
  let firstPages = 0;
  let oldPages = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('state_id=a')) return new Response(JSON.stringify({ state: { listed: false, continue_watching_dismissed: false } }));
    if (path.includes('/catalog/items/a')) return new Response(JSON.stringify(title));
    if (path.includes('/catalog/list/film/a')) return new Response(JSON.stringify({ listed: true }));
    if (path.includes('cursor=old')) { oldPages += 1; return new Response(JSON.stringify({ preference: { view: 'grid', sort: 'title' }, items: [] })); }
    if (path.includes('/catalog/view')) return firstPages++ === 0
      ? new Response(JSON.stringify({ preference: { view: 'grid', sort: 'title' }, items: [title], next_cursor: 'old' }))
      : new Promise<Response>((resolve) => { resolveFresh = resolve; });
    throw new Error(`Unexpected ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
  render(<Browse mode="movies" onExit={() => undefined} />);
  fireEvent.click(await screen.findByTestId('card-a'));
  fireEvent.click(await screen.findByRole('button', { name: 'Add to My List' }));
  await waitFor(() => expect(resolveFresh).toBeTypeOf('function'));
  fireEvent.click(screen.getByRole('button', { name: 'Close details' }));
  const load = screen.getByRole('button', { name: /more Titles/i });
  fireEvent.click(load);
  expect(oldPages).toBe(0);
  await act(async () => { resolveFresh?.(new Response(JSON.stringify({ preference: { view: 'grid', sort: 'title' }, items: [{ ...title, listed: true }, { id: 'new', title: 'New title', kind: 'film', local_only: true, listed: false }], next_cursor: 'new' }))); });
  expect(await screen.findByTestId('card-new')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Load more Titles' })).toBeEnabled();
});

it('does not let a completed My List write refresh the previous destination', async () => {
  const film = { id: 'film', title: 'Film destination', kind: 'film', local_only: true, listed: false };
  const series = { id: 'series', title: 'Series destination', kind: 'series', local_only: true, listed: false };
  let resolveWrite: ((response: Response) => void) | undefined;
  let filmViews = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('state_id=film')) return new Response(JSON.stringify({ state: { listed: false, continue_watching_dismissed: false } }));
    if (path.includes('/catalog/items/film')) return new Response(JSON.stringify(film));
    if (path.includes('/catalog/list/film/film')) return new Promise<Response>((resolve) => { resolveWrite = resolve; });
    if (path.includes('media=film')) { filmViews += 1; return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [film] }, { name: 'My List', items: [] }] })); }
    if (path.includes('media=series')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [series] }] }));
    throw new Error(`Unexpected ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
  const { rerender } = render(<Browse mode="movies" detailID="film" onExit={() => undefined} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add to My List' }));
  await waitFor(() => expect(resolveWrite).toBeTypeOf('function'));
  rerender(<Browse mode="tv" onExit={() => undefined} />);
  expect(await screen.findByTestId('card-series')).toBeInTheDocument();
  await act(async () => { resolveWrite?.(new Response(JSON.stringify({ listed: true }))); });
  expect(screen.getByTestId('card-series')).toBeInTheDocument();
  expect(screen.queryByTestId('card-film')).not.toBeInTheDocument();
  expect(filmViews).toBe(1);
});

it('reconciles two concurrent Continue Watching dismissals in one destination', async () => {
  const a = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false };
  const b = { id: 'b', title: 'Beta', kind: 'film', local_only: true, listed: false };
  const dismissed = new Set<string>();
  let resolveA: ((response: Response) => void) | undefined;
  let resolveB: ((response: Response) => void) | undefined;
  let views = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/catalog/continue-watching/film/a')) return new Promise<Response>((resolve) => { resolveA = resolve; });
    if (path.includes('/catalog/continue-watching/film/b')) return new Promise<Response>((resolve) => { resolveB = resolve; });
    if (path.includes('/catalog/view')) {
      views += 1;
      return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [a, b].filter((item) => !dismissed.has(item.id)) }, { name: 'New', items: [a, b] }] }));
    }
    throw new Error(`Unexpected ${path}`);
  });
  render(<Browse onExit={() => undefined} />);
  const row = await screen.findByRole('region', { name: 'Continue Watching' });
  fireEvent.click(within(row).getByRole('button', { name: 'Hide Alpha from Continue Watching' }));
  fireEvent.click(within(row).getByRole('button', { name: 'Hide Beta from Continue Watching' }));
  await waitFor(() => expect(resolveA && resolveB).toBeTruthy());
  dismissed.add('a');
  await act(async () => { resolveA?.(new Response(JSON.stringify({ dismissed: true }))); });
  await waitFor(() => expect(views).toBe(2));
  expect(within(row).getByTestId('card-b')).toBeInTheDocument();
  dismissed.add('b');
  await act(async () => { resolveB?.(new Response(JSON.stringify({ dismissed: true }))); });
  await waitFor(() => expect(views).toBe(3));
  expect(within(row).queryByTestId('card-b')).not.toBeInTheDocument();
});

it('loads the saved preference after a concurrent dismissal refresh', async () => {
  const title = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false };
  let resolvePreference: ((response: Response) => void) | undefined;
  let saved = false;
  let dismissed = false;
  let views = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/preferences/all') && init?.method === 'PUT') return new Promise<Response>((resolve) => { resolvePreference = resolve; });
    if (path.includes('/catalog/continue-watching/film/a')) { dismissed = true; return new Response(JSON.stringify({ dismissed })); }
    if (path.includes('/catalog/view')) {
      views += 1;
      return new Response(JSON.stringify(saved
        ? { preference: { view: 'grid', sort: 'title' }, items: [title] }
        : { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: dismissed ? [] : [title] }, { name: 'New', items: [title] }] }));
    }
    throw new Error(`Unexpected ${path}`);
  });
  render(<Browse onExit={() => undefined} />);
  const row = await screen.findByRole('region', { name: 'Continue Watching' });
  fireEvent.change(await screen.findByLabelText('View'), { target: { value: 'grid' } });
  await waitFor(() => expect(resolvePreference).toBeTypeOf('function'));
  fireEvent.click(within(row).getByRole('button', { name: 'Hide Alpha from Continue Watching' }));
  await waitFor(() => expect(views).toBe(2));
  saved = true;
  await act(async () => { resolvePreference?.(new Response(JSON.stringify({ view: 'grid', sort: 'title' }))); });
  expect(await screen.findByRole('region', { name: 'Titles' })).toBeInTheDocument();
  expect(screen.queryByRole('region', { name: 'Continue Watching' })).not.toBeInTheDocument();
});

it('loads the saved preference after a concurrent watched-state reload', async () => {
  const title = { id: 'a', title: 'Alpha', kind: 'film', local_only: true, listed: false };
  let resolvePreference: ((response: Response) => void) | undefined;
  let saved = false;
  let views = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/catalog/preferences/all') && init?.method === 'PUT') return new Promise<Response>((resolve) => { resolvePreference = resolve; });
    if (path.includes('/catalog/watched/film/a')) return new Response(JSON.stringify({ watched: true }));
    if (path.includes('state_id=a')) return new Response(JSON.stringify({ state: { listed: false } }));
    if (path.includes('/catalog/view')) {
      views += 1;
      return new Response(JSON.stringify(saved
        ? { preference: { view: 'grid', sort: 'title' }, items: [title] }
        : { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [title] }] }));
    }
    throw new Error(`Unexpected ${path}`);
  });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  render(<Browse onExit={() => undefined} />);
  await screen.findByRole('region', { name: 'New' });
  fireEvent.change(screen.getByLabelText('View'), { target: { value: 'grid' } });
  await waitFor(() => expect(resolvePreference).toBeTypeOf('function'));
  fireEvent.click(screen.getByTestId('card-a'));
  fireEvent.click(await screen.findByRole('button', { name: 'Mark watched' }));
  await waitFor(() => expect(views).toBe(2));
  saved = true;
  await act(async () => { resolvePreference?.(new Response(JSON.stringify({ view: 'grid', sort: 'title' }))); });
  expect(await screen.findByRole('region', { name: 'Titles' })).toBeInTheDocument();
});

});

function pendingResponse() {
 let resolve!: (value: Response) => void;
 const promise = new Promise<Response>((done) => { resolve = done; });
 return {promise, resolve};
}
function ratingFetch(overrides: { get?: Promise<Response>; save?: Promise<Response> } = {}) {
 return async (input: RequestInfo | URL, init?: RequestInit) => {
  const path=String(input);
  if (path.includes('/catalog/view')) return new Response(JSON.stringify({preference:{view:'rows',sort:'title'},sections:[]}));
  if (path.includes('/catalog/items/')) return new Response(JSON.stringify({id:path.endsWith('/second')?'second':'first',title:'Detail',kind:'film',local_only:true}));
  if (path.includes('/ratings/')) {
   if (init?.method === 'PUT') return overrides.save ?? new Response(JSON.stringify({saved:true}));
   return path.endsWith('/first') && overrides.get ? overrides.get : new Response(JSON.stringify({rating:{value:2}}));
  }
  throw new Error(`Unexpected request ${path}`);
 };
}
it('waits for the current rating before allowing a save', async () => {
 const get=pendingResponse();
 vi.spyOn(globalThis,'fetch').mockImplementation(ratingFetch({get:get.promise}));
 HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
 render(<Browse detailID="first" onExit={()=>undefined}/>);
 const select=await screen.findByRole('combobox',{name:'Your rating'});
 expect(select).toBeDisabled();
 await act(async()=>{get.resolve(new Response(JSON.stringify({rating:{value:3}})));});
 expect(select).toHaveValue('3'); expect(select).toBeEnabled();
});
it('serializes rating saves and displays the saved value', async () => {
 const save=pendingResponse();
 vi.spyOn(globalThis,'fetch').mockImplementation(ratingFetch({save:save.promise}));
 HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
 render(<Browse detailID="first" onExit={()=>undefined}/>);
 const select=await screen.findByRole('combobox',{name:'Your rating'});
 await waitFor(()=>expect(select).toHaveValue('2'));
 fireEvent.change(select,{target:{value:'5'}});
 expect(select).toBeDisabled();
 await act(async()=>{save.resolve(new Response(JSON.stringify({saved:true})));});
 expect(select).toHaveValue('5'); expect(select).toBeEnabled();
});
it('keeps a new catalog rating when an old save finishes last', async () => {
 const save=pendingResponse();
 vi.spyOn(globalThis,'fetch').mockImplementation(ratingFetch({save:save.promise}));
 HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
 const {rerender}=render(<Browse detailID="first" onExit={()=>undefined}/>);
 const select=await screen.findByRole('combobox',{name:'Your rating'});
 await waitFor(()=>expect(select).toHaveValue('2'));
 fireEvent.change(select,{target:{value:'5'}});
 rerender(<Browse detailID="second" onExit={()=>undefined}/>);
 await waitFor(()=>expect(screen.getByRole('combobox',{name:'Your rating'})).toBeEnabled());
 await act(async()=>{save.resolve(new Response(JSON.stringify({saved:true})));});
 expect(screen.getByRole('combobox',{name:'Your rating'})).toHaveValue('2');
});
