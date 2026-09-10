/** Component/visual-state suite. It uses mocked API responses and is not production acceptance evidence. */
import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const viewports = [{ name: 'tv', width: 1920, height: 1080 }, { name: 'desktop', width: 1440, height: 900 }, { name: 'tablet', width: 1024, height: 768 }, { name: 'phone', width: 390, height: 844 }];
type JSONValue = Record<string, unknown>;
type MockResponse = { status?: number; json: JSONValue };
type ScanPolicyFixture = { library_id: string; enabled: boolean; schedule_kind: 'interval' | 'daily'; interval_seconds: number; local_time: string; timezone: string; exclusions: string[] };
const ready = { claimed: true, readiness: { ffprobe: true, ffmpeg: true } };
const posterArt = `data:image/svg+xml;base64,${Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 600"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#183a82"/><stop offset="1" stop-color="#05070c"/></linearGradient></defs><rect width="400" height="600" fill="url(#g)"/><circle cx="295" cy="160" r="105" fill="#5b8cff" opacity=".52"/><path d="M0 430L230 250l170 155v195H0z" fill="#101623" opacity=".82"/></svg>').toString('base64')}`;
const backdropArt = `data:image/svg+xml;base64,${Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900"><defs><radialGradient id="g"><stop stop-color="#5b8cff"/><stop offset="1" stop-color="#05070c"/></radialGradient></defs><rect width="1600" height="900" fill="#05070c"/><ellipse cx="1180" cy="330" rx="520" ry="380" fill="url(#g)" opacity=".58"/><path d="M580 900L1100 330l500 430v140z" fill="#101623" opacity=".85"/></svg>').toString('base64')}`;
const brightBackdropArt = `data:image/svg+xml;base64,${Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900"><rect width="1600" height="900" fill="#f7d96b"/><circle cx="1180" cy="280" r="360" fill="#f6f8ff"/><path d="M500 900L1100 260l500 500v140z" fill="#5b8cff"/></svg>').toString('base64')}`;
const film = { id: 'film-1', title: 'Cobalt Sky', kind: 'film', year: 2024, synopsis: 'A small local signal.', local_only: false, poster: posterArt, backdrop: backdropArt, container: 'mp4', video_codec: 'h264', video_profile: 'High', video_level: 40, width: 1920, height: 1080, bitrate: 5_000_000, frame_rate_milli: 30_000, bit_depth: 8, audio: [{ codec: 'aac', profile: 'LC', channels: 2, sample_rate: 48_000, bitrate: 128_000 }] };
const series = { id: 'series-1', title: 'Night Relay', kind: 'series', year: 2023, synopsis: 'Episodes from a local relay.', local_only: true, poster: posterArt };
const ownerLibrariesResponse: MockResponse = { json: { libraries: [
  { id: 'films', name: 'Films', kind: 'film', locations: [{ id: 'films-root', library_id: 'films', path: '/media/films', root_kind: 'film', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1 }] },
  { id: 'tv', name: 'TV', kind: 'episode', locations: [{ id: 'tv-root', library_id: 'tv', path: '/media/tv', root_kind: 'episode', state: 'available', scan_complete: true, items: 1, missing: 0, updated_at: 1 }] },
] } };
const scanPolicyResponse = (libraryID: string): MockResponse => ({ json: { policy: { library_id: libraryID, enabled: false, schedule_kind: 'interval', interval_seconds: 86400, local_time: '03:00', timezone: 'UTC', exclusions: [] } satisfies ScanPolicyFixture } });

