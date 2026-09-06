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
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });

  await page.goto('/setup');
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
  await expect(page.getByRole('button', { name: /view details for blue horizon 2026/i })).toBeFocused();
  await page.getByRole('button', { name: /film compatibility check 2026/i }).click();
  await page.getByRole('button', { name: /play compatibility check 2026/i }).click();
  await expectPlayback(page);
  await page.screenshot({ path: testInfo.outputPath('production-transcode-playback-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: /back to library/i }).click();

  await page.getByRole('button', { name: /series signal/i }).click();
  await page.getByRole('button', { name: /play s1 e1 signal/i }).click();
  await expectPlayback(page);
  const seekResponse = page.waitForResponse((response) => response.url().includes('/playback/sessions/') && response.url().endsWith('/seek'));
  await page.locator('video').evaluate((video) => {
    const media = video as HTMLVideoElement;
    media.currentTime = 0.1;
    media.dispatchEvent(new Event('seeked', { bubbles: true }));
  });
  expect((await seekResponse).ok()).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('production-remux-playback-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: /back to library/i }).click();

  await page.getByRole('region', { name: 'Continue Watching' }).getByRole('button', { name: /series signal/i }).click();
  await expect(page.getByRole('dialog')).toContainText(/S1 E1 Signal/i);
  await expect(page).toHaveURL(/\/detail\//);
  await page.reload();
  await expect(page.getByRole('dialog')).toContainText(/S1 E1 Signal/i);
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
});

async function expectPlayback(page: import('@playwright/test').Page) {
  const video = page.locator('video');
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState), { timeout: 20_000 }).toBeGreaterThan(0);
  await page.getByRole('button', { name: 'Play', exact: true }).click();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).currentTime > 0 || (element as HTMLVideoElement).ended), { timeout: 20_000 }).toBeTruthy();
}
