import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { Setup } from './Setup';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it('validates an optional TMDB credential before starting the first normal-mode scan', async () => {
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('/owner/roots')) return new Response(JSON.stringify({ saved: true }));
    if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ provider: 'tmdb', configured: true, state: 'configured', message: 'TMDB is configured.' }));
    if (path.includes('/owner/scan')) return new Response(JSON.stringify({ scan: { status: 'running', scanned: 0, failed: 0, unmatched: 0 } }), { status: 202 });
    return new Response('{}');
  });
  render(<Setup readiness={{ ffprobe: true, ffmpeg: true }} initialRoots={{ films: '/media/films', tv: '' }} onCompleted={() => undefined} />);
  fireEvent.change(screen.getByLabelText(/TMDB API Read Access Token/i), { target: { value: 'credential' } });
  fireEvent.click(screen.getByRole('button', { name: /save libraries/i }));
  expect(await screen.findByText(/set up your first profile/i)).toBeVisible();
  expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/settings/tmdb', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ token: 'credential' }) }));
  expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan', expect.objectContaining({ method: 'POST' }));
});
