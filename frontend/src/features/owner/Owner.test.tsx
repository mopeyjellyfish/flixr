import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Owner } from './Owner';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe('owner operations', () => {
	it('shows required TMDB attribution in credits', async () => {
		vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true }, profiles: [], films: '', tv: '', configured: false, enabled: true, source: 'none', state: 'unavailable', message: 'Unavailable', scan: {}, screens: [] })));
		render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
		expect(await screen.findByRole('img', { name: /TMDB/i })).toHaveAttribute('src', '/tmdb-logo.svg');
		expect(screen.getByText(/uses the TMDB API but is not endorsed or certified by TMDB/i)).toBeVisible();
		expect(screen.getByRole('link', { name: /visit TMDB/i })).toHaveAttribute('href', 'https://www.themoviedb.org');
	});
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
    expect(screen.getByLabelText(/TMDB API Read Access Token/i)).toHaveValue('');
    expect(screen.queryByText(/token-/i)).not.toBeInTheDocument();
  });

  it('keeps an unlocked library root editable when the other root is environment-managed', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '/media/films', tv: '/media/tv' }));
      if (path.endsWith('/owner/settings')) return new Response(JSON.stringify({ settings: [
        { key: 'library.films_root', mutable: false, source: 'environment' }, { key: 'library.tv_root', mutable: true, source: 'saved' },
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

  it('requires explicit confirmation before cleaning up suspicious removals', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '/media/films', tv: '' }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      if (path.includes('/scan/removals/confirm') && init?.method === 'POST') return new Response(JSON.stringify({ confirmed: true, locations: [{ root_kind: 'film', state: 'available', scan_complete: true, items: 0, missing: 0 }] }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: { status: 'review_required', scanned: 0, unmatched: 0, failed: 0, message: 'library removals require owner review' }, locations: [{ root_kind: 'film', state: 'review_required', scan_complete: false, items: 0, missing: 2, pending_scan_id: 'scan-1' }] }));
      return new Response(JSON.stringify({ configured: false, settings: [], screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByText(/2 missing files/i)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /confirm removal/i }));
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('2 missing files'));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan/removals/confirm', expect.objectContaining({ method: 'POST', body: JSON.stringify({ scan_id: 'scan-1', root_kind: 'film' }) }));
    expect(await screen.findByText(/cleanup confirmed/i)).toBeVisible();
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
      if (path.includes('/owner/scan')) return new Response(JSON.stringify({ scan: { status: 'running', scanned: 0, unmatched: 0, failed: 0 } }), { status: 202 });
      if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ provider: 'tmdb', configured: true, state: 'configured', message: 'TMDB is configured.' }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ configured: false }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    const token = await screen.findByLabelText(/TMDB API Read Access Token/i);
    fireEvent.change(token, { target: { value: 'replace-me' } });
    fireEvent.click(screen.getByRole('button', { name: /save TMDB credential/i }));
    await screen.findByText(/credential verified.*enrichment is running/i);
    expect(screen.getByText(/provider status: running/i)).toBeVisible();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/settings/tmdb', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ token: 'replace-me' }) }));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan', expect.objectContaining({ method: 'POST' }));
		fireEvent.click(screen.getByLabelText(/use online metadata/i));
		expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/settings/tmdb', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ enabled: false }) }));
  });

  it('shows actionable provider failure state', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: { status: 'partial', scanned: 3, unmatched: 0, failed: 1 } }));
      if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ provider: 'tmdb', configured: true, state: 'failed', message: 'Metadata failed for some titles. Check the credential or connection, then start another scan.' }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByText(/metadata failed for some titles/i)).toBeVisible();
    expect(screen.getByText(/provider status: failed/i)).toBeVisible();
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

  it('requires an explicit survivor before merging an identity conflict and reports its affected titles', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/owner/identity/repairs')) return new Response(JSON.stringify({ conflicts: [{ id: 7, kind: 'film', reason: 'The titles claim the same provider identity.', state: 'open', left: { id: 'film-a', title: 'Arrival', kind: 'film', local_only: false }, right: { id: 'film-b', title: 'Arrival (duplicate)', kind: 'film', local_only: false } }], merges: [] }));
      if (path.endsWith('/owner/identity/merges') && init?.method === 'POST') return new Response(JSON.stringify({ id: 'merge-7', kind: 'film', state: 'active', survivor: { id: 'film-a', title: 'Arrival', kind: 'film', local_only: false }, source: { id: 'film-b', title: 'Arrival (duplicate)', kind: 'film', local_only: false }, decisions: ['Preserved source history for unmerge.'] }));
      return new Response(JSON.stringify({ profiles: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);

    expect(await screen.findByRole('heading', { name: /identity repair/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /merge selected titles/i })).toBeDisabled();
    fireEvent.click(screen.getByRole('radio', { name: /^keep arrival; merge arrival \(duplicate\) into it$/i }));
    fireEvent.click(screen.getByRole('button', { name: /merge selected titles/i }));

    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('Merge Arrival (duplicate) into Arrival?'));
    expect(fetcher).not.toHaveBeenCalledWith('/api/v1/owner/identity/merges', expect.anything());
    confirm.mockReturnValue(true);
    fireEvent.click(screen.getByRole('button', { name: /merge selected titles/i }));

    expect(await screen.findByText(/identity repair merged arrival and arrival \(duplicate\)/i)).toBeInTheDocument();
    expect(screen.getAllByText(/affected titles: arrival and arrival \(duplicate\)/i)).toHaveLength(2);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/identity/merges', expect.objectContaining({ method: 'POST', body: JSON.stringify({ kind: 'film', survivor_id: 'film-a', source_id: 'film-b' }) }));
  });

  it('shows active merge decisions and makes unmerge an explicit action', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/owner/identity/repairs')) return new Response(JSON.stringify({ conflicts: [], merges: [{ id: 'merge-7', kind: 'film', state: 'active', survivor: { id: 'film-a', title: 'Arrival', kind: 'film', local_only: false }, source: { id: 'film-b', title: 'Arrival (duplicate)', kind: 'film', local_only: false }, decisions: ['Retained newer survivor progress.'] }] }));
      if (path.endsWith('/owner/identity/merges/merge-7/unmerge') && init?.method === 'POST') return new Response(JSON.stringify({ id: 'merge-7', kind: 'film', state: 'unmerged', survivor: { id: 'film-a', title: 'Arrival', kind: 'film', local_only: false }, source: { id: 'film-b', title: 'Arrival (duplicate)', kind: 'film', local_only: false }, decisions: ['Retained newer survivor progress.'] }));
      return new Response(JSON.stringify({ profiles: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);

    expect(await screen.findByText(/retained newer survivor progress/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /unmerge arrival and arrival \(duplicate\)/i }));
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('Unmerge Arrival and Arrival (duplicate)?'));
    expect(fetcher).not.toHaveBeenCalledWith('/api/v1/owner/identity/merges/merge-7/unmerge', expect.anything());
    confirm.mockReturnValue(true);
    fireEvent.click(screen.getByRole('button', { name: /unmerge arrival and arrival \(duplicate\)/i }));
    expect(await screen.findByText(/unmerged arrival and arrival \(duplicate\)/i)).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/identity/merges/merge-7/unmerge', expect.objectContaining({ method: 'POST' }));
  });

  it('reports a stale identity repair after a version conflict and offers a refresh', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/roots')) return new Response(JSON.stringify({ films: '', tv: '' }));
      if (path.includes('/owner/identity/repairs')) return new Response(JSON.stringify({ conflicts: [{ id: 7, kind: 'film', reason: 'provider_identity', state: 'open', left: { id: 'film-a', title: 'Arrival', kind: 'film', local_only: false }, right: { id: 'film-b', title: 'Arrival (duplicate)', kind: 'film', local_only: false } }], merges: [] }));
      if (path.endsWith('/owner/identity/merges') && init?.method === 'POST') return new Response(JSON.stringify({ error: { code: 'identity_conflict' } }), { status: 409 });
      return new Response(JSON.stringify({ profiles: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);

    fireEvent.click(await screen.findByRole('radio', { name: /^keep arrival; merge arrival \(duplicate\) into it$/i }));
    fireEvent.click(screen.getByRole('button', { name: /merge selected titles/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(/repair list changed before this merge/i);
    expect(screen.getByRole('button', { name: /refresh repair list/i })).toBeInTheDocument();
  });
});
