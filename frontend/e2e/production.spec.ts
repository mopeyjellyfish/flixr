/** Production acceptance path: this suite uses the built flixr binary and never mocks its API. */
import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { acceptScreens } from './screen-acceptance';

const viewports = [
  { name: 'tv', width: 1920, height: 1080 },
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 1024, height: 768 },
  { name: 'phone', width: 390, height: 844 },
];

test('built binary completes setup, scan, profile, browse, and detail flow', async ({ page, browser }, testInfo) => {
  test.setTimeout(150_000);
  const token = process.env.FLIXR_SETUP_TOKEN;
  const filmsRoot = process.env.FLIXR_FILMS_ROOT;
  const tvRoot = process.env.FLIXR_TV_ROOT;
  if (!token || !filmsRoot || !tvRoot) throw new Error('FLIXR_SETUP_TOKEN, FLIXR_FILMS_ROOT, and FLIXR_TV_ROOT are required for production acceptance.');
  const errors: string[] = [];
  const externalRequests: string[] = [];
  const origin = new URL(testInfo.project.use.baseURL!).origin;
  await page.context().route('**/*', (route) => {
    if (new URL(route.request().url()).origin === origin) return route.continue();
    externalRequests.push(route.request().url());
    return route.abort();
  });
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });

  await page.goto('/setup');
  await expect(page.locator('[data-app-content]')).not.toHaveAttribute('inert');
  await page.getByLabel(/setup token/i).fill(token);
  await page.getByLabel(/owner password/i).fill('production-owner-password');
  const scan = page.waitForResponse((response) => response.url().includes('/api/v1/owner/scan') && response.request().method() === 'POST');
  await page.getByRole('button', { name: /secure this server/i }).click();
  await expect(page.getByRole('heading', { name: /bring your libraries home/i })).toBeVisible();
  await page.getByLabel(/films library/i).fill(filmsRoot);
  await page.getByLabel(/tv library/i).fill(tvRoot);
  await page.getByRole('button', { name: /save libraries/i }).click();
  expect((await scan).ok()).toBeTruthy();
  await expect(page.getByRole('heading', { name: /profile/i })).toBeVisible();
  await expect.poll(async () => page.evaluate(async () => {
    const response = await fetch('/api/v1/owner/scan/status');
    const body = await response.json() as { scan?: { status?: string; scanned?: number; unmatched?: number; failed?: number } };
    const scanStatus = body.scan;
    return `${scanStatus?.status}:${scanStatus?.scanned}:${scanStatus?.unmatched}:${scanStatus?.failed}`;
  }), { timeout: 30_000 }).toBe('complete:4:0:0');
  await page.getByLabel(/^name$/i).fill('Production viewer');
  await page.getByRole('button', { name: /create profile/i }).click();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByRole('region', { name: 'New' }).getByRole('button', { name: /film blue horizon 2026/i })).toBeVisible({ timeout: 30_000 });
  await page.getByRole('button', { name: /switch profile/i }).click();
  await page.getByRole('button', { name: /production viewer/i }).click();
  await expect(page.getByRole('region', { name: 'New' }).getByRole('button', { name: /film blue horizon 2026/i })).toBeVisible();

  await expect(page.getByTestId(/card-.*$/)).toHaveCount(3);
  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    await page.goto('/home');
    await expect(page.locator('[data-app-content]')).not.toHaveAttribute('inert');
    await expect(page.getByRole('region', { name: 'New' }).getByRole('button', { name: /film blue horizon 2026/i })).toBeVisible();
    const axe = await new AxeBuilder({ page }).analyze();
    expect(axe.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`production-home-${viewport.name}.png`), fullPage: true });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole('region', { name: 'New' }).getByRole('button', { name: /film blue horizon 2026/i }).click();
  await page.getByRole('button', { name: /play blue horizon 2026/i }).click();
  await expectPlayback(page);
  const directVideo = page.locator('video');
  await directVideo.evaluate((element) => { const video = element as HTMLVideoElement; video.loop = true; });
  await page.locator('main.player').focus();
  await page.keyboard.press('Space');
  await expect.poll(() => directVideo.evaluate((element) => (element as HTMLVideoElement).paused)).toBeTruthy();
  for (const viewport of [...viewports, { name: 'phone-landscape', width: 844, height: 390 }, { name: '4k', width: 3840, height: 2160 }, { name: '8k', width: 7680, height: 4320 }]) {
    await page.setViewportSize(viewport);
    const stage = await page.locator('main.player').boundingBox();
    expect(Math.abs((stage?.width ?? 0) - viewport.width)).toBeLessThan(0.5);
    expect(Math.abs((stage?.height ?? 0) - viewport.height)).toBeLessThan(0.5);
    await expect(page.getByRole('button', { name: 'Fullscreen', exact: true })).toBeInViewport();
    await page.getByText('Settings', { exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Playback speed' })).toBeInViewport();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('combobox', { name: 'Playback speed' })).not.toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`immersive-player-${viewport.name}.png`) });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await directVideo.evaluate((element) => { (element as HTMLVideoElement).playbackRate = 0.25; });
  await page.locator('main.player').focus();
  await page.keyboard.press('Space');
  await expect(page.locator('main.player')).toHaveClass(/controls-hidden/, { timeout: 6000 });
  await page.mouse.move(200, 200);
  await expect(page.locator('main.player')).not.toHaveClass(/controls-hidden/);
  await directVideo.evaluate((element) => { (element as HTMLVideoElement).loop = false; (element as HTMLVideoElement).playbackRate = 1; });

  const acknowledgedHeartbeat = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/heartbeat') && response.ok());
  await directVideo.dispatchEvent('pause');
  const acknowledgedRequest = (await acknowledgedHeartbeat).request();
  const acknowledgedPosition = Number((acknowledgedRequest.postDataJSON() as { position_ms: number }).position_ms);
  expect(acknowledgedPosition).toBeGreaterThan(0);

  await page.context().setOffline(true);
  await expect.poll(() => page.evaluate(() => navigator.onLine)).toBeFalsy();
  const failedHeartbeat = page.waitForEvent('requestfailed', (request) => request.method() === 'POST' && request.url().endsWith('/heartbeat'));
  const failedCapabilityRefresh = page.waitForEvent('requestfailed', (request) => request.method() === 'GET' && request.url().includes('/catalog/items/'));
  await directVideo.dispatchEvent('pause');
  await Promise.all([failedHeartbeat, failedCapabilityRefresh]);
  const reconnectPlan = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/playback/plans') && response.ok());
  await page.context().setOffline(false);
  await expect.poll(() => page.evaluate(() => navigator.onLine)).toBeTruthy();
  const reconnectBody = await (await reconnectPlan).json() as { resume_ms: number };
  expect(reconnectBody.resume_ms).toBe(acknowledgedPosition);
  await expect.poll(() => directVideo.evaluate((element) => (element as HTMLVideoElement).readyState), { timeout: 20_000 }).toBeGreaterThan(0);
  await page.getByRole('button', { name: 'Pause', exact: true }).click();
  await clickTimeline(page, false);
  await expect(page.getByRole('button', { name: 'Play', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('production-direct-playback-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('production-direct-playback-phone.png'), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole('button', { name: /back to library/i }).click();
  await expect(page.locator('[data-catalog-id]:focus')).toHaveAccessibleName(/blue horizon 2026/i);
  let faultSession = '';
  let injectedSegmentFailures = 0;
  let releaseInitialSegmentFailures!: () => void;
  const initialSegmentFailureGate = new Promise<void>((resolve) => { releaseInitialSegmentFailures = resolve; });
  const failInitialSegments = async (route: import('@playwright/test').Route) => {
    const match = new URL(route.request().url()).pathname.match(/\/playback\/sessions\/([^/]+)\/segment-[^/]+\.m4s$/);
    if (!match) return route.continue();
    faultSession ||= match[1];
    if (match[1] === faultSession) {
      await initialSegmentFailureGate;
      injectedSegmentFailures += 1;
      return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'playback_failed' } }) });
    }
    return route.continue();
  };
  await page.route('**/api/v1/playback/sessions/*/segment-*.m4s', failInitialSegments);
  const initialCompatibilityPlan = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/playback/plans') && response.ok());
  await page.getByRole('button', { name: /film compatibility check 2026/i }).click();
  await page.getByRole('button', { name: /play compatibility check 2026/i }).click();
  const initialCompatibilityBody = await (await initialCompatibilityPlan).json() as { session_id: string; heartbeat_url: string };
  const acknowledgedCompatibilityPosition = 750;
  const recoveredCompatibilityPlan = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/playback/plans') && response.ok());
  const compatibilityHeartbeat = await page.evaluate(async ({ heartbeatURL, positionMS }) => {
    const response = await fetch(heartbeatURL, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ position_ms: positionMS, observation: 1 }) });
    return response.status;
  }, { heartbeatURL: initialCompatibilityBody.heartbeat_url, positionMS: acknowledgedCompatibilityPosition });
  expect(compatibilityHeartbeat).toBe(200);
  releaseInitialSegmentFailures();
  const recoveredCompatibilityBody = await (await recoveredCompatibilityPlan).json() as { session_id: string; resume_ms: number };
  expect(recoveredCompatibilityBody.session_id).not.toBe(initialCompatibilityBody.session_id);
  expect(recoveredCompatibilityBody.resume_ms).toBe(acknowledgedCompatibilityPosition);
  expect(injectedSegmentFailures).toBeGreaterThan(0);
  await page.unroute('**/api/v1/playback/sessions/*/segment-*.m4s', failInitialSegments);
  await expectPlayback(page);
  await page.screenshot({ path: testInfo.outputPath('production-transcode-playback-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: /back to library/i }).click();

  await page.getByRole('button', { name: /series signal/i }).click();
  await page.getByRole('button', { name: /play s1 e1 signal/i }).click();
  await expectPlayback(page);
  await page.getByRole('button', { name: 'Pause', exact: true }).click();
  await clickTimeline(page, true);
  await expect(page.getByRole('button', { name: 'Play', exact: true })).toBeVisible();
  await page.getByText('Settings', { exact: true }).click();
  const audio = page.getByRole('combobox', { name: /audio track/i });
  await expect(audio).toBeVisible();
  await expect(audio.locator('option')).toHaveText([/English/, /French/, /Director Commentary.*Japanese.*External/]);
  const subtitles = page.getByRole('combobox', { name: 'Subtitles' });
  await expect(subtitles).toBeVisible();
  await expect(subtitles.locator('option')).toHaveText([/Off/, /English.*Default.*Forced/, /French/]);
  const initialSubtitleURL = await page.locator('video track').getAttribute('src');
  expect(initialSubtitleURL).toBeTruthy();
  const initialCue = await page.evaluate(async (url) => (await fetch(url!)).text(), initialSubtitleURL);
  expect(initialCue).toContain('Signal caption');
  const initialSession = new URL(initialSubtitleURL!, origin).pathname.split('/')[5];
  const offResponse = page.waitForResponse((response) => response.url().includes('/playback/sessions/') && response.url().endsWith('/subtitle'));
  await subtitles.selectOption('off');
  const offPlan = await (await offResponse).json() as { session_id: string; subtitle_url?: string };
  expect(offPlan.session_id).toBe(initialSession);
  expect(offPlan.subtitle_url).toBeUndefined();
  await expect(page.locator('video track')).toHaveCount(0);
  const frenchSubtitleResponse = page.waitForResponse((response) => response.url().includes('/playback/sessions/') && response.url().endsWith('/subtitle'));
  await subtitles.selectOption('embedded:4');
  expect((await frenchSubtitleResponse).ok()).toBeTruthy();
  const frenchSubtitleURL = await page.locator('video track').getAttribute('src');
  const frenchCue = await page.evaluate(async (url) => (await fetch(url!)).text(), frenchSubtitleURL);
  expect(frenchCue).toContain('Sous-titre Signal');
  for (const seconds of [0.1, 0.3, 0.1]) {
    const seekResponse = page.waitForResponse((response) => response.url().includes('/playback/sessions/') && response.url().endsWith('/seek'));
    await page.locator('video').evaluate((video, target) => {
      const media = video as HTMLVideoElement;
      media.currentTime = target;
      media.dispatchEvent(new Event('seeked', { bubbles: true }));
    }, seconds);
    const response = await seekResponse;
    expect(response.ok()).toBeTruthy();
    const plan = await response.json() as { selected_subtitle?: { index: number }; subtitle_url?: string };
    expect(plan.selected_subtitle?.index).toBe(4);
    expect(plan.subtitle_url).toBeTruthy();
    await expect(subtitles).toHaveValue('embedded:4');
    await expect(page.locator('video track')).toHaveAttribute('src', plan.subtitle_url!);
    const cue = await page.evaluate(async (url) => {
      const response = await fetch(url!);
      return { status: response.status, text: await response.text() };
    }, plan.subtitle_url);
    expect(cue.status).toBe(200);
    expect(cue.text).toContain('Sous-titre Signal');
  }
  const frenchResponse = page.waitForResponse((response) => response.url().includes('/playback/sessions/') && response.url().endsWith('/audio'));
  await audio.selectOption('embedded:2');
  const french = await frenchResponse;
  expect(french.ok()).toBeTruthy();
  const frenchPlan = await french.json() as { plan: { audio_stream_index: number }; resume_ms: number };
  expect(frenchPlan.plan.audio_stream_index).toBe(2);
  expect(frenchPlan.resume_ms).toBeGreaterThanOrEqual(100);
  await expect.poll(() => page.locator('video').evaluate((element) => (element as HTMLVideoElement).readyState), { timeout: 20_000 }).toBeGreaterThan(0);
  const externalResponse = page.waitForResponse((response) => response.url().includes('/playback/sessions/') && response.url().endsWith('/audio'));
  await audio.selectOption('external:3');
  const external = await externalResponse;
  expect(external.ok()).toBeTruthy();
  const externalPlan = await external.json() as { plan: { audio_stream_index: number; audio_external: boolean } };
  expect(externalPlan.plan).toMatchObject({ audio_stream_index: 3, audio_external: true });
  await page.screenshot({ path: testInfo.outputPath('production-remux-playback-desktop.png'), fullPage: true });
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Play', exact: true }).click();
  const firstEpisodeURL = page.url();
  await expect(page.getByRole('heading', { name: 'Next episode' })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByRole('dialog')).toContainText(/S1 E2 · Signal/i);
  await page.screenshot({ path: testInfo.outputPath('production-next-episode-countdown-desktop.png'), fullPage: true });
  await expect.poll(() => page.url(), { timeout: 20_000 }).not.toBe(firstEpisodeURL);
  await expectPlaybackStartedAutomatically(page);
  await expect(page.getByRole('heading', { name: 'End of series' })).toBeVisible({ timeout: 20_000 });
  await page.screenshot({ path: testInfo.outputPath('production-end-of-series-desktop.png'), fullPage: true });
  const refreshedHome = page.waitForResponse((response) => response.url().includes('/api/v1/catalog/view') && response.request().method() === 'GET');
  await page.getByRole('button', { name: /return to your library/i }).click();
  expect((await refreshedHome).ok()).toBeTruthy();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByRole('region', { name: 'Continue Watching' }).getByRole('button', { name: /series signal/i })).toHaveCount(0);
  await page.getByRole('region', { name: 'New' }).getByRole('button', { name: /series signal/i }).click();
  await expect(page.getByRole('dialog')).toContainText(/Signal.*Season 1 · Episode 1/i);
  await expect(page).toHaveURL(/\/detail\//);
  await page.reload();
  await expect(page.getByRole('dialog')).toContainText(/Signal.*Season 1 · Episode 1/i);
  await page.goBack();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('button', { name: 'Search' }).click();
  await page.getByLabel(/search titles/i).fill('Signal');
  await expect(page.getByRole('region', { name: /search results/i })).toContainText('Signal');

  const axe = await new AxeBuilder({ page }).analyze();
  expect(axe.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('production-search-desktop.png'), fullPage: true });
  await acceptScreens(page, browser, testInfo);
  const activeGenerations = await page.evaluate(async () => {
    const login = await fetch('/api/v1/owner/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: 'production-owner-password' }),
    });
    if (!login.ok) throw new Error(`owner login failed with HTTP ${login.status}`);
    const status = await fetch('/api/v1/owner/playback/status');
    if (!status.ok) throw new Error(`playback status failed with HTTP ${status.status}`);
    const body = await status.json() as { generations: unknown[] };
    return body.generations;
  });
  expect(activeGenerations).toEqual([]);
  expect(errors.filter((message) => !message.includes('ERR_INTERNET_DISCONNECTED') && !message.includes('503'))).toEqual([]);
  expect(externalRequests).toEqual([]);
});