async function mock(page: Page, handler: (path: string, method: string, query: string, body?: Record<string, unknown>) => MockResponse | undefined) {
  // New routes take precedence. Keep interception installed while changing states
  // so an in-flight request cannot escape to the real development proxy.
  await page.route('**/api/v1/**', (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const response = handler(url.pathname, request.method(), url.search, request.postDataJSON() as Record<string, unknown> | undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/screens' ? { json: { screens: [] } } : undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/owner/screens' ? { json: { screens: [] } } : undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/owner/sessions' ? { json: { sessions: [] } } : undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/owner/media-version-groups' ? { json: { groups: [], candidates: [], total: 0 } } : undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/owner/libraries' ? ownerLibrariesResponse : undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/owner/scan/jobs' ? { json: { jobs: [] } } : undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/owner/backups' ? { json: { policy: { enabled: false, destination: '', schedule_kind: 'interval', interval_seconds: 86400, local_time: '03:00', timezone: 'UTC', retain_count: 7, retain_age_seconds: 2592000, budget_bytes: 10737418240, last_status: 'never' }, jobs: [] } } : undefined)
      ?? (request.method() === 'GET' && /^\/api\/v1\/owner\/libraries\/[^/]+\/scan-policy$/.test(url.pathname) ? scanPolicyResponse(url.pathname.split('/').at(-2) ?? '') : undefined)
      ?? (request.method() === 'GET' && url.pathname.startsWith('/api/v1/ratings/') ? { json: { rating: null } } : undefined)
      ?? (request.method() === 'GET' && url.pathname === '/api/v1/history' ? { json: { events: [] } } : undefined);
    if (!response) return route.fulfill({ status: 599, json: { error: { code: 'unexpected_test_request' }, request: { path: url.pathname, method: request.method(), query: url.search } } });
    return route.fulfill({ status: response.status ?? 200, json: response.json });
  });
}

test('mocked owner configures and runs a verified backup', async ({ page }, testInfo) => {
  await mock(page, (path, method, _query, body) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/owner/roots')) return { json: { films: '', tv: '' } };
    if (path.endsWith('/settings/tmdb')) return { json: { configured: false } };
    if (path.endsWith('/owner/metadata/unmatched')) return { json: { items: [] } };
    if (path.endsWith('/owner/identity/repairs')) return { json: { conflicts: [], merges: [] } };
    if (path.endsWith('/owner/settings')) return { json: { settings: [] } };
    if (path.endsWith('/settings/playback')) return { json: { segment_dir: '/cache/segments', generation_bytes: 1, global_bytes: 1, max_generations: 1 } };
    if (path.endsWith('/playback/status')) return { json: { settings: { segment_dir: '/cache/segments', generation_bytes: 1, global_bytes: 1, max_generations: 1 }, generations: [] } };
    if (path.endsWith('/scan/status')) return { json: { scan: {} } };
    if (path.endsWith('/profiles')) return { json: { profiles: [] } };
    if (path.endsWith('/owner/backups/policy') && method === 'PUT') return { json: { policy: body! } };
    if (path.endsWith('/owner/backups/jobs') && method === 'POST') return { status: 202, json: { job: { id: 'backup-1', trigger: 'manual', status: 'queued', queued_at: 1 } } };
    return undefined;
  });
  await open(page, '/owner'); await page.getByLabel('Backup folder').fill('/backups/flixr'); await page.getByRole('button', { name: /save backup policy/i }).click(); await expect(page.getByText('Backup policy saved.')).toBeVisible();
  const runBackup = page.getByRole('button', { name: /back up now/i });
  await expect(runBackup).toBeEnabled();
  await runBackup.click(); await expect(page.getByText(/manual backup/i)).toBeVisible(); await check(page, []); await page.locator('#backups').screenshot({ path: testInfo.outputPath('owner-backups.png') });
  await page.screenshot({ path: testInfo.outputPath('owner-desktop.png'), fullPage: true });
  // Phone layout: the section strip stays reachable, nothing overflows, and forms stack.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('navigation', { name: 'Server settings' }).getByRole('link', { name: 'Backups' })).toBeVisible();
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).resolves.toBeTruthy();
  await page.getByRole('navigation', { name: 'Server settings' }).getByRole('link', { name: 'Libraries' }).click();
  await expect(page.getByRole('navigation', { name: 'Server settings' })).toBeInViewport();
  await check(page, []);
  await page.screenshot({ path: testInfo.outputPath('owner-phone.png'), fullPage: true });
});
async function open(page: Page, path: string) {
  const status = page.waitForResponse((response) => response.url().includes('/api/v1/setup/status'));
  await page.goto('/');
  await status;
  await page.waitForFunction(() => !document.body.textContent?.includes('Loading local Flixr…'));
  if (path !== '/') await page.evaluate((nextPath) => { window.history.pushState({}, '', nextPath); window.dispatchEvent(new PopStateEvent('popstate')); }, path);
  await expect(page.locator('[data-app-content]')).not.toHaveAttribute('inert');
}
async function check(page: Page, errors: string[]) {
  // Let entrance animations finish so axe measures settled colours, not mid-fade blends.
  await page.evaluate(() => Promise.all(document.getAnimations().filter((animation) => animation.effect?.getTiming().iterations !== Infinity).map((animation) => animation.finished.catch(() => undefined))));
  const results = await new AxeBuilder({ page }).analyze();
  expect(results.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  // Axe and layout checks apply to every route, including routes without a hero.
  expect(errors).toEqual([]);
}

for (const viewport of viewports) {
  test(`mocked delivery-unit states: ${viewport.name}`, async ({ page }, testInfo) => {
    test.setTimeout(90_000);
    await page.setViewportSize(viewport);
    const errors: string[] = [];
    page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
    page.on('pageerror', (error) => errors.push(error.message));

    let claimed = false;
    let scanStarts = 0;
    let profileCreates = 0;
    let profileSelections = 0;
    await mock(page, (path, method) => {
      if (path.endsWith('/setup/status')) return { json: { claimed, readiness: { ffprobe: false, ffmpeg: false } } };
      if (path.endsWith('/setup/claim')) { claimed = true; return { json: { claimed: true } }; }
      if (path.endsWith('/owner/setup') && method === 'PATCH') return { json: { step: 'libraries' } };
      if (path.endsWith('/owner/setup')) return { json: { step: 'libraries', checks: [] } };
      if (path.endsWith('/owner/roots')) return { json: { saved: true } };
      if (path.endsWith('/owner/scan')) { scanStarts += 1; return { json: { scan: { status: 'running', scanned: 0, unmatched: 0, failed: 0 } } }; }
      if (path.endsWith('/profiles') && method === 'POST') { profileCreates += 1; return { json: { id: 'first-run-viewer', name: 'First-run viewer', protected: false } }; }
      if (path.endsWith('/profiles/first-run-viewer/select')) { profileSelections += 1; return { json: { selected: true } }; }
      if (path.endsWith('/catalog/home')) return { json: { items: [film], total: 1, next: null } };
      if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [film] }, { name: 'My List', items: [] }] } };
      if (path.endsWith('/profiles')) return { json: { profiles: [] } };
      return undefined;
    });
    await open(page, '/');
    await expect(page.getByText(/ffprobe is unavailable/i)).toBeVisible();
    await expect(page.getByText(/direct play can continue/i)).toBeVisible();
    await expect(page.getByRole('button', { name: /secure this server/i })).toBeVisible();
    await check(page, errors);
    await page.getByLabel(/setup token/i).fill('first-run-token');
    await page.getByLabel(/owner password/i).fill('safe owner password');
    await page.getByRole('button', { name: /secure this server/i }).click();
    await expect(page.getByRole('heading', { name: /how would you like to begin/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /import an existing server/i })).toHaveAttribute('aria-disabled', 'true');
    await page.getByRole('button', { name: /start fresh/i }).click();
    await expect(page.getByRole('heading', { name: /bring your libraries home/i })).toBeVisible();
    const personalCredential = page.getByLabel(/TMDB API Read Access Token/i);
    await expect(personalCredential).toBeHidden();
    await page.getByText(/advanced: personal TMDB credential/i).click();
    await expect(personalCredential).toBeVisible();
    await expect(page.getByRole('button', { name: /save libraries/i })).toBeVisible();
    await check(page, errors);
    await page.getByLabel(/films library/i).fill('/media/films');
    await page.getByRole('button', { name: /save libraries/i }).click();
    await expect(page.getByRole('heading', { name: /profile/i })).toBeVisible();
    await expect(page.getByRole('status')).toContainText(/direct play still works/i);
    expect(scanStarts).toBe(0);
    await expect(page.getByRole('button', { name: /create profile/i })).toBeVisible();
    await check(page, errors);
    await page.getByLabel(/^name$/i).fill('First-run viewer');
    await page.getByRole('button', { name: /create profile/i }).click();
    await expect(page.getByRole('heading', { name: 'Cobalt Sky' })).toBeVisible();
    expect(profileCreates).toBe(1);
    expect(profileSelections).toBe(1);
    await check(page, errors);
    let pinAttempts = 0;
    await mock(page, (path) => {
      if (path.endsWith('/setup/status')) return { json: ready };
      if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'protected', name: 'Protected', protected: true }] } };
      if (path.endsWith('/select')) { pinAttempts += 1; return pinAttempts === 1 ? { status: 401, json: { error: { code: 'invalid_pin' } } } : { status: 429, json: { error: { code: 'pin_rate_limited' } } }; }
      return undefined;
    });
    await open(page, '/');
    await page.getByRole('button', { name: /protected/i }).click();
    // Pasting into the first cell fills the rest; the fourth digit submits.
    await page.getByLabel(/profile PIN digit 1 of 4/i).fill('0000');
    await expect(page.getByRole('alert')).toContainText(/not valid/i);
    await page.getByLabel(/profile PIN digit 1 of 4/i).fill('0000');
    await expect(page.getByRole('alert')).toContainText(/too many/i);
    // The mocked 401 and 429 responses are logged as console errors by design.
    errors.length = 0;
    await check(page, errors);

    await mock(page, (path, _method, query) => {
      if (path.endsWith('/setup/status')) return { json: ready };
      if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
      if (path.endsWith('/select')) return { json: {} };
      if (path.endsWith('/catalog/home')) return { json: { items: [film, series], total: 2, next: null } };
      if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [film] }, { name: 'New', items: [series] }, { name: 'My List', items: [] }] } };
      if (path.endsWith('/catalog/items/film-1')) return { json: film };
      if (path.endsWith('/catalog/items/series-1')) return { json: series };
      if (path.endsWith('/catalog/series/series-1')) return { json: { ...series, seasons: [{ id: 'season-1', number: 1, episodes: [{ id: 'episode-1', title: 'First Signal', kind: 'episode', season: 1, episode: 1, local_only: true }] }] } };
      if (path.endsWith('/catalog/search')) return { json: { items: query.includes('relay') ? [series] : [], total: query.includes('relay') ? 1 : 0, next: null } };
      return undefined;
    });
    await open(page, '/');
    await page.getByRole('button', { name: 'Viewer' }).click();
    await expect(page.getByRole('heading', { name: 'Night Relay' })).toBeVisible();
    await page.getByTestId('card-film-1').focus();
    await page.getByTestId('card-film-1').press('ArrowDown');
    await expect(page.getByTestId('card-series-1')).toBeFocused();
    await page.getByTestId('card-series-1').press('ArrowUp');
    await page.getByTestId('card-film-1').press('ArrowUp');
    await expect(page.getByRole('button', { name: 'Home' })).toBeFocused();
    await page.getByTestId('card-film-1').click();
    await expect(page.getByRole('dialog')).toContainText(/mp4/i);
    await page.getByRole('button', { name: /close details/i }).click();
    await page.getByTestId('card-series-1').click();
    await expect(page.getByRole('dialog')).toContainText(/First Signal.*Season 1 · Episode 1/i);
    await page.getByRole('button', { name: /close details/i }).click();
    await check(page, errors);
    await page.screenshot({ path: testInfo.outputPath('populated-home.png'), fullPage: true });
    await open(page, '/search?q=missing');
    await expect(page.getByRole('heading', { name: /no matching titles/i })).toBeVisible();
    await open(page, '/search?q=relay');
    await expect(page.getByTestId('card-series-1')).toBeVisible();

    await mock(page, (path, method) => {
      if (path.endsWith('/setup/status')) return { json: { claimed: true, readiness: { ffprobe: false, ffmpeg: true } } };
      if (path.endsWith('/owner/roots')) return { json: { films: '/media/films', tv: '/media/tv' } };
      if (path.endsWith('/owner/settings')) return { json: { settings: [
        { key: 'library.films_root', category: 'Libraries', scope: 'server', default: '', environment: 'FLIXR_FILMS_ROOT', file_secret: '', persistence: 'database', restart: 'immediate', valid: 'empty or existing directory', secret: false, advanced: false, value: '/media/films', source: 'saved', mutable: true },
        { key: 'library.tv_root', category: 'Libraries', scope: 'server', default: '', environment: 'FLIXR_TV_ROOT', file_secret: '', persistence: 'database', restart: 'immediate', valid: 'empty or existing directory', secret: false, advanced: false, value: '/media/tv', source: 'saved', mutable: true },
        { key: 'metadata.tmdb_token', category: 'Metadata', scope: 'server', default: '', environment: 'FLIXR_TMDB_TOKEN', file_secret: 'FLIXR_TMDB_TOKEN_FILE', persistence: 'database', restart: 'immediate', valid: 'provider token', secret: true, advanced: false, value: 'configured', source: 'saved', mutable: false },
        { key: 'playback.segment_dir', category: 'Playback', scope: 'server', default: '/tmp/flixr-segments', environment: 'FLIXR_SEGMENT_DIR', file_secret: '', persistence: 'sidecar and database', restart: 'restart', valid: 'absolute, Flixr-owned directory', secret: false, advanced: false, value: '/tmp/flixr-segments', source: 'environment', mutable: false },
        { key: 'playback.global_bytes', category: 'Playback', scope: 'server', default: '536870912', environment: 'FLIXR_GLOBAL_BYTES', file_secret: '', persistence: 'database', restart: 'immediate', valid: 'at least generation bytes', secret: false, advanced: false, value: '536870912', source: 'saved', mutable: true },
      ] } };
      if (path.endsWith('/settings/tmdb')) return { json: { configured: true } };
      if (path.endsWith('/owner/metadata/unmatched')) return { json: { items: [{ ...film, provider_id: '42' }] } };
      if (path.endsWith('/owner/identity/repairs') && method === 'GET') return { json: { conflicts: [], merges: [] } };
      if (path.endsWith('/metadata/film/film-1/fields') && method === 'GET') return { json: { fields: [{ field: 'tags', value: 'family', source: 'local', locked: true }] } };
      if (path.endsWith('/metadata/film/film-1/refresh/preview')) return { json: { fields: [{ field: 'synopsis', value: 'Provider refresh', source: 'provider', locked: false }] } };
      if (path.endsWith('/settings/playback')) return { json: { segment_dir: '/tmp/flixr-segments', generation_bytes: 268435456, global_bytes: 536870912, max_generations: 2 } };
      if (path.endsWith('/playback/status')) return { json: { settings: { segment_dir: '/tmp/flixr-segments', generation_bytes: 268435456, global_bytes: 536870912, max_generations: 2 }, generations: [] } };
      if (path.endsWith('/scan/status')) return { json: { scan: { status: 'partial', scanned: 2, unmatched: 1, failed: 1 } } };
      if (path.endsWith('/profiles')) return { json: { profiles: [] } };
      return undefined;
    });
    await open(page, '/owner');
    await expect(page.getByText(/ffprobe is unavailable/i)).toBeVisible();
    await expect(page.getByText(/partial: 2 scanned/i)).toBeVisible();
    await expect(page.getByLabel(/TMDB API Read Access Token/i)).toHaveValue('');
    await page.getByText('Edit metadata').click();
    await expect(page.getByLabel('Tags')).toHaveValue('family');
    await page.getByRole('button', { name: /preview provider refresh/i }).click();
    await expect(page.getByText(/Provider refresh/)).toBeVisible();
    await check(page, errors);

    await mock(page, (path) => {
      if (path.endsWith('/setup/status')) return { json: ready };
      if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
      if (path.endsWith('/select')) return { json: {} };
      if (path.endsWith('/catalog/home')) return { json: { items: [{ ...film, local_only: true, synopsis: '' }], next: null } };
      if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [{ ...film, local_only: true, synopsis: '' }] }, { name: 'My List', items: [] }] } };
      return undefined;
    });
    await open(page, '/');
    await page.getByRole('button', { name: 'Viewer' }).click();
    await expect(page.getByText('Local-only metadata')).toHaveCount(0);
    await check(page, errors);
    const failureConsoleStart = errors.length;
    await mock(page, (path) => path.endsWith('/setup/status') ? { json: ready } : { status: 500, json: { error: { code: 'request_failed' } } });
    await open(page, '/home');
    await expect(page.getByRole('heading', { name: /catalog unavailable/i })).toBeVisible();
    const expectedTransportErrors = errors.splice(failureConsoleStart);
    expect(expectedTransportErrors.every((message) => message.includes('500'))).toBeTruthy();
    await check(page, errors);
  });
}

