import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './app/App';

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/'); });
describe('Flixr routes', () => {
  it('claims the first owner into the authenticated owner route', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(JSON.stringify({ claimed: false, readiness: { ffprobe: false, ffmpeg: false } })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ claimed: true }), { status: 201 }));
    render(<App />);
    fireEvent.change(await screen.findByLabelText(/setup token/i), { target: { value: 'one-time-token' } });
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: 'safe password' } });
    fireEvent.click(screen.getByRole('button', { name: /claim flixr/i }));
    await waitFor(() => expect(fetcher).toHaveBeenLastCalledWith('/api/v1/setup/claim', expect.objectContaining({ method: 'POST' })));
    expect(await screen.findByRole('heading', { name: /keep your local cinema ready/i })).toBeInTheDocument();
  });


  it('loads the lazy browse route and updates detail state when history moves', async () => {
    window.history.replaceState({}, '', '/home');
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/catalog/films/film-1')) return new Response(JSON.stringify({ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }));
      return new Response(JSON.stringify({ items: [{ id: 'film-1', title: 'Signal', kind: 'film', local_only: true }], total: 1, next: null }));
    });
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new Event('close')); };
    render(<App />);
    fireEvent.click(await screen.findByTestId('card-film-1'));
    expect(await screen.findByRole('dialog')).toBeVisible();
    expect(window.location.pathname).toBe('/detail/film-1');
    window.history.pushState({}, '', '/home');
    window.dispatchEvent(new PopStateEvent('popstate'));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    fireEvent.change(screen.getByLabelText(/search titles/i), { target: { value: 'Signal' } });
    await waitFor(() => expect(window.location.pathname + window.location.search).toBe('/search?q=Signal'));
    expect(screen.queryByText(/Loading local Flixr/i)).not.toBeInTheDocument();
  });
});
