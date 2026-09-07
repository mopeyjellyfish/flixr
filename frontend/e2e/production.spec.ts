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
  await page.screenshot({ path: testInfo.outputPath('production-direct-playback-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('production-direct-playback-phone.png'), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole('button', { name: /back to library/i }).click();
  await expect(page.locator('[data-catalog-id]:focus')).toHaveAccessibleName(/blue horizon 2026/i);
  await page.getByRole('button', { name: /film compatibility check 2026/i }).click();
  await page.getByRole('button', { name: /play compatibility check 2026/i }).click();
  await expectPlayback(page);
  await page.screenshot({ path: testInfo.outputPath('production-transcode-playback-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: /back to library/i }).click();

  await page.getByRole('button', { name: /series signal/i }).click();
  await page.getByRole('button', { name: /play s1 e1 signal/i }).click();
  await expectPlayback(page);
  const audio = page.getByRole('combobox', { name: /audio track/i });
  await expect(audio).toBeVisible();
  await expect(audio.locator('option')).toHaveText([/English/, /French/, /Director Commentary.*Japanese.*External/]);
  const seekResponse = page.waitForResponse((response) => response.url().includes('/playback/sessions/') && response.url().endsWith('/seek'));
  await page.locator('video').evaluate((video) => {
    const media = video as HTMLVideoElement;
    media.currentTime = 0.1;
    media.dispatchEvent(new Event('seeked', { bubbles: true }));
  });
  expect((await seekResponse).ok()).toBeTruthy();
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
  expect(errors).toEqual([]);
  expect(externalRequests).toEqual([]);
});

async function expectPlayback(page: import('@playwright/test').Page) {
  const video = page.locator('video');
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState), { timeout: 20_000 }).toBeGreaterThan(0);
  await page.getByRole('button', { name: 'Play', exact: true }).click();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).currentTime > 0 || (element as HTMLVideoElement).ended), { timeout: 20_000 }).toBeTruthy();
}

async function expectPlaybackStartedAutomatically(page: import('@playwright/test').Page) {
  const video = page.locator('video');
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState), { timeout: 20_000 }).toBeGreaterThan(0);
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).currentTime > 0 || (element as HTMLVideoElement).ended), { timeout: 20_000 }).toBeTruthy();
}