test('mocked owner identity repair confirms merge and unmerge without exposing file paths', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const errors: string[] = [];
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('pageerror', (error) => errors.push(error.message));
  const arrival = { id: 'film-a', title: 'Arrival', kind: 'film', local_only: false };
  const duplicate = { id: 'film-b', title: 'Arrival (duplicate)', kind: 'film', local_only: false };
  let merged = false;
  let unmerged = false;
  await mock(page, (path, method) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/owner/roots')) return { json: { films: '', tv: '' } };
    if (path.endsWith('/settings/tmdb')) return { json: { configured: false } };
    if (path.endsWith('/owner/metadata/unmatched')) return { json: { items: [] } };
    if (path.endsWith('/owner/identity/repairs')) return { json: { conflicts: merged ? [] : [{ id: 7, kind: 'film', reason: 'provider_identity', state: 'open', left: arrival, right: duplicate }], merges: merged ? [{ id: 'merge-7', kind: 'film', state: unmerged ? 'unmerged' : 'active', survivor: arrival, source: duplicate, decisions: ['Original title records remain recoverable.'] }] : [] } };
    if (path.endsWith('/owner/identity/merges') && method === 'POST') { merged = true; return { json: { id: 'merge-7', kind: 'film', state: 'active', survivor: arrival, source: duplicate, decisions: ['Original title records remain recoverable.'] } }; }
    if (path.endsWith('/owner/identity/merges/merge-7/unmerge') && method === 'POST') { unmerged = true; return { json: { id: 'merge-7', kind: 'film', state: 'unmerged', survivor: arrival, source: duplicate, decisions: ['Original title records remain recoverable.'] } }; }
    if (path.endsWith('/owner/settings')) return { json: { settings: [] } };
    if (path.endsWith('/settings/playback')) return { json: { segment_dir: '/tmp/flixr-segments', generation_bytes: 1, global_bytes: 1, max_generations: 1 } };
    if (path.endsWith('/playback/status')) return { json: { settings: { segment_dir: '/tmp/flixr-segments', generation_bytes: 1, global_bytes: 1, max_generations: 1 }, generations: [] } };
    if (path.endsWith('/scan/status')) return { json: { scan: {} } };
    if (path.endsWith('/profiles')) return { json: { profiles: [] } };
    return undefined;
  });

  await open(page, '/owner');
  const repair = page.getByRole('region', { name: 'Identity repair' });
  await expect(repair.getByText(/same provider identity/i)).toBeVisible();
  await expect(repair).not.toContainText('film-a');
  await expect(repair).not.toContainText('film-b');
  await expect(repair).not.toContainText('/media');
  await repair.getByRole('radio', { name: /^keep arrival; merge arrival \(duplicate\) into it$/i }).check();
  await repair.getByRole('button', { name: /merge selected titles/i }).click();
  const mergeDialog = page.getByRole('dialog', { name: /merge arrival \(duplicate\) into arrival/i });
  await expect(mergeDialog).not.toContainText('/media');
  await mergeDialog.getByRole('button', { name: /merge titles/i }).click();
  await repair.getByText('Identity repair history').click();
  await expect(repair.getByRole('button', { name: /unmerge arrival and arrival \(duplicate\)/i })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('owner-identity-repair.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('button', { name: /unmerge arrival and arrival \(duplicate\)/i })).toBeVisible();
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).resolves.toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('owner-identity-repair-mobile.png'), fullPage: true });
  await repair.getByRole('button', { name: /unmerge arrival and arrival \(duplicate\)/i }).click();
  const unmergeDialog = page.getByRole('dialog', { name: /unmerge arrival and arrival \(duplicate\)/i });
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).resolves.toBeTruthy();
  await unmergeDialog.getByRole('button', { name: 'Unmerge' }).click();
  await expect(repair.getByText(/unmerge retained the state/i)).toBeVisible();
  await check(page, errors);
});

