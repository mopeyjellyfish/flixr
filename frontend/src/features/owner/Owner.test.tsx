import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Owner, ProfileManager } from './Owner';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe('owner operations', () => {
	it('edits a profile content policy with understandable rating and tag rules', async () => {
		const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
			const path = String(input);
			if (path === '/api/v1/profiles') return new Response(JSON.stringify({ profiles: [{ id: 'child', name: 'Child', protected: false }] }));
			if (path.endsWith('/access-policy') && init?.method === 'PUT') return new Response(String(init.body));
			if (path.endsWith('/access-policy')) return new Response(JSON.stringify({ library_ids: [], rating_region: '', max_rating: '', unrated_policy: 'allow', allow_tags: [], deny_tags: [], version: 1 }));
			return new Response('{}');
		});
		render(<ProfileManager libraries={[{ id: 'films', name: 'Films', kind: 'film', locations: [] }, { id: 'kids', name: 'Kids', kind: 'film', locations: [] }]} />);
		const policy = await screen.findByRole('group', { name: /content access for child/i });
		fireEvent.click(within(policy).getByLabelText(/only selected libraries/i));
		fireEvent.click(within(policy).getByLabelText(/^kids$/i));
		fireEvent.change(within(policy).getByLabelText(/rating region/i), { target: { value: 'GB' } });
		fireEvent.change(within(policy).getByLabelText(/maximum rating/i), { target: { value: '12' } });
		fireEvent.change(within(policy).getByLabelText(/unrated titles/i), { target: { value: 'deny' } });
		fireEvent.change(within(policy).getByLabelText(/allowed tags/i), { target: { value: 'family' } });
		fireEvent.change(within(policy).getByLabelText(/blocked tags/i), { target: { value: 'scary' } });
		fireEvent.click(within(policy).getByRole('button', { name: /save content access/i }));
		await screen.findByText(/content access updated/i);
		expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/profiles/child/access-policy', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ library_ids: ['kids'], rating_region: 'GB', max_rating: '12', unrated_policy: 'deny', allow_tags: ['family'], deny_tags: ['scary'] }) }));
	});
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
      if (path.includes('/owner/libraries')) return new Response(JSON.stringify({ libraries: [{ id: 'films', name: 'Films', kind: 'film', locations: [{ id: 'films-root', library_id: 'films', path: '/media/films', root_kind: 'film', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1 }] }] }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: {} }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      if (path.includes('/owner/screens')) return new Response(JSON.stringify({ screens: ++activityLoads > 1 ? [{ id: 'tv', name: 'Living room', state: 'available' }] : [] }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    const films = await screen.findByLabelText(/add folder to films/i);
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
      if (path.includes('/owner/libraries')) return new Response(JSON.stringify({ libraries: [{ id: 'films', name: 'Films', kind: 'film', locations: [{ id: 'films-root', library_id: 'films', path: '/media/films', root_kind: 'film', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1 }] }, { id: 'tv', name: 'TV', kind: 'episode', locations: [{ id: 'tv-root', library_id: 'tv', path: '/media/tv', root_kind: 'episode', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1 }] }] }));
      if (path.includes('/settings/tmdb')) return new Response(JSON.stringify({ configured: true }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByText('/media/films')).toBeInTheDocument();
    expect(screen.getByText('/media/tv')).toBeInTheDocument();
    expect(screen.getByText(/credential configured/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/TMDB API Read Access Token/i)).toHaveValue('');
    expect(screen.queryByText(/token-/i)).not.toBeInTheDocument();
  });

  it('keeps an unlocked library root editable when the other root is environment-managed', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.includes('/owner/libraries')) return new Response(JSON.stringify({ libraries: [{ id: 'films', name: 'Films', kind: 'film', locations: [{ id: 'films-root', library_id: 'films', path: '/media/films', root_kind: 'film', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1 }] }, { id: 'tv', name: 'TV', kind: 'episode', locations: [{ id: 'tv-root', library_id: 'tv', path: '/media/tv', root_kind: 'episode', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1 }] }] }));
      if (path.endsWith('/owner/settings')) return new Response(JSON.stringify({ settings: [
        { key: 'library.films_root', mutable: false, source: 'environment' }, { key: 'library.tv_root', mutable: true, source: 'saved' },
      ] }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ scan: {}, configured: false, screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByText('Managed by environment')).toBeVisible();
    expect(within(screen.getByText('/media/films').parentElement!).queryByRole('button', { name: 'Move folder' })).not.toBeInTheDocument();
    expect(within(screen.getByText('/media/tv').parentElement!).getByRole('button', { name: 'Move folder' })).toBeEnabled();
  });

  it('surfaces and explicitly confirms an environment-staged populated root move', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/owner/libraries') && !init?.method) return new Response(JSON.stringify({ libraries: [{ id: 'films', name: 'Films', kind: 'film', locations: [{ id: 'films-root', library_id: 'films', path: '/media/films', root_kind: 'film', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1, pending_change_id: 'env-preview', pending_path: '/mnt/films', pending_change_origin: 'environment' }] }] }));
      if (path.includes('/library-location-changes/env-preview/confirm')) return new Response(JSON.stringify({ libraries: [{ id: 'films', name: 'Films', kind: 'film', locations: [{ id: 'films-root', library_id: 'films', path: '/mnt/films', root_kind: 'film', state: 'unknown', scan_complete: false, items: 0, missing: 0, updated_at: 2 }] }] }));
      if (path.endsWith('/owner/settings')) return new Response(JSON.stringify({ settings: [{ key: 'library.films_root', source: 'environment' }] }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ scan: {}, configured: false, screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByText('/mnt/films')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /review and apply/i }));
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('/mnt/films'));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/library-location-changes/env-preview/confirm', expect.objectContaining({ method: 'POST' }));
    expect(await screen.findByText(/environment-managed folder moved/i)).toBeVisible();
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
      if (path.includes('/scan/removals/confirm') && init?.method === 'POST') return new Response(JSON.stringify({ confirmed: true, locations: [{ id: 'films-root', library_id: 'films', root_kind: 'film', state: 'available', scan_complete: true, items: 0, missing: 0 }] }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: { status: 'review_required', scanned: 0, unmatched: 0, failed: 0, message: 'library removals require owner review' }, locations: [{ id: 'films-root', library_id: 'films', root_kind: 'film', state: 'review_required', scan_complete: false, items: 0, missing: 2, pending_scan_id: 'scan-1' }] }));
      return new Response(JSON.stringify({ configured: false, settings: [], screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    expect(await screen.findByText(/2 missing files/i)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /confirm removal/i }));
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('2 missing files'));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan/removals/confirm', expect.objectContaining({ method: 'POST', body: JSON.stringify({ scan_id: 'scan-1', location_id: 'films-root' }) }));
    expect(await screen.findByText(/cleanup confirmed/i)).toBeVisible();
  });

  it('previews a folder removal before applying the explicit owner confirmation', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/owner/libraries') && !init?.method) return new Response(JSON.stringify({ libraries: [{ id: 'archive', name: 'Archive', kind: 'film', locations: [{ id: 'disk-2', library_id: 'archive', path: '/media/archive', root_kind: 'film', state: 'available', scan_complete: true, items: 3, missing: 0, updated_at: 1 }] }] }));
      if (path.includes('/change-preview')) return new Response(JSON.stringify({ id: 'preview-1', location_id: 'disk-2', affected_sources: 3, affected_titles: 2 }));
      if (path.includes('/library-location-changes/preview-1/confirm')) return new Response(JSON.stringify({ libraries: [{ id: 'archive', name: 'Archive', kind: 'film', locations: [] }] }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: {} }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ configured: false, settings: [], screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /remove folder/i }));
    expect(await screen.findByText(/media files were untouched/i)).toBeVisible();
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('2 affected titles'));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/library-locations/disk-2/change-preview', expect.objectContaining({ method: 'POST', body: JSON.stringify({ path: '' }) }));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/library-location-changes/preview-1/confirm', expect.objectContaining({ method: 'POST' }));
  });

  it('discards a folder preview when the owner cancels confirmation', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/owner/libraries') && !init?.method) return new Response(JSON.stringify({ libraries: [{ id: 'archive', name: 'Archive', kind: 'film', locations: [{ id: 'disk-2', library_id: 'archive', path: '/media/archive', root_kind: 'film', state: 'available', scan_complete: true, items: 3, missing: 0, updated_at: 1 }] }] }));
      if (path.includes('/change-preview')) return new Response(JSON.stringify({ id: 'preview-1', location_id: 'disk-2', affected_sources: 3, affected_titles: 2 }));
      if (path.endsWith('/library-location-changes/preview-1') && init?.method === 'DELETE') return new Response(JSON.stringify({ libraries: [{ id: 'archive', name: 'Archive', kind: 'film', locations: [{ id: 'disk-2', library_id: 'archive', path: '/media/archive', root_kind: 'film', state: 'available', scan_complete: true, items: 3, missing: 0, updated_at: 1 }] }] }));
      if (path.includes('/scan/status')) return new Response(JSON.stringify({ scan: {} }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ configured: false, settings: [], screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /remove folder/i }));
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/library-location-changes/preview-1', expect.objectContaining({ method: 'DELETE' })));
    expect(fetcher).not.toHaveBeenCalledWith('/api/v1/owner/library-location-changes/preview-1/confirm', expect.anything());
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
      if (path.endsWith('/owner/profiles/ada/access-policy')) return new Response(JSON.stringify({ library_ids: [], rating_region: '', max_rating: '', unrated_policy: 'allow', allow_tags: [], deny_tags: [], version: 1 }));
      if (path === '/api/v1/profiles') return new Response(JSON.stringify({ profiles: [{ id: 'ada', name: 'Ada', protected: false }] }));
      return new Response('{}');
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);
    const remove = await screen.findByRole('button', { name: /delete profile/i });
    expect(await screen.findByRole('group', { name: /content access for ada/i })).toBeVisible();
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

  it('configures a per-library schedule and exposes durable scan activity', async () => {
    const fetcher=vi.spyOn(globalThis,'fetch').mockImplementation(async(input,init)=>{
      const path=String(input);
      if(path.includes('/setup/status'))return new Response(JSON.stringify({claimed:true,readiness:{ffprobe:true,ffmpeg:true}}));
      if(path.endsWith('/owner/libraries')&&!init?.method)return new Response(JSON.stringify({libraries:[{id:'films',name:'Films',kind:'film',locations:[]}]}));
      if(path.endsWith('/owner/libraries/films/scan-policy'))return new Response(JSON.stringify({policy:{library_id:'films',enabled:true,schedule_kind:'interval',interval_seconds:21600,local_time:'03:00',timezone:'UTC',next_run_at:1800000000,exclusions:['Extras/**']}}));
      if(path.endsWith('/owner/scan/jobs')&&init?.method==='POST')return new Response(JSON.stringify({job:{id:'job-2',library_id:'films',trigger:'manual',status:'queued',queued_at:1,attempt:1,scanned:0,skipped:0,failed:0,unmatched:0}}),{status:202});
      if(path.endsWith('/owner/scan/jobs'))return new Response(JSON.stringify({jobs:[{id:'job-1',library_id:'films',trigger:'schedule',status:'partial',queued_at:1,finished_at:2,attempt:1,total:2,scanned:1,skipped:1,failed:1,unmatched:0,files:[{location_id:'films-root',relative_path:'broken.mp4',outcome:'failed',message:'This file could not be inspected.',retryable:true}]}]}));
      if(path.includes('/profiles'))return new Response(JSON.stringify({profiles:[]}));
      return new Response(JSON.stringify({scan:{},settings:[],configured:false,screens:[]}));
    });
    render(<Owner onBrowse={()=>undefined} onLogout={()=>undefined}/>);
    expect(await screen.findByRole('heading',{name:/scheduled scans/i})).toBeVisible();
    expect(await screen.findByDisplayValue('Extras/**')).toBeVisible();
    expect(screen.getByText(/broken.mp4/i)).toBeVisible();
    fireEvent.click(screen.getByRole('button',{name:/run now/i}));
    await vi.waitFor(()=>expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan/jobs',expect.objectContaining({method:'POST',body:JSON.stringify({library_id:'films'})})));
  });

  it('keeps exclusions writable for environment schedules, including newly created libraries', async () => {
    let created = false;
    const policy = (libraryID: string) => ({ library_id: libraryID, enabled: true, schedule_kind: 'daily', interval_seconds: 86400, local_time: '03:00', timezone: 'Europe/London', next_run_at: 1800000000, last_success_at: 1700000000, exclusions: ['Extras/**'] });
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/owner/settings')) return new Response(JSON.stringify({ settings: [{ key: 'background.scan_schedule', source: 'environment' }] }));
      if (path.endsWith('/owner/libraries') && init?.method === 'POST') { created = true; return new Response(JSON.stringify({ id: 'archive', name: 'Archive', kind: 'film', locations: [] }), { status: 201 }); }
      if (path.endsWith('/owner/libraries')) return new Response(JSON.stringify({ libraries: [
        { id: 'films', name: 'Films', kind: 'film', locations: [] },
        ...(created ? [{ id: 'archive', name: 'Archive', kind: 'film', locations: [] }] : []),
      ] }));
      const match = path.match(/\/owner\/libraries\/([^/]+)\/scan-policy$/);
      if (match && init?.method === 'PATCH') return new Response(JSON.stringify({ policy: { ...policy(match[1]), ...JSON.parse(String(init.body)) } }));
      if (match) return new Response(JSON.stringify({ policy: policy(match[1]) }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ scan: {}, jobs: [], configured: false, settings: [], screens: [] }));
    });
    render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);

    const filmsForm = (await screen.findByRole('button', { name: /save exclusions for films/i })).closest('form')!;
    expect(within(filmsForm).getByLabelText(/enable scheduled scans/i)).toBeDisabled();
    expect(within(filmsForm).getByLabelText(/frequency/i)).toBeDisabled();
    const exclusions = within(filmsForm).getByLabelText(/excluded paths/i);
    expect(exclusions).toBeEnabled();
    expect(within(filmsForm).getByRole('button', { name: /save exclusions for films/i })).toBeEnabled();
    expect(filmsForm.querySelector('time[datetime="2023-11-14T22:13:20.000Z"]')).not.toBeNull();
    fireEvent.change(exclusions, { target: { value: 'Extras/**\nSamples/**' } });
    fireEvent.click(within(filmsForm).getByRole('button', { name: /save exclusions for films/i }));
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/libraries/films/scan-policy', expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ ...policy('films'), exclusions: ['Extras/**', 'Samples/**'] }) })));

    const createForm = screen.getByRole('heading', { name: /create library/i }).closest('form')!;
    fireEvent.change(within(createForm).getByLabelText(/^name$/i), { target: { value: 'Archive' } });
    fireEvent.click(within(createForm).getByRole('button', { name: /create library/i }));
    const archiveForm = (await screen.findByRole('button', { name: /save exclusions for archive/i })).closest('form')!;
    expect(within(archiveForm).getByLabelText(/enable scheduled scans/i)).toBeDisabled();
    expect(within(archiveForm).getByLabelText(/excluded paths/i)).toBeEnabled();
    expect(within(archiveForm).getByRole('button', { name: /save exclusions for archive/i })).toBeEnabled();
  });

  it('shows exact job timestamps and retries one selected failed file', async () => {
    const failedFile = { location_id: 'disk-a', relative_path: 'broken/movie-a.mkv', outcome: 'failed', error_code: 'probe_failed', message: 'Probe failed.', retryable: true };
    const otherFile = { location_id: 'disk-b', relative_path: 'broken/movie-b.mkv', outcome: 'failed', error_code: 'probe_failed', message: 'Probe failed.', retryable: true };
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.includes('/setup/status')) return new Response(JSON.stringify({ claimed: true, readiness: { ffprobe: true, ffmpeg: true } }));
      if (path.endsWith('/owner/libraries')) return new Response(JSON.stringify({ libraries: [{ id: 'films', name: 'Films', kind: 'film', locations: [] }] }));
      if (path.endsWith('/owner/libraries/films/scan-policy')) return new Response(JSON.stringify({ policy: { library_id: 'films', enabled: false, schedule_kind: 'interval', interval_seconds: 21600, local_time: '03:00', timezone: 'UTC', exclusions: [] } }));
      if (path.endsWith('/owner/scan/jobs/job-1/retry') && init?.method === 'POST') return new Response(JSON.stringify({ job: { id: 'retry-1', library_id: 'films', trigger: 'retry', status: 'queued', queued_at: 1700000180, attempt: 2, scanned: 0, skipped: 0, failed: 0, unmatched: 0 } }), { status: 202 });
      if (path.endsWith('/owner/scan/jobs')) return new Response(JSON.stringify({ jobs: [{ id: 'job-1', library_id: 'films', trigger: 'schedule', status: 'partial', queued_at: 1700000000, started_at: 1700000060, finished_at: 1700000120, attempt: 1, total: 2, scanned: 0, skipped: 0, failed: 2, unmatched: 0, files: [failedFile, otherFile] }] }));
      if (path.includes('/profiles')) return new Response(JSON.stringify({ profiles: [] }));
      return new Response(JSON.stringify({ scan: {}, configured: false, settings: [], screens: [] }));
    });
    const { container } = render(<Owner onBrowse={() => undefined} onLogout={() => undefined} />);

    expect(await screen.findByText('broken/movie-a.mkv', { exact: false })).toBeVisible();
    for (const timestamp of ['2023-11-14T22:13:20.000Z', '2023-11-14T22:14:20.000Z', '2023-11-14T22:15:20.000Z']) {
      expect(container.querySelector(`time[datetime="${timestamp}"]`)).not.toBeNull();
    }
    fireEvent.click(screen.getByRole('button', { name: /retry broken\/movie-a\.mkv from disk-a/i }));
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan/jobs/job-1/retry', expect.objectContaining({ method: 'POST', body: JSON.stringify({ files: [failedFile] }) })));
    fireEvent.click(screen.getByRole('button', { name: /retry failed files/i }));
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/scan/jobs/job-1/retry', expect.objectContaining({ method: 'POST', body: JSON.stringify({ files: [failedFile, otherFile] }) })));
  });
});