async function expectPlayback(page: import('@playwright/test').Page) {
  const video = page.locator('video');
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState), { timeout: 20_000 }).toBeGreaterThan(0);
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).currentTime > 0 || (element as HTMLVideoElement).ended), { timeout: 20_000 }).toBeTruthy();
}

async function expectPlaybackStartedAutomatically(page: import('@playwright/test').Page) {
  const video = page.locator('video');
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState), { timeout: 20_000 }).toBeGreaterThan(0);
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).currentTime > 0 || (element as HTMLVideoElement).ended), { timeout: 20_000 }).toBeTruthy();
}

async function clickTimeline(page: import('@playwright/test').Page, streamed: boolean) {
  await page.locator('main.player').hover();
  const timeline = page.getByRole('slider', { name: 'Seek' });
  await expect(timeline).toBeEnabled();
  const bounds = await timeline.boundingBox();
  if (!bounds) throw new Error('timeline has no clickable bounds');
  const before = Number(await timeline.inputValue());
  const seekResponse = streamed
    ? page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/seek'))
    : undefined;
  const x = before < 500 ? bounds.width / 2 : 1;
  await timeline.click({ position: { x, y: bounds.height / 2 } });
  const target = Number(await timeline.inputValue());
  if (seekResponse) {
    const response = await seekResponse;
    expect(response.ok()).toBeTruthy();
    const requested = Number((response.request().postDataJSON() as { position_ms: number }).position_ms);
    const body = await response.json() as { resume_ms: number };
    expect(requested).not.toBe(before);
    expect(body.resume_ms).toBe(requested);
  } else {
    expect(target).not.toBe(before);
    await expect.poll(() => page.locator('video').evaluate((element) => Math.round((element as HTMLVideoElement).currentTime * 1000))).toBe(target);
  }
}