test('mocked viewer journey: filtered grids, sort, demo detail, and My List', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const preferences: Record<'all' | 'film' | 'series', { view: 'rows' | 'grid'; sort: 'title' | 'year' | 'added' | 'watched' }> = { all: { view: 'rows', sort: 'title' }, film: { view: 'rows', sort: 'title' }, series: { view: 'rows', sort: 'title' } };
  let listed = false;
  const filmDemo = { id: 'demo-film', title: 'Demo Film', kind: 'film', year: 2026, local_only: false, playable: false, demo: true };
  const seriesDemo = { id: 'demo-series', title: 'Demo Series', kind: 'series', year: 2026, local_only: false, playable: false, demo: true };
  await mock(page, (path, method, query, body) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
    if (path.endsWith('/select')) return { json: {} };
    const preferenceMedia = path.match(/preferences\/(all|film|series)$/)?.[1] as keyof typeof preferences | undefined;
    if (preferenceMedia) {
      if (method === 'PUT') Object.assign(preferences[preferenceMedia], body);
      return { json: preferences[preferenceMedia] };
    }
    if (path.endsWith('/catalog/list/film/demo-film')) { listed = method === 'PUT'; return { json: { listed } }; }
    if (path.endsWith('/catalog/view')) {
      const media = query.includes('media=film') ? 'film' : query.includes('media=series') ? 'series' : 'all';
      const item = media === 'series' ? seriesDemo : filmDemo;
      const preference = preferences[media];
      return { json: preference.view === 'grid' ? { preference, items: [{ ...item, listed: item.id === filmDemo.id && listed }] } : { preference, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [{ ...item, listed: item.id === filmDemo.id && listed }] }, { name: 'My List', items: listed && item.id === filmDemo.id ? [{ ...item, listed }] : [] }] } };
    }
    if (path.endsWith('/catalog/items/demo-film')) return { json: filmDemo };
    if (path.endsWith('/catalog/items/demo-series') || path.endsWith('/catalog/series/demo-series')) return { json: seriesDemo };
    if (path.endsWith('/catalog/home')) return { json: { items: [filmDemo] } };
    return undefined;
  });
  await open(page, '/');
  await page.getByRole('button', { name: 'Viewer' }).click();
  await page.getByRole('button', { name: 'Movies' }).click();
  await page.getByLabel('View', { exact: true }).selectOption('grid');
  await expect(page.getByLabel('Titles').getByText('Demo Film')).toBeVisible();
  await page.getByLabel('Sort').selectOption('year');
  await page.screenshot({ path: testInfo.outputPath('movies-grid.png'), fullPage: true });
  await page.getByRole('button', { name: 'TV' }).click();
  await expect(page.getByLabel('View', { exact: true })).toHaveValue('rows');
  await page.getByLabel('View', { exact: true }).selectOption('grid');
  await expect(page.getByLabel('Titles').getByText('Demo Series')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('tv-grid.png'), fullPage: true });
  await page.getByRole('button', { name: 'Movies' }).click();
  await expect(page.getByLabel('Sort')).toHaveValue('year');
  await page.getByTestId('card-demo-film').click();
  const detail = page.getByRole('dialog');
  await expect(detail.getByText('Demo title · no media file')).toBeVisible();
  await expect(detail.getByRole('button', { name: /play demo film/i })).toHaveCount(0);
  await detail.getByRole('button', { name: 'Add to My List' }).click();
  await expect(detail.getByRole('button', { name: 'Remove from My List' })).toBeVisible();
  await detail.getByRole('button', { name: 'Remove from My List' }).click();
  await expect(detail.getByRole('button', { name: 'Add to My List' })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('demo-detail.png'), fullPage: true });
});



