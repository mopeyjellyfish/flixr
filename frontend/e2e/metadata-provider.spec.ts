import { expect, test } from '@playwright/test';

type CatalogItem = { id: string; title: string; kind: string; year: number; synopsis: string; poster: string; backdrop: string };
type SeriesDetail = CatalogItem & { seasons: Array<{ episodes: CatalogItem[] }> };

test('automatic normal-mode metadata survives reverse-proxy restart offline', async ({ page, browser }, testInfo) => {
  test.setTimeout(90_000);
  const configResponse = await page.request.get('/__acceptance/config');
  expect(configResponse.ok()).toBeTruthy();
  const config = await configResponse.json() as { setup_token: string; films: string; tv: string };
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });

  await page.goto('/setup');
  await expect(page.locator('[data-app-content]')).not.toHaveAttribute('inert');
  await page.getByLabel(/setup token/i).fill(config.setup_token);
  await page.getByLabel(/owner password/i).fill('metadata-acceptance-password');
  await page.getByRole('button', { name: /secure this server/i }).click();
  await expect(page.getByRole('heading', { name: /how would you like to begin/i })).toBeVisible();
  await page.getByRole('button', { name: /start fresh/i }).click();
  await expect(page.getByRole('heading', { name: /bring your libraries home/i })).toBeVisible();
	await expect(page.getByText(/automatic TMDB access is available/i)).toBeVisible();
  await page.getByLabel(/films library/i).fill(config.films);
  await page.getByLabel(/tv library/i).fill(config.tv);
  await page.getByRole('button', { name: /save libraries/i }).click();
  await expect(page.getByRole('heading', { name: /profile/i })).toBeVisible();
  await expect.poll(async () => page.evaluate(async () => {
    const response = await fetch('/api/v1/owner/scan/status');
    const body = await response.json() as { scan: { status: string; failed: number } };
    return `${body.scan.status}:${body.scan.failed}`;
  }), { timeout: 30_000 }).toBe('complete:0');
  await page.getByLabel(/^name$/i).fill('Metadata viewer');
  await page.getByRole('button', { name: /create profile/i }).click();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByText('Curated Blue Horizon', { exact: false }).first()).toBeVisible();

  const catalogResponse = await page.request.get('/api/v1/catalog/home?offset=0&limit=48');
  expect(catalogResponse.ok()).toBeTruthy();
  const catalog = await catalogResponse.json() as { items: CatalogItem[] };
  const film = catalog.items.find((item) => item.kind === 'film' && item.title === 'Curated Blue Horizon');
  const seriesSummary = catalog.items.find((item) => item.kind === 'series' && item.title === 'Signal Archive');
  expect(film).toMatchObject({ year: 2026, synopsis: 'Verified film synopsis' });
  expect(film?.poster).toMatch(/^\/api\/v1\/catalog\/artwork\//);
  expect(film?.backdrop).toMatch(/^\/api\/v1\/catalog\/artwork\//);
  expect(seriesSummary).toMatchObject({ year: 2025, synopsis: 'Verified series synopsis' });
  expect(seriesSummary?.poster).toMatch(/^\/api\/v1\/catalog\/artwork\//);
  expect(seriesSummary?.backdrop).toMatch(/^\/api\/v1\/catalog\/artwork\//);
  const seriesResponse = await page.request.get(`/api/v1/catalog/series/${seriesSummary!.id}`);
  expect(seriesResponse.ok()).toBeTruthy();
  const series = await seriesResponse.json() as SeriesDetail;
  const episode = series.seasons[0]?.episodes[0];
  expect(episode).toMatchObject({ title: 'Verified Episode 1', year: 2025, synopsis: 'Verified episode synopsis' });
  expect(episode?.backdrop).toMatch(/^\/api\/v1\/catalog\/artwork\//);

  const artwork = page.locator('img[src^="/api/v1/catalog/artwork/"]').first();
  await artwork.scrollIntoViewIfNeeded();
  await expect.poll(() => artwork.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBeTruthy();
  const artworkSource = await artwork.getAttribute('src');
  expect(artworkSource).toMatch(/^\/api\/v1\/catalog\/artwork\//);
  const authenticatedArtwork = await page.request.get(artworkSource!);
  expect(authenticatedArtwork.headers()['content-type']).toBe('image/png');
  const anonymous = await browser.newContext();
  expect((await anonymous.request.get(new URL(artworkSource!, page.url()).href)).status()).toBe(403);
  await anonymous.close();

  expect(await page.evaluate(async (id) => (await fetch(`/api/v1/progress/${id}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ position_ms: 1234, generation: 0, observed_at: Date.now() }) })).ok, film!.id)).toBeTruthy();

  expect((await page.request.post('/__acceptance/restart-offline')).status()).toBe(204);
  await page.reload();
  await expect(page.getByText('Curated Blue Horizon', { exact: false }).first()).toBeVisible();
  expect(await page.evaluate(async (source) => new Promise<boolean>((resolve) => {
    const image = new Image();
    image.onload = () => resolve(image.naturalWidth > 0);
    image.onerror = () => resolve(false);
    image.src = `${source}${source.includes('?') ? '&' : '?'}after-restart=1`;
  }), artworkSource!)).toBeTruthy();
  const offlineCatalog = await (await page.request.get('/api/v1/catalog/home?offset=0&limit=48')).json() as { items: CatalogItem[] };
  expect(offlineCatalog.items.map((item) => item.id)).toEqual(catalog.items.map((item) => item.id));
  const offlineSeries = await (await page.request.get(`/api/v1/catalog/series/${series.id}`)).json() as SeriesDetail;
  expect(offlineSeries.seasons[0]?.episodes[0]).toMatchObject({ id: episode!.id, title: 'Verified Episode 1', synopsis: 'Verified episode synopsis' });
  const progress = await page.request.get(`/api/v1/progress/${film!.id}`);
  expect(await progress.json()).toMatchObject({ position_ms: 1234 });
  expect(errors).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath('metadata-after-offline-restart.png'), fullPage: true });
});
