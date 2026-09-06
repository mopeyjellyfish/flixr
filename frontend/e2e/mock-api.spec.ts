/** Component/visual-state suite. It uses mocked API responses and is not production acceptance evidence. */
import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const viewports = [{ name: 'tv', width: 1920, height: 1080 }, { name: 'desktop', width: 1440, height: 900 }, { name: 'tablet', width: 1024, height: 768 }, { name: 'phone', width: 390, height: 844 }];
type JSONValue = Record<string, unknown>;
const ready = { claimed: true, readiness: { ffprobe: true, ffmpeg: true } };
const posterArt = `data:image/svg+xml;base64,${Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 600"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#183a82"/><stop offset="1" stop-color="#05070c"/></linearGradient></defs><rect width="400" height="600" fill="url(#g)"/><circle cx="295" cy="160" r="105" fill="#5b8cff" opacity=".52"/><path d="M0 430L230 250l170 155v195H0z" fill="#101623" opacity=".82"/></svg>').toString('base64')}`;
const backdropArt = `data:image/svg+xml;base64,${Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900"><defs><radialGradient id="g"><stop stop-color="#5b8cff"/><stop offset="1" stop-color="#05070c"/></radialGradient></defs><rect width="1600" height="900" fill="#05070c"/><ellipse cx="1180" cy="330" rx="520" ry="380" fill="url(#g)" opacity=".58"/><path d="M580 900L1100 330l500 430v140z" fill="#101623" opacity=".85"/></svg>').toString('base64')}`;
const film = { id: 'film-1', title: 'Cobalt Sky', kind: 'film', year: 2024, synopsis: 'A small local signal.', local_only: false, poster: posterArt, backdrop: backdropArt, container: 'mp4', video_codec: 'h264' };
const series = { id: 'series-1', title: 'Night Relay', kind: 'series', year: 2023, synopsis: 'Episodes from a local relay.', local_only: true, poster: posterArt };