test('poster collections keep 16px measured gaps after viewport resize', async ({ page }) => {
  const items = Array.from({ length: 80 }, (_, index) => ({ id: `geometry-${index}`, title: `Geometry title ${index}`, kind: 'film', local_only: true, listed: false }));
  let view: 'rows' | 'grid' = 'rows';
  await mock(page, (path, method, _query, body) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
    if (path.endsWith('/select')) return { json: {} };
    if (path.endsWith('/preferences/film')) {
      if (method === 'PUT' && (body?.view === 'rows' || body?.view === 'grid')) view = body.view;
      return { json: { view, sort: 'title' } };
    }
    if (path.endsWith('/catalog/view')) return view === 'grid'
      ? { json: { preference: { view, sort: 'title' }, items } }
      : { json: { preference: { view, sort: 'title' }, sections: [{ name: 'Geometry', items }] } };
    return undefined;
  });
  await open(page, '/');
  await page.getByRole('button', { name: 'Viewer' }).click();
  await page.getByRole('button', { name: 'Movies' }).click();

  const assertRailGap = async () => {
    await expect.poll(async () => {
      const first = await page.getByTestId('card-geometry-0').boundingBox();
      const second = await page.getByTestId('card-geometry-1').boundingBox();
      return first && second ? second.x - (first.x + first.width) : null;
    }).toBeCloseTo(16, 0);
  };
  await assertRailGap();
  for (const viewport of [{ width: 1920, height: 1080 }, { width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await assertRailGap();
  }

  await page.getByLabel('View', { exact: true }).selectOption('grid');
  const grid = page.getByRole('region', { name: 'Titles' });
  const assertGridGap = async () => {
    await expect.poll(async () => {
      const columns = Number(await grid.getAttribute('data-columns'));
      const first = await page.getByTestId('card-geometry-0').boundingBox();
      const nextRow = await page.getByTestId(`card-geometry-${columns}`).boundingBox();
      return first && nextRow ? nextRow.y - (first.y + first.height) : null;
    }).toBeCloseTo(16, 0);
  };
  await assertGridGap();
  for (const viewport of [{ width: 1920, height: 1080 }, { width: 1440, height: 900 }, { width: 1024, height: 768 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await assertGridGap();
  }
});

test('large poster grids keep a bounded browser DOM while scrolling to the final row', async ({ page }) => {
  const items = Array.from({ length: 1_000 }, (_, index) => ({ id: `grid-${index}`, title: `Grid title ${index}`, kind: 'film', local_only: true, listed: false }));
  await mock(page, (path) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
    if (path.endsWith('/select')) return { json: {} };
    if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'grid', sort: 'title' }, items } };
    return undefined;
  });
  await open(page, '/');
  await page.getByRole('button', { name: 'Viewer' }).click();
  const grid = page.getByRole('region', { name: 'Titles' });
  await expect(grid).toBeVisible();
  expect(await grid.locator('[data-card]').count()).toBeLessThan(100);
  await grid.evaluate((node) => { node.scrollTop = node.scrollHeight; });
  await expect(page.getByTestId('card-grid-999')).toBeVisible();
  expect(await grid.locator('[data-card]').count()).toBeLessThan(100);
});
test('bright artwork keeps the shared hero scrim above artwork and below content', async ({ page }, testInfo) => {
  const brightFilm = { ...film, backdrop: brightBackdropArt };
  await mock(page, (path) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
    if (path.endsWith('/select')) return { json: {} };
    if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [brightFilm] }, { name: 'My List', items: [] }] } };
    return undefined;
  });
  await open(page, '/');
  await page.getByRole('button', { name: 'Viewer' }).click();
  await expect(page.getByRole('heading', { name: 'Cobalt Sky' })).toBeVisible();
  const layers = await page.locator('.hero').evaluate((hero) => ({ scrim: getComputedStyle(hero, '::before').backgroundImage, scrimZ: getComputedStyle(hero, '::before').zIndex, contentZ: getComputedStyle(hero.querySelector('.featured-copy')!).zIndex }));
  expect(layers.scrim).not.toBe('none');
  expect(layers.scrimZ).toBe('0');
  expect(layers.contentZ).toBe('1');
  await page.screenshot({ path: testInfo.outputPath('bright-artwork-scrim.png'), fullPage: true });
});
async function mockMediaSource(page: Page) {
  // These tests drive media events explicitly. Native decoding is covered by production acceptance.
  await page.context().addInitScript(() => {
    Object.defineProperty(HTMLMediaElement.prototype, 'src', { configurable: true, set(value) { document.documentElement.dataset.mockMediaSource = String(value); } });
  });
}
test('mocked playback planning and capacity error states', async ({ page }, testInfo) => {
  await mockMediaSource(page);
  const errors: string[] = [];
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('pageerror', (error) => errors.push(error.message));
  await mock(page, (path) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [] } };
    if (path.includes('/catalog/items/')) return { json: { ...film, id: path.split('/').at(-1) ?? film.id } };
    if (path.endsWith('/playback/plans')) return { json: { plan: { kind: 'direct', description: 'Original media' }, session_id: 'session-1', media_url: 'data:video/mp4;base64,', heartbeat_url: '/api/v1/playback/sessions/session-1/heartbeat', seek_url: '/api/v1/playback/sessions/session-1/seek', stop_url: '/api/v1/playback/sessions/session-1/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 } };
    if (path.endsWith('/heartbeat')) return { json: { expires_at: 9999999999 } };
    if (path.endsWith('/stop')) return { json: { stopped: true } };
    return undefined;
  });
  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    await page.goto('/play/film-1');
    await expect(page.getByRole('heading', { name: film.title, exact: true })).toBeVisible();
    await expect(page.locator('main.player')).toBeFocused();
    await expect(page.locator('video')).toBeVisible();
    const results = await new AxeBuilder({ page }).analyze();
    expect(results.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`player-${viewport.name}.png`), fullPage: true });
    await page.getByRole('button', { name: /back to library/i }).click();
    await expect(page).toHaveURL(/\/home$/);
  }
  expect(errors).toEqual([]);

  await mock(page, (path) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.includes('/catalog/items/')) return { json: { ...film, id: path.split('/').at(-1) ?? film.id } };
    if (path.endsWith('/playback/plans')) return { status: 503, json: { error: { code: 'playback_capacity' } } };
    return undefined;
  });
  await page.goto('/play/film-2');
  await expect(page.getByRole('alert')).toContainText(/playback limit/i);
});

