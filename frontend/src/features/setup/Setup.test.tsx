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

it('explains automatic metadata access and keeps personal credentials advanced', () => {
	render(<Setup readiness={{ ffprobe: true, ffmpeg: true }} metadata={{ provider: 'tmdb', enabled: true, configured: true, source: 'application', state: 'configured', message: 'Automatic TMDB access is available.' }} initialRoots={{ films: '/media/films', tv: '' }} initialStep="libraries" onCompleted={() => undefined} />);
	expect(screen.getByText(/automatic TMDB access is available/i)).toBeVisible();
	expect(screen.getByText(/advanced: personal TMDB credential/i)).toBeVisible();
});

it('offers an accessible fresh setup choice while clearly reserving server import', async () => {
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.endsWith('/setup/claim')) return new Response(JSON.stringify({ claimed: true }), { status: 201 });
    if (path.endsWith('/owner/setup') && init?.method === 'PATCH') return new Response(JSON.stringify({ step: 'libraries' }));
    if (path.includes('/owner/setup?')) return new Response(JSON.stringify({ step: 'libraries', checks: [] }));
    throw new Error(`Unexpected request ${init?.method ?? 'GET'} ${path}`);
  });
  render(<Setup readiness={{ ffprobe: true, ffmpeg: true }} onCompleted={() => undefined} />);
  fireEvent.change(screen.getByLabelText(/setup token/i), { target: { value: 'one-time-token' } });
  fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: 'safe password' } });
  fireEvent.click(screen.getByRole('button', { name: /secure this server/i }));

  expect(await screen.findByRole('heading', { name: /how would you like to begin/i })).toBeVisible();
  const unavailableImport = screen.getByRole('button', { name: /import an existing server/i });
  expect(unavailableImport).toHaveAttribute('aria-disabled', 'true');
  expect(screen.getByText(/will become available in a future release/i)).toBeVisible();
  expect(screen.queryByLabelText(/films library/i)).not.toBeInTheDocument();

  fireEvent.click(unavailableImport);
  expect(screen.getByRole('heading', { name: /how would you like to begin/i })).toBeVisible();

  fireEvent.click(screen.getByRole('button', { name: /start fresh/i }));
  expect(await screen.findByLabelText(/films library/i)).toHaveAttribute('placeholder', '/media/films');
  expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/setup', expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ step: 'libraries' }) }));
});

it('shows owner-only container path failures and rechecks corrected folders before saving', async () => {
  let checks = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/owner/setup?')) {
      checks += 1;
      const state = checks === 1 ? 'missing' : 'ready';
      return new Response(JSON.stringify({ step: 'libraries', checks: [
        { id: 'films', label: 'Films', state, path: '/media/films', message: state === 'missing' ? 'This folder does not exist inside the Flixr container.' : 'Flixr can traverse and read this container folder.', action: state === 'missing' ? 'Add or correct the read-only media volume in your Compose file, then recheck this container path.' : '' },
        { id: 'ffprobe', label: 'Media inspection', state: 'ready', message: 'ffprobe is available in the Flixr container.' },
      ] }));
    }
    if (path.endsWith('/owner/roots')) return new Response(JSON.stringify({ saved: true }));
    if (path.endsWith('/owner/scan')) return new Response(JSON.stringify({ scan: { status: 'running' } }), { status: 202 });
    if (path.endsWith('/owner/setup') && init?.method === 'PATCH') return new Response(JSON.stringify({ step: 'profile' }));
    throw new Error(`Unexpected request ${init?.method ?? 'GET'} ${path}`);
  });
  render(<Setup readiness={{ ffprobe: true, ffmpeg: true }} initialRoots={{ films: '/media/films', tv: '' }} initialStep="libraries" onCompleted={() => undefined} />);

  fireEvent.click(screen.getByRole('button', { name: /recheck folders and tools/i }));
  expect(await screen.findByRole('alert')).toHaveTextContent(/does not exist inside the Flixr container/i);
  expect(screen.getByText(/correct the read-only media volume in your Compose file/i)).toBeVisible();
  expect(screen.getByText('/media/films')).toBeVisible();

  fireEvent.click(screen.getByRole('button', { name: /recheck folders and tools/i }));
  expect(await screen.findByText(/can traverse and read this container folder/i)).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: /save libraries/i }));
  expect(await screen.findByRole('heading', { name: /first profile/i })).toBeVisible();
});

it('persists skip and back navigation without creating accounts or libraries', async () => {
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (path.includes('/owner/setup?')) return new Response(JSON.stringify({ step: 'libraries', checks: [] }));
    if (path.endsWith('/owner/setup') && init?.method === 'PATCH') return new Response(init.body);
    throw new Error(`Unexpected request ${init?.method ?? 'GET'} ${path}`);
  });
  render(<Setup readiness={{ ffprobe: true, ffmpeg: true }} initialRoots={{ films: '', tv: '' }} initialStep="libraries" onCompleted={() => undefined} />);
  fireEvent.click(screen.getByRole('button', { name: /skip for now/i }));
  expect(await screen.findByRole('heading', { name: /first profile/i })).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: /back to libraries/i }));
  expect(await screen.findByLabelText(/films library/i)).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: /back to setup choices/i }));
  expect(await screen.findByRole('heading', { name: /how would you like to begin/i })).toBeVisible();
  expect(fetcher).not.toHaveBeenCalledWith('/api/v1/owner/roots', expect.anything());
  expect(fetcher).not.toHaveBeenCalledWith('/api/v1/profiles', expect.anything());
});
