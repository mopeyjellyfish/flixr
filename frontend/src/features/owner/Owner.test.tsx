import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Owner } from './Owner';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe('owner operations', () => {
  it('refreshes activity without replacing unsaved library changes', async () => {
    let activityLoads = 0;
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '/media/films', tv: '' }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: {} }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      if (path.includes('/owner/screens')) return new Response(JSON.stringify({ screens: ++activityLoads > 1 ? [{ id: 'tv', name: 'Living room', state: 'available' }] : [] }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    const films = await screen.findByDisplayValue('/media/films');
    fireEvent.change(films, { target: { value: '/media/new-films' } });
    fireEvent.click(screen.getByRole('button', { name: /refresh activity/i }));
    expect(await screen.findByText(/living room/i)).toBeVisible();
    expect(films).toHaveValue('/media/new-films');
  });

  it('offers sign in when the owner session has expired', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      if (String(input).includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      return new Response(JSON.stringify({ error: { code: 'owner_required' } }), { status: 401 });
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByRole('link', { name: /sign in to administer/i })).toHaveAttribute('href', '/login');
    expect(screen.queryByLabelText(/films root/i)).not.toBeInTheDocument();
  });

  it('reads persisted roots and configured TMDB state without exposing a stored token', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: false, ffmpeg: true } }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: { status: 'partial', scanned: 4, unmatched: 1, failed: 1 } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '/media/films', tv: '/media/tv' }));
      if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ configured: true }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByDisplayValue('/media/films')).toBeInTheDocument();
    expect(screen.getByDisplayValue('/media/tv')).toBeInTheDocument();
    expect(screen.getByText(/credential configured/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/TMDB access token/i)).toHaveValue('');
    expect(screen.queryByText(/token-/i)).not.toBeInTheDocument();
  });

  it('keeps an unlocked library root editable when the other root is environment-managed', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '/media/films', tv: '/media/tv' }));
      if (path.endsWith('/owner/settings')) return new Response(JSON.stringify({ settings: [
        { key: 'library.films_root', mutable: false }, { key: 'library.tv_root', mutable: true },
      ] }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ scan: {}, configured: false, screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByDisplayValue('/media/films')).toBeDisabled();
    expect(screen.getByDisplayValue('/media/tv')).toBeEnabled();
    expect(screen.getByRole('button', { name: /save roots/i })).toBeEnabled();
  });

  it('shows the explicit initial scan state', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ configured: false }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: { status: '', scanned: 0, unmatched: 0, failed: 0 } }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByText('No scan has started.')).toBeInTheDocument();
  });

  it('offers an owner-only local diagnostics download and explains its contents', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true }, profiles: [], films: '', tv: '', configured: false, scan: {} })));
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    const download = await screen.findByRole('link', { name: /download diagnostics/i });
    expect(download).toHaveAttribute('href', '/api/v1/owner/diagnostics');
    expect(screen.getByText(/versions, runtime health, and recent failure IDs/i)).toBeInTheDocument();
    expect(screen.getByText(/never includes media names, paths, passwords, or tokens/i)).toBeInTheDocument();
  });

  it('saves and removes a replacement TMDB token', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/profiles/ada') && init?.method === 'DELETE') return new Response(JSON.stringify({ error: { code: 'profile_failed' } }), { status: 500 });
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: { status: '', scanned: 0, unmatched: 0, failed: 0 } }));
      if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ configured: false }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ configured: false }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    const token = await screen.findByLabelText(/TMDB access token/i);
    fireEvent.change(token, { target: { value: 'replace-me' } });
    fireEvent.click(screen.getByRole('button', { name: /save TMDB credential/i }));
    await screen.findByText(/TMDB credential saved/i);
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/owner/settings/tmdb', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ token: 'replace-me' }) }));
  });

  it('confirms profile deletion and reports a deletion error', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/profiles/ada') && init?.method === 'DELETE') return new Response(JSON.stringify({ error: { code: 'profile_failed' } }), { status: 500 });
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: {} }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [{ id: 'ada', name: 'Ada', protected: false }] }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    const remove = await screen.findByRole('button', { name: /delete profile/i });
    fireEvent.click(remove);
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining("Ada"));
    expect(fetcher).not.toHaveBeenCalledWith('/api/v1/profiles/ada', expect.anything());
    confirm.mockReturnValue(true);
    fireEvent.click(remove);
    expect(await screen.findByText(/could not create that profile/i)).toBeInTheDocument();
  });

  it('edits locked local metadata and previews a provider refresh', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: {} }));
      if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ configured: true }));
      if (path.includes('/owner/metadata/unmatched')) return new Response(JSON.stringify({ items: [{ id: 'film-1', kind: 'film', title: 'Film', local_only: false, provider_id: '42', synopsis: 'Old', year: 2024 }] }));
      if (path.includes('/fields') && !path.includes('/refresh')) return new Response(JSON.stringify({ fields: [{ field: 'tags', value: 'family', source: 'local', locked: true }] }));
      if (path.includes('/refresh/preview')) return new Response(JSON.stringify({ fields: [{ field: 'synopsis', value: 'Provider text', source: 'provider', locked: false }] }));
      return new Response(JSON.stringify({ profiles: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    fireEvent.click(await screen.findByText('Edit metadata'));
    expect(await screen.findByDisplayValue('family')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /preview provider refresh/i }));
    expect(await screen.findByText(/Provider text/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /save metadata/i }));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/metadata/film/film-1/fields', expect.objectContaining({ method: 'PUT' }));
  });
});