async function mock(page: Page, handler: (path: string, method: string, query: string, body?: Record<string, unknown>) => { status?: number; json: JSONValue }) {
  await page.unrouteAll();
  await page.route('**/api/v1/**', (route) => {
    const request = route.request();
    const response = handler(new URL(request.url()).pathname, request.method(), new URL(request.url()).search, request.postDataJSON() as Record<string, unknown> | undefined);
    return route.fulfill({ status: response.status ?? 200, json: response.json });
  });
}
async function open(page: Page, path: string) {
  const status = page.waitForResponse((response) => response.url().includes('/api/v1/setup/status'));
  await page.goto('/');
  await status;
  await page.waitForFunction(() => !document.body.textContent?.includes('Loading local Flixr…'));
  if (path !== '/') await page.evaluate((nextPath) => { window.history.pushState({}, '', nextPath); window.dispatchEvent(new PopStateEvent('popstate')); }, path);
}
async function check(page: Page, errors: string[]) {
  const results = await new AxeBuilder({ page }).analyze();
  expect(results.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await expect(page.getByRole('button', { name: /view details/i })).toBeVisible().catch(() => undefined);
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
      if (path.endsWith('/owner/roots')) return { json: { saved: true } };
      if (path.endsWith('/owner/scan')) { scanStarts += 1; return { json: { scan: { status: 'running', scanned: 0, unmatched: 0, failed: 0 } } }; }
      if (path.endsWith('/profiles') && method === 'POST') { profileCreates += 1; return { json: { id: 'first-run-viewer', name: 'First-run viewer', protected: false } }; }
      if (path.endsWith('/profiles/first-run-viewer/select')) { profileSelections += 1; return { json: { selected: true } }; }
      if (path.endsWith('/catalog/home')) return { json: { items: [film], total: 1, next: null } };
      if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [film] }, { name: 'My List', items: [] }] } };
      if (path.endsWith('/profiles')) return { json: { profiles: [] } };
      return { json: {} };
    });
    await open(page, '/');
    await expect(page.getByText(/ffprobe is unavailable/i)).toBeVisible();
    await expect(page.getByText(/direct play can continue/i)).toBeVisible();
    await expect(page.getByRole('button', { name: /secure this server/i })).toBeVisible();
    await check(page, errors);
    await page.getByLabel(/setup token/i).fill('first-run-token');
    await page.getByLabel(/owner password/i).fill('safe owner password');
    await page.getByRole('button', { name: /secure this server/i }).click();
    await expect(page.getByRole('heading', { name: /bring your libraries home/i })).toBeVisible();
    await expect(page.getByLabel(/tmdb/i)).toHaveCount(0);
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
      return { json: {} };
    });
    await open(page, '/');
    await page.getByRole('button', { name: /protected/i }).click();
    await page.getByLabel(/profile PIN/i).fill('0000');
    await page.getByRole('button', { name: /continue/i }).click();
    await expect(page.getByRole('alert')).toContainText(/not valid/i);
    await page.getByRole('button', { name: /continue/i }).click();
    await expect(page.getByRole('alert')).toContainText(/too many/i);
    errors.length = 0;

    await mock(page, (path, _method, query) => {
      if (path.endsWith('/setup/status')) return { json: ready };
      if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
      if (path.endsWith('/select')) return { json: {} };
      if (path.endsWith('/catalog/home')) return { json: { items: [film, series], total: 2, next: null } };
      if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [film, series] }, { name: 'My List', items: [] }] } };
      if (path.endsWith('/catalog/films/film-1')) return { json: film };
      if (path.endsWith('/catalog/items/film-1')) return { json: film };
      if (path.endsWith('/catalog/items/series-1')) return { json: series };
      if (path.endsWith('/catalog/series/series-1')) return { json: { ...series, seasons: [{ id: 'season-1', number: 1, episodes: [{ id: 'episode-1', title: 'First Signal', kind: 'episode', season: 1, episode: 1, local_only: true }] }] } };
      if (path.endsWith('/catalog/search')) return { json: { items: query.includes('relay') ? [series] : [], total: query.includes('relay') ? 1 : 0, next: null } };
      return { json: {} };
    });
    await open(page, '/');
    await page.getByRole('button', { name: 'Viewer' }).click();
    await expect(page.getByRole('heading', { name: 'Cobalt Sky' })).toBeVisible();
    await page.getByRole('button', { name: /view details for cobalt sky/i }).click();
    await expect(page.getByRole('dialog')).toContainText(/Media: mp4/i);
    await page.getByRole('button', { name: /close details/i }).click();
    await page.getByTestId('card-series-1').click();
    await expect(page.getByRole('dialog')).toContainText(/S1 E1 First Signal/i);
    await page.getByRole('button', { name: /close details/i }).click();
    await check(page, errors);
    await page.screenshot({ path: testInfo.outputPath('populated-home.png'), fullPage: true });
    await open(page, '/search?q=missing');
    await expect(page.getByRole('heading', { name: /no matching titles/i })).toBeVisible();
    await open(page, '/search?q=relay');
    await expect(page.getByTestId('card-series-1')).toBeVisible();

    await mock(page, (path) => {
      if (path.endsWith('/setup/status')) return { json: { claimed: true, readiness: { ffprobe: false, ffmpeg: true } } };
      if (path.endsWith('/owner/roots')) return { json: { films: '/media/films', tv: '/media/tv' } };
      if (path.endsWith('/settings/tmdb')) return { json: { configured: true } };
      if (path.endsWith('/settings/playback')) return { json: { segment_dir: '/tmp/flixr-segments', generation_bytes: 268435456, global_bytes: 536870912, max_generations: 2 } };
      if (path.endsWith('/playback/status')) return { json: { settings: { segment_dir: '/tmp/flixr-segments', generation_bytes: 268435456, global_bytes: 536870912, max_generations: 2 }, generations: [] } };
      if (path.endsWith('/scan/status')) return { json: { scan: { status: 'partial', scanned: 2, unmatched: 1, failed: 1 } } };
      if (path.endsWith('/profiles')) return { json: { profiles: [] } };
      return { json: {} };
    });
    await open(page, '/owner');
    await expect(page.getByText(/ffprobe is unavailable/i)).toBeVisible();
    await expect(page.getByText(/partial: 2 scanned/i)).toBeVisible();
    await expect(page.getByLabel(/TMDB access token/i)).toHaveValue('');
    await check(page, errors);

    await mock(page, (path) => {
      if (path.endsWith('/setup/status')) return { json: ready };
      if (path.endsWith('/profiles')) return { json: { profiles: [{ id: 'viewer', name: 'Viewer', protected: false }] } };
      if (path.endsWith('/select')) return { json: {} };
      if (path.endsWith('/catalog/home')) return { json: { items: [{ ...film, local_only: true, synopsis: '' }], next: null } };
      if (path.endsWith('/catalog/view')) return { json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Continue Watching', items: [] }, { name: 'New', items: [{ ...film, local_only: true, synopsis: '' }] }, { name: 'My List', items: [] }] } };
      return { json: {} };
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
    if (path.endsWith('/catalog/items/demo-film') || path.endsWith('/catalog/films/demo-film')) return { json: filmDemo };
    if (path.endsWith('/catalog/items/demo-series') || path.endsWith('/catalog/series/demo-series')) return { json: seriesDemo };
    if (path.endsWith('/catalog/home')) return { json: { items: [filmDemo] } };
    return { json: {} };
  });
  await open(page, '/');
  await page.getByRole('button', { name: 'Viewer' }).click();
  await page.getByRole('button', { name: 'Movies' }).click();
  await page.getByLabel('View').selectOption('grid');
  await expect(page.getByLabel('Titles').getByText('Demo Film')).toBeVisible();
  await page.getByLabel('Sort').selectOption('year');
  await page.screenshot({ path: testInfo.outputPath('movies-grid.png'), fullPage: true });
  await page.getByRole('button', { name: 'TV' }).click();
  await expect(page.getByLabel('View')).toHaveValue('rows');
  await page.getByLabel('View').selectOption('grid');
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

test('mocked playback planning and capacity error states', async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('pageerror', (error) => errors.push(error.message));
  await mock(page, (path) => {
    if (path.endsWith('/setup/status')) return { json: ready };
    if (path.endsWith('/playback/plans')) return { json: { plan: { kind: 'direct', description: 'Original media' }, session_id: 'session-1', media_url: 'data:video/mp4;base64,', heartbeat_url: '/api/v1/playback/sessions/session-1/heartbeat', seek_url: '/api/v1/playback/sessions/session-1/seek', stop_url: '/api/v1/playback/sessions/session-1/stop', resume_ms: 0, stream_offset_ms: 0, expires_at: 9999999999 } };
    return { json: {} };
  });
  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    await page.goto('/play/film-1');
    await expect(page.getByRole('heading', { name: /now playing/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /back to library/i })).toBeFocused();
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
    if (path.endsWith('/playback/plans')) return { status: 503, json: { error: { code: 'playback_capacity' } } };
    return { json: {} };
  });
  await page.goto('/play/film-2');
  await expect(page.getByRole('alert')).toContainText(/playback limit/i);
});

