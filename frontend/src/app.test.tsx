import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './app/App';
import { strictFetch } from './test/http';

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/'); });
async function renderApp() {
  render(<App />);
  await waitFor(() => expect(document.querySelector('.boot-splash')).toBeNull(), { timeout: 3000 });
}

describe('Flixr routes', () => {
  it.each([false, true])('shows demo guidance only when the server enables demo mode (%s)', async (demo) => {
    window.history.replaceState({}, '', '/home');
    vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([
      { path: '/api/v1/setup/status', handle: () => ({ json: { claimed: true, demo, demo_source: 'Curated showcase', readiness: { ffprobe: true, ffmpeg: true } } }) },
      { path: '/api/v1/catalog/view?media=all', handle: () => ({ json: { preference: { view: 'rows', sort: 'title' }, sections: [] } }) },
    ]));
    await renderApp();
    await screen.findByRole('heading', { name: /your library is waiting/i });
    expect(Boolean(screen.queryByRole('complementary', { name: 'Development demo' }))).toBe(demo);
  });

  it('returns an owner with no household profiles to the setup walkthrough after sign in', async () => {
    window.history.replaceState({}, '', '/login');
    vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([
      { path: '/api/v1/setup/status', handle: () => ({ json: { claimed: true, readiness: { ffprobe: true, ffmpeg: true } } }) },
      { path: '/api/v1/owner/login', method: 'POST', handle: () => ({ json: {} }) },
      { path: '/api/v1/profiles', handle: () => ({ json: { profiles: [] } }) },
      { path: '/api/v1/owner/roots', handle: () => ({ json: { films: '/media/films', tv: '' } }) },
    ]));
    await renderApp();
    fireEvent.change(await screen.findByLabelText(/owner password/i), { target: { value: 'existing owner password' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByLabelText(/films library/i)).toHaveValue('/media/films');
    expect(window.location.pathname).toBe('/setup');
    expect(screen.queryByLabelText(/setup token/i)).not.toBeInTheDocument();
  });

  it('recovers a bookmarked home after the local server becomes available', async () => {
    window.history.replaceState({}, '', '/home');
    let online = false;
    vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([
      { path: '/api/v1/setup/status', handle: () => {
        if (!online) throw new Error('Server unavailable');
        return { json: { claimed: true, readiness: { ffprobe: true, ffmpeg: true } } };
      } },
      { path: '/api/v1/catalog/view?media=all', handle: () => ({ json: { preference: { view: 'rows', sort: 'title' }, sections: [] } }) },
    ]));
    await renderApp();
    expect(await screen.findByRole('alert')).toHaveTextContent(/local server did not respond/i);
    online = true;
    fireEvent.click(screen.getByRole('button', { name: /try again/i }));
    expect(await screen.findByRole('heading', { name: /your library is waiting/i })).toBeVisible();
    expect(window.location.pathname).toBe('/home');
  });

  it('resumes interrupted setup with saved libraries and never asks to claim twice', async () => {
    window.history.replaceState({}, '', '/setup');
    vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([
      { path: '/api/v1/setup/status', handle: () => ({ json: { claimed: true, readiness: { ffprobe: true, ffmpeg: true } } }) },
      { path: '/api/v1/owner/roots', handle: () => ({ json: { films: '/media/films', tv: '/media/tv' } }) },
      { path: '/api/v1/profiles', handle: () => ({ json: { profiles: [] } }) },
    ]));
    await renderApp();
    expect(await screen.findByLabelText(/films library/i)).toHaveValue('/media/films');
    expect(screen.getByLabelText(/tv library/i)).toHaveValue('/media/tv');
    expect(screen.queryByLabelText(/setup token/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /skip for now/i }));
    expect(await screen.findByRole('heading', { name: /first profile/i })).toBeInTheDocument();
  });

  it('guides a claimed owner through libraries and a profile before entering home', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      const method = init?.method;
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: false, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/setup/claim')) return new Response(JSON.stringify({ claimed: true }), { status: 201 });
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ saved: true }));
      if (path.includes('/owner/scan')) return new Response(JSON.stringify({ scan: { status: 'running', scanned: 0, failed: 0, unmatched: 0 } }));
      if (path.includes('/profiles/profile-1/select')) return new Response(JSON.stringify({ selected: true }));
      if (path.endsWith('/profiles') && method === 'POST') return new Response(JSON.stringify({ id: 'profile-1', name: 'Alex', protected: false }), { status: 201 });
      if (path.endsWith('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [] }));
      throw new Error(`Unexpected request ${method ?? 'GET'} ${path}`);
    });
    await renderApp();
    fireEvent.change(await screen.findByLabelText(/setup token/i), { target: { value: 'one-time-token' } });
    expect(screen.getByRole('heading', { name: /local cinema/i })).not.toHaveFocus();
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: 'safe password' } });
    fireEvent.click(screen.getByRole('button', { name: /secure this server/i }));
    const libraries = await screen.findByRole('heading', { name: /libraries/i });
    await waitFor(() => expect(libraries).toHaveFocus());
    expect(screen.queryByText(/owner operations/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/tmdb/i)).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/films library/i), { target: { value: '/media/films' } });
    fireEvent.click(screen.getByRole('button', { name: /save libraries/i }));
    const profile = await screen.findByRole('heading', { name: /profile/i });
    await waitFor(() => expect(profile).toHaveFocus());
    await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan', expect.objectContaining({ method: 'POST' })));
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: 'Alex' } });
    fireEvent.click(screen.getByRole('button', { name: /create profile/i }));
    await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/profiles/profile-1/select', expect.objectContaining({ method: 'POST' })));
    await waitFor(() => expect(window.location.pathname).toBe('/home'));
  });


  it('retries profile selection without creating a duplicate profile', async () => {
    let creates = 0;
    let selections = 0;
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: false, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/setup/claim')) return new Response(JSON.stringify({ claimed: true }), { status: 201 });
      if (path.endsWith('/profiles') && init?.method === 'POST') { creates += 1; return new Response(JSON.stringify({ id: 'profile-1', name: 'Alex', protected: false }), { status: 201 }); }
      if (path.includes('/profiles/profile-1/select')) { selections += 1; return selections === 1 ? new Response(JSON.stringify({ error: { code: 'credential_busy' } }), { status: 429 }) : new Response(JSON.stringify({ selected: true })); }
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [] }));
      throw new Error(`Unexpected request ${init?.method ?? 'GET'} ${path}`);
    });
    await renderApp();
    fireEvent.change(await screen.findByLabelText(/setup token/i), { target: { value: 'token' } });
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: 'safe password' } });
    fireEvent.click(screen.getByRole('button', { name: /secure this server/i }));
    fireEvent.click(await screen.findByRole('button', { name: /skip for now/i }));
    fireEvent.change(await screen.findByLabelText(/^name$/i), { target: { value: 'Alex' } });
    const create = screen.getByRole('button', { name: /create profile/i });
    fireEvent.click(create);
    fireEvent.click(create);
    expect(await screen.findByRole('alert')).toHaveTextContent(/busy securing credentials/i);
    expect(screen.getByLabelText(/^name$/i)).toBeDisabled();
    expect(screen.getByLabelText(/pin/i)).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: /retry entering flixr/i }));
    await waitFor(() => expect(window.location.pathname).toBe('/home'));
    expect(creates).toBe(1);
  });

  it('keeps setup completable when an automatic ready scan cannot start', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: false, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/setup/claim')) return new Response(JSON.stringify({ claimed: true }), { status: 201 });
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ saved: true }));
      if (path.includes('/owner/scan')) return new Response(JSON.stringify({ error: { code: 'scan_failed' } }), { status: 500 });
      if (path.endsWith('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      throw new Error(`Unexpected request ${path}`);
    });
    await renderApp();
    fireEvent.change(await screen.findByLabelText(/setup token/i), { target: { value: 'token' } });
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: 'safe password' } });
    fireEvent.click(screen.getByRole('button', { name: /secure this server/i }));
    fireEvent.change(await screen.findByLabelText(/films library/i), { target: { value: '/media/films' } });
    fireEvent.click(screen.getByRole('button', { name: /save libraries/i }));
    expect(await screen.findByRole('status')).toHaveTextContent(/could not start a scan/i);
    expect(screen.getByRole('button', { name: /create profile/i })).toBeEnabled();
  });

  it('recovers a claimed server reloaded at setup through profile selection', async () => {
    window.history.replaceState({}, '', '/setup');
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      throw new Error(`Unexpected request ${path}`);
    });
    await renderApp();
    expect(await screen.findByRole('heading', { name: /who.s watching/i })).toBeInTheDocument();
    expect(window.location.pathname).toBe('/profiles');
  });

  it('recovers when browser history returns a claimed server to setup', async () => {
    window.history.replaceState({}, '', '/home');
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      if (path.includes('/catalog/view')) return new Response(JSON.stringify({ preference: { view: 'rows', sort: 'title' }, sections: [] }));
      throw new Error(`Unexpected request ${path}`);
    });
    await renderApp();
    await screen.findByRole('heading', { name: /local cinema/i });
    window.history.pushState({}, '', '/setup');
    fireEvent(window, new PopStateEvent('popstate'));
    expect(await screen.findByRole('heading', { name: /who.s watching/i })).toBeInTheDocument();
    expect(window.location.pathname).toBe('/profiles');
  });

	it('preserves the viewer destination while detail history opens and closes', async () => {
		window.history.replaceState({}, '', '/movies');
    vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([
      { path: '/api/v1/setup/status', handle: () => ({ json: { claimed: true, readiness: { ffprobe: true, ffmpeg: true } } }) },
      { path: /^\/api\/v1\/catalog\/view\?media=(film|all)$/, handle: () => ({ json: { preference: { view: 'grid', sort: 'title' }, items: [{ id: 'film-1', title: 'Signal', kind: 'film', local_only: true, listed: false }] } }) },
      { path: /^\/api\/v1\/catalog\/search\?q=(Signal|Relay)&offset=0&limit=48$/, handle: ({ url }) => { const title = url.searchParams.get('q') ?? ''; return { json: { items: [{ id: title.toLowerCase(), title, kind: 'film', local_only: true }] } }; } },
    ]));
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')); };
    await renderApp();
    fireEvent.click(await screen.findByTestId('card-film-1'));
    expect(await screen.findByRole('dialog')).toBeVisible();
		expect(window.location.pathname).toBe('/detail/film-1');
		fireEvent.click(screen.getByRole('button', { name: /close details/i }));
		await waitFor(() => expect(window.location.pathname).toBe('/movies'));
		expect(screen.getByRole('button', { name: 'Movies' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    const historyLength = window.history.length;
    fireEvent.change(screen.getByLabelText(/search titles/i), { target: { value: 'S' } });
    fireEvent.change(screen.getByLabelText(/search titles/i), { target: { value: 'Signal' } });
    await waitFor(() => expect(window.location.pathname + window.location.search).toBe('/search?q=Signal'));
    expect(window.history.length).toBe(historyLength);
    window.history.pushState({}, '', '/search?q=Relay');
    fireEvent(window, new PopStateEvent('popstate'));
    await waitFor(() => expect(screen.getByLabelText(/search titles/i)).toHaveValue('Relay'));
    expect(await screen.findByText('Relay')).toBeInTheDocument();
    expect(screen.queryByText(/Loading local Flixr/i)).not.toBeInTheDocument();
  });
});
