import { expect, test } from '@playwright/test';

const detail = { series_id: 'series-1', title: 'Night Relay', order: 'dvd', revision: 4, needs_repair: false, entries: [
  { catalog_id: 'episode-1', title: 'First relay', season: 1, episode: 1, episode_end: 2, mapping: { position: 2, end_position: 3, season: 1, episode: 4, episode_end: 5, special: false } },
  { catalog_id: 'episode-2', title: 'Second relay', season: 1, episode: 3, episode_end: 3, mapping: { position: 1, end_position: 1, season: 1, episode: 1, episode_end: 1, special: false } },
] };

test('owner saves an alternate order and keeps feedback usable on a phone viewport', async ({ page }) => {
  let saves = 0;
  let savedDetail = detail;
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith('/setup/status')) return route.fulfill({ json: { claimed: true, readiness: { ffprobe: true, ffmpeg: true } } });
    if (path.endsWith('/owner/episode-orders') && request.method() === 'GET') return route.fulfill({ json: { series: [{ id: 'series-1', title: 'Night Relay' }], total: 1 } });
    if (path.endsWith('/owner/episode-orders/series-1/groups')) return route.fulfill({ json: { groups: [{ id: 'dvd', name: 'DVD order', order: 'dvd' }] } });
    if (path.endsWith('/owner/episode-orders/series-1/preview')) return route.fulfill({ json: detail });
    if (path.endsWith('/owner/episode-orders/series-1') && request.method() === 'GET') return route.fulfill({ json: savedDetail });
    if (path.endsWith('/owner/episode-orders/series-1') && request.method() === 'PUT') {
      saves += 1;
      const body = request.postDataJSON() as { order: 'aired' | 'dvd' | 'absolute'; entries: typeof detail.entries };
      savedDetail = { ...detail, order: body.order, entries: body.entries, revision: 5 };
      return route.fulfill(saves === 1 ? { json: savedDetail } : { status: 409, json: { error: { code: 'episode_order_conflict' } } });
    }
    if (path.endsWith('/catalog/view')) return route.fulfill({ json: { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Series', items: [{ id: 'series-1', title: 'Night Relay', kind: 'series', local_only: true, listed: false }] }] } });
    if (path.endsWith('/catalog/items/series-1')) return route.fulfill({ json: { id: 'series-1', title: 'Night Relay', kind: 'series', local_only: true, listed: false } });
    if (path.endsWith('/catalog/series/series-1')) return route.fulfill({ json: {
      id: 'series-1', title: 'Night Relay', kind: 'series', local_only: true, episode_order: savedDetail.order, order_needs_repair: false,
      seasons: [{ id: 'source-season', number: 1, episodes: [
        { id: 'episode-1', title: 'First relay', kind: 'episode', season: 1, episode: 1, episode_end: 2, local_only: true, playable: true, episode_order: savedDetail.entries.find((entry) => entry.catalog_id === 'episode-1')?.mapping },
        { id: 'episode-2', title: 'Second relay', kind: 'episode', season: 1, episode: 3, episode_end: 3, local_only: true, playable: true, episode_order: savedDetail.entries.find((entry) => entry.catalog_id === 'episode-2')?.mapping },
      ] }],
    } });
    if (path.endsWith('/owner/libraries')) return route.fulfill({ json: { libraries: [] } });
    if (path.endsWith('/owner/scan/jobs')) return route.fulfill({ json: { jobs: [] } });
    if (path.endsWith('/owner/scan/status')) return route.fulfill({ json: { scan: {}, locations: [] } });
    if (path.endsWith('/owner/settings/tmdb')) return route.fulfill({ json: { enabled: true, configured: false, source: 'none', state: 'unavailable', message: 'Unavailable' } });
    if (path.endsWith('/owner/metadata/unmatched')) return route.fulfill({ json: { items: [] } });
    if (path.endsWith('/owner/identity/repairs')) return route.fulfill({ json: { conflicts: [], merges: [] } });
    if (path.endsWith('/owner/settings/playback')) return route.fulfill({ json: { segment_dir: '/tmp/segments', generation_bytes: 1, global_bytes: 1, max_generations: 1 } });
    if (path.endsWith('/owner/playback/status')) return route.fulfill({ json: { settings: { segment_dir: '/tmp/segments', generation_bytes: 1, global_bytes: 1, max_generations: 1 }, generations: [] } });
    if (path.endsWith('/owner/screens')) return route.fulfill({ json: { screens: [] } });
    if (path.endsWith('/owner/settings')) return route.fulfill({ json: { settings: [] } });
    if (path.endsWith('/owner/backups')) return route.fulfill({ json: { policy: { last_status: 'never' }, jobs: [] } });
    if (path.endsWith('/profiles')) return route.fulfill({ json: { profiles: [] } });
    return route.fulfill({ json: {} });
  });
  await page.goto('/owner');
  await page.getByRole('button', { name: 'Edit episode order' }).click();
  await page.getByLabel('Find a series').fill('night');
  await page.getByRole('button', { name: 'Find series' }).click();
  await page.getByRole('button', { name: 'Night Relay' }).click();
  await page.getByRole('button', { name: 'Load provider groups' }).click();
  await page.getByLabel('Provider group').selectOption('dvd');
  await page.getByRole('button', { name: 'Preview provider mapping' }).click();
  await page.getByRole('button', { name: 'Save episode order' }).click();
  await expect(page.getByText('Episode order saved.')).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).resolves.toBeTruthy();
  await page.getByRole('button', { name: 'Save episode order' }).click();
  await expect(page.getByRole('alert')).toContainText(/changed elsewhere/i);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/detail/series-1');
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.getByText('DVD order')).toBeVisible();
  await expect(page.locator('.episode-row strong').allTextContents()).resolves.toEqual(['S1 · E1 · Second relay', 'S1 · E4–5 · First relay']);
  await expect(page.getByRole('button', { name: /mark season watched/i })).toHaveCount(0);
});