test('source replacement preserves standard element fullscreen', async ({ page }) => {
  await mockMediaSource(page);
  await mock(page, (path) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.includes('/catalog/items/')) return { json: film };
    if (path.endsWith('/playback/plans')) return { json: { plan: { kind: 'direct' }, session_id: 'session-1', media_url: 'data:video/mp4;base64,first', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 } };
    if (path.endsWith('/quality')) return { json: { plan: { kind: 'direct' }, session_id: 'session-2', media_url: 'data:video/mp4;base64,second', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 } };
    if (path.endsWith('/heartbeat')) return { json: { accepted: true, expires_at: 9999999999 } };
    if (path.endsWith('/stop')) return { json: { stopped: true } };
    return undefined;
  });
  await page.goto('/play/film-1');
  const video = page.locator('video');
  await expect(page.locator('html')).toHaveAttribute('data-mock-media-source', 'data:video/mp4;base64,first');
  await video.evaluate((element) => {
    element.dispatchEvent(new Event('loadedmetadata'));
    element.dispatchEvent(new Event('canplay'));
    element.dispatchEvent(new Event('playing'));
  });
  await page.getByRole('button', { name: 'Fullscreen' }).click();
  await expect.poll(() => page.evaluate(() => document.fullscreenElement?.matches('main.player'))).toBe(true);

  // Headless browsers stop accepting pointer input once their native fullscreen
  // surface owns the window. Invoke the mounted controls' DOM handlers directly.
  await page.locator('summary[aria-label="Playback settings"]').evaluate((button) => (button as HTMLElement).click());
  await page.getByRole('combobox', { name: 'Streaming quality' }).evaluate((select) => {
    const quality = select as HTMLSelectElement;
    quality.value = 'data_saver';
    quality.dispatchEvent(new Event('change', { bubbles: true }));
  });

  await expect(page.locator('html')).toHaveAttribute('data-mock-media-source', 'data:video/mp4;base64,second');
  expect(await page.evaluate(() => document.fullscreenElement?.matches('main.player'))).toBe(true);
});

