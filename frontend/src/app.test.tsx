import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './app/App';

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/'); });
describe('Flixr routes', () => {
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
      return new Response(JSON.stringify({}));
    });
    render(<App />);
    fireEvent.change(await screen.findByLabelText(/setup token/i), { target: { value: 'one-time-token' } });
    expect(screen.getByRole('heading', { name: /local cinema/i })).not.toHaveFocus();
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: 'safe password' } });
    fireEvent.click(screen.getByRole('button', { name: /secure this server/i }));
    const libraries = await screen.findByRole('heading', { name: /libraries/i });
    expect(libraries).toHaveFocus();
    expect(screen.queryByText(/owner operations/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/tmdb/i)).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/films library/i), { target: { value: '/media/films' } });
    fireEvent.click(screen.getByRole('button', { name: /save libraries/i }));
    const profile = await screen.findByRole('heading', { name: /profile/i });
    expect(profile).toHaveFocus();
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
      return new Response(JSON.stringify({ profiles: [] }));
    });
    render(<App />);
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
      return new Response(JSON.stringify({ profiles: [] }));
    });
    render(<App />);
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
      return new Response(JSON.stringify({}));
    });
    render(<App />);
    expect(await screen.findByRole('heading', { name: /choose your profile/i })).toBeInTheDocument();
    expect(window.location.pathname).toBe('/profiles');
  });

  it('recovers when browser history returns a claimed server to setup', async () => {
    window.history.replaceState({}, '', '/home');
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ items: [], total: 0, next: null }));
    });
    render(<App />);
    await screen.findByRole('heading', { name: /local cinema/i });
    window.history.pushState({}, '', '/setup');
    fireEvent(window, new PopStateEvent('popstate'));
    expect(await screen.findByRole('heading', { name: /choose your profile/i })).toBeInTheDocument();
    expect(window.location.pathname).toBe('/profiles');
  });

	it('preserves the viewer destination while detail history opens and closes', async () => {
		window.history.replaceState({}, '', '/movies');
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/catalog/films/film-1')) return new Response(JSON.stringify({ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }));
		if (path.includes('/catalog/items/film-1')) return new Response(JSON.stringify({ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }));
      return new Response(JSON.stringify({ items: [{ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }], total: 1, next: null }));
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')); };
    render(<App />);
    fireEvent.click(await screen.findByTestId('card-film-1'));
    expect(await screen.findByRole('dialog')).toBeVisible();
		expect(window.location.pathname).toBe('/detail/film-1');
		fireEvent.click(screen.getByRole('button', { name: /close details/i }));
		await waitFor(() => expect(window.location.pathname).toBe('/movies'));
		expect(screen.getByRole('button', { name: 'Movies' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    fireEvent.change(screen.getByLabelText(/search titles/i), { target: { value: 'Signal' } });
    await waitFor(() => expect(window.location.pathname + window.location.search).toBe('/search?q=Signal'));
    expect(screen.queryByText(/Loading local Flixr/i)).not.toBeInTheDocument();
  });
});
