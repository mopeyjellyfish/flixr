import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Owner } from './Owner';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe('owner operations', () => {
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