test('mocked playback heartbeat, buffering, cross-client resume, lease recovery, stop, and network recovery', async ({ page, context }) => {
  await mockMediaSource(page);
  let heartbeats = 0;
  let stops = 0;
  let expired = false;
  let interrupted = false;
  let plans = 0;
  const errors: string[] = [];
  const observe = (client: Page) => {
    client.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
    client.on('pageerror', (error) => errors.push(error.message));
  };
  const handler = (path: string) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [] } };
    if (path.includes('/catalog/items/')) return { json: { ...film, id: path.split('/').at(-1) ?? film.id } };
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return { json: { plan: { kind: 'direct', description: 'Original media' }, session_id: `session-${plans}`, media_url: `data:video/mp4;base64,session-${plans}`, heartbeat_url: `/api/v1/playback/sessions/session-${plans}/heartbeat`, seek_url: `/api/v1/playback/sessions/session-${plans}/seek`, stop_url: `/api/v1/playback/sessions/session-${plans}/stop`, resume_ms: plans > 1 ? 12_000 : 0, stream_offset_ms: 0, expires_at: 9999999999 } };
    }
    if (path.endsWith('/heartbeat')) {
      heartbeats += 1;
      if (expired) return { status: 403, json: { error: { code: 'playback_session_invalid' } } };
      if (interrupted) return { status: 500, json: { error: { code: 'request_failed' } } };
      return { json: { expires_at: 9999999999 } };
    }
    if (path.endsWith('/stop')) {
      stops += 1;
      return { json: { stopped: true } };
    }
    return undefined;
  };
  observe(page);
  await mock(page, handler);

  await page.goto('/play/film-1');
  const firstVideo = page.locator('video');
  await expect(firstVideo).toBeVisible();
  // The video element mounts before the playback-plan request finishes. Wait for
  // its source so this event exercises media buffering, not plan initialization.
  await expect(page.locator('html')).toHaveAttribute('data-mock-media-source', 'data:video/mp4;base64,session-1');
  await firstVideo.dispatchEvent('waiting');
  await expect(page.locator('.player-loading')).toBeVisible();
  await expect(page.locator('.player-loading')).toHaveText('');
  await expect(page.locator('.player-status')).toHaveText('Loading');
  const seekedTo = await page.locator('video').evaluate((video) => {
    const media = video as HTMLVideoElement;
    media.currentTime = 5;
    media.dispatchEvent(new Event('seeked'));
    return media.currentTime;
  });
  expect(seekedTo).toBe(5);
  await page.locator('video').dispatchEvent('pause');
  await expect.poll(() => heartbeats).toBeGreaterThan(0);
  await page.getByRole('button', { name: /back to library/i }).click();
  await expect.poll(() => stops).toBeGreaterThan(0);

  const secondClient = await context.newPage();
  observe(secondClient);
  await mock(secondClient, handler);
  const activateMockedPlayback = async (session: number) => {
    await expect(secondClient.locator('html')).toHaveAttribute('data-mock-media-source', `data:video/mp4;base64,session-${session}`);
    return secondClient.locator('video').evaluate((video) => {
      const media = video as HTMLVideoElement;
      Object.defineProperty(media, 'readyState', { configurable: true, value: HTMLMediaElement.HAVE_FUTURE_DATA });
      media.play = () => Promise.resolve();
      media.dispatchEvent(new Event('loadedmetadata'));
      media.dispatchEvent(new Event('canplay'));
      media.dispatchEvent(new Event('playing'));
      return media.currentTime;
    });
  };
  await secondClient.goto('/play/film-1');
  await expect(secondClient.locator('video')).toBeVisible();
  const resumedAt = await activateMockedPlayback(2);
  expect(resumedAt).toBe(12);
  expired = true;
  const plansBeforeExpiry = plans;
  await secondClient.locator('video').dispatchEvent('pause');
  await expect.poll(() => plans).toBeGreaterThan(plansBeforeExpiry);
  const recoveredFromExpiry = await activateMockedPlayback(plans);
  expect(recoveredFromExpiry).toBe(12);
  await expect(secondClient.getByRole('alert')).toHaveCount(0);

  expired = false;
  const plansBeforeReload = plans;
  await secondClient.goto('/play/film-1');
  await expect(secondClient.locator('video')).toBeVisible();
  await expect.poll(() => plans).toBeGreaterThan(plansBeforeReload);
  await activateMockedPlayback(plans);
  interrupted = true;
  const plansBeforeInterruption = plans;
  await secondClient.locator('video').dispatchEvent('pause');
  await expect.poll(() => plans).toBeGreaterThan(plansBeforeInterruption);
  const recoveredFromInterruption = await activateMockedPlayback(plans);
  expect(recoveredFromInterruption).toBe(12);
  await expect(secondClient.getByRole('alert')).toHaveCount(0);
  expect(
    errors.every((message) => message.includes('403') || message.includes('500')),
    `unexpected browser errors: ${JSON.stringify(errors)}`,
  ).toBeTruthy();

  interrupted = false;
  const stopsBeforeExit = stops;
  await secondClient.getByRole('button', { name: /back to library/i }).click();
  await expect.poll(() => stops).toBeGreaterThan(stopsBeforeExit);
});