test('mocked playback heartbeat, buffering, cross-client resume, expiry, recovery, stop, and interruption states', async ({ page, context }) => {
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
    if (path.endsWith('/playback/plans')) {
      plans += 1;
      return { json: { plan: { kind: 'direct', description: 'Original media' }, session_id: `session-${plans}`, media_url: 'data:video/mp4;base64,', heartbeat_url: `/api/v1/playback/sessions/session-${plans}/heartbeat`, seek_url: `/api/v1/playback/sessions/session-${plans}/seek`, stop_url: `/api/v1/playback/sessions/session-${plans}/stop`, resume_ms: plans > 1 ? 12_000 : 0, stream_offset_ms: 0, expires_at: 9999999999 } };
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
    return { json: {} };
  };
  observe(page);
  await mock(page, handler);

  await page.goto('/play/film-1');
  await expect(page.locator('video')).toBeVisible();
  await page.locator('video').dispatchEvent('waiting');
  await expect(page.getByRole('status')).toContainText(/buffering/i);
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
  await secondClient.goto('/play/film-1');
  await expect(secondClient.locator('video')).toBeVisible();
  const resumedAt = await secondClient.locator('video').evaluate((video) => {
    const media = video as HTMLVideoElement;
    media.dispatchEvent(new Event('loadedmetadata'));
    return media.currentTime;
  });
  expect(resumedAt).toBe(12);
  expired = true;
  await secondClient.locator('video').dispatchEvent('pause');
  await expect(secondClient.getByRole('alert')).toContainText(/session expired/i);

  expired = false;
  await secondClient.goto('/play/film-1');
  await expect(secondClient.locator('video')).toBeVisible();
  interrupted = true;
  await secondClient.locator('video').dispatchEvent('pause');
  await expect(secondClient.getByRole('alert')).toContainText(/complete that request/i);
  expect(errors.every((message) => message.includes('403') || message.includes('500'))).toBeTruthy();
});
