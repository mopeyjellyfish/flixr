/** Production acceptance path: this suite uses the built flixr binary and never mocks its API. */
import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const viewports = [
  { name: 'tv', width: 1920, height: 1080 },
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 1024, height: 768 },
  { name: 'phone', width: 390, height: 844 },
];

test('built binary completes setup, scan, profile, browse, and detail flow', async ({ page }, testInfo) => {
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
  await page.getByRole('button', { name: /claim flixr/i }).click();
  await expect(page.getByRole('heading', { name: /keep your local cinema ready/i })).toBeVisible();
  await page.getByLabel(/films root/i).fill(filmsRoot);
  await page.getByLabel(/tv root/i).fill(tvRoot);
  await page.getByRole('button', { name: /save roots/i }).click();
  await expect(page.getByRole('status')).toContainText(/library roots saved/i);
  await page.getByRole('button', { name: /start scan/i }).click();
  await expect(page.getByText(/complete: 3 scanned, 0 unmatched, 0 failed/i)).toBeVisible({ timeout: 30_000 });
  await page.getByLabel(/^name$/i).fill('Production viewer');
  await page.getByRole('button', { name: /create profile/i }).click();
  await page.getByRole('button', { name: /log out/i }).click();
  await page.getByRole('button', { name: /production viewer/i }).click();

  await expect(page.getByRole('heading', { name: /blue horizon 2026/i })).toBeVisible();
  await expect(page.getByTestId(/card-.*$/)).toHaveCount(2);
  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    await page.goto('/home');
    await expect(page.getByRole('button', { name: /view details for blue horizon 2026/i })).toBeVisible();
    const axe = await new AxeBuilder({ page }).analyze();
    expect(axe.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`production-home-${viewport.name}.png`), fullPage: true });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole('button', { name: /series signal/i }).click();
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
  expect(errors).toEqual([]);
});