test('personal history dates, clear undo, and rating persist across navigation', async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('pageerror', error => errors.push(error.message));
  const timestamp = Date.UTC(2024, 4, 6, 12);
  let cleared = false;
  let rating = 2;
  await mock(page, (path, method, _query, body) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'New', items: [film] }] } };
    if (path.endsWith('/catalog/items/film-1')) return { json: film };
    if (path.endsWith('/ratings/film-1')) {
      if (method === 'PUT') { rating = Number(body?.value); return { json: { saved: true } }; }
      return { json: { rating: { value: rating } } };
    }
    if (path.endsWith('/history/clear')) { cleared = true; return { json: { id: 'clear-one', undo_until: Date.now() + 300000 } }; }
    if (path.endsWith('/history/clear/clear-one/undo')) { cleared = false; return { json: { restored: true } }; }
    if (path.endsWith('/history')) return { json: { events: cleared ? [] : [
      { id: 'local', catalog_id: 'film-1', title: 'Local completion', kind: 'film', type: 'completed', provenance: 'local', source_time: null, recorded_at: timestamp },
      { id: 'imported', catalog_id: 'gone', title: 'Unknown imported viewing', kind: 'film', type: 'summary', provenance: 'import', source_time: null, recorded_at: timestamp },
    ] } };
    return undefined;
  });
  await page.goto('/history');
  await expect(page.getByRole('heading', { name: 'Viewing history' })).toBeVisible();
  await expect(page.locator('time')).toHaveAttribute('datetime', new Date(timestamp).toISOString());
  await expect(page.getByText('Date unknown')).toHaveCount(1);
  await check(page, errors);
  await page.screenshot({ path: testInfo.outputPath('history-desktop.png') });
  await page.getByRole('button', { name: 'Clear history' }).click();
  await expect(page.getByText('Local completion')).toHaveCount(0);
  await page.getByRole('button', { name: 'Undo clear' }).click();
  await expect(page.getByText('Local completion')).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await check(page, errors);
  await page.screenshot({ path: testInfo.outputPath('history-phone.png') });
  await page.goto('/detail/film-1');
  const select = page.getByRole('combobox', { name: 'Your rating' });
  await expect(select).toHaveValue('2');
  await select.selectOption('5');
  await expect(select).toBeEnabled();
  await expect(select).toHaveValue('5');
  await page.reload();
  await expect(page.getByRole('combobox', { name: 'Your rating' })).toHaveValue('5');
  await check(page, errors);
});
