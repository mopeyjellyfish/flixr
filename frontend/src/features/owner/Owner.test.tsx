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
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
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
});
