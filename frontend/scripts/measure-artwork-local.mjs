import { chromium } from '@playwright/test';

const baseURL = process.env.FLIXR_MEASURE_URL;
if (!baseURL) throw new Error('Set FLIXR_MEASURE_URL to the isolated server URL.');

const observer = `(() => {
  const state = { start: performance.now(), visibleDecoded: new Set(), watched: new WeakSet(), firstVisibleDecoded: null, grid30Decoded: null };
  const watch = (img) => {
    if (!(img instanceof HTMLImageElement) || state.watched.has(img) || !img.src.includes('/api/v1/catalog/artwork/')) return;
    state.watched.add(img);
    img.decode().then(() => {
      const source = img.currentSrc || img.src;
      const rect = img.getBoundingClientRect();
      if (rect.bottom <= 0 || rect.top >= innerHeight || state.visibleDecoded.has(source)) return;
      state.visibleDecoded.add(source);
      const elapsed = performance.now() - state.start;
      if (state.firstVisibleDecoded === null) state.firstVisibleDecoded = elapsed;
      if (state.visibleDecoded.size === 30) state.grid30Decoded = elapsed;
    }).catch(() => {});
  };
  new MutationObserver((changes) => changes.forEach((change) => change.addedNodes.forEach((node) => {
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    if (node.matches?.('img')) watch(node);
    node.querySelectorAll?.('img').forEach(watch);
  }))).observe(document, { childList: true, subtree: true });
  document.querySelectorAll('img').forEach(watch);
  window.__issue56Artwork = state;
})()`;

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1920, height: 2160 } });
await page.goto(`${baseURL}/profiles`, { waitUntil: 'networkidle' });
await page.evaluate(async () => {
  const profiles = await (await fetch('/api/v1/profiles')).json();
  const alex = profiles.profiles.find((profile) => profile.name === 'Alex');
  if (!alex) throw new Error('The isolated demo must contain the Alex profile.');
  await fetch(`/api/v1/profiles/${alex.id}/select`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ pin: '' }) });
  await fetch('/api/v1/catalog/preferences/film', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ view: 'grid', sort: 'title' }) });
});
await page.addInitScript(observer);
async function measure(navigate) {
  await navigate();
  await page.waitForFunction(() => window.__issue56Artwork?.grid30Decoded !== null, null, { timeout: 30_000 });
  return page.evaluate(() => {
    const visibleArtworkImages = [...document.images].filter((image) => image.src.includes('/api/v1/catalog/artwork/') && image.getBoundingClientRect().bottom > 0 && image.getBoundingClientRect().top < innerHeight).length;
    if (visibleArtworkImages < 30) throw new Error(`Expected 30 visible artwork images, got ${visibleArtworkImages}.`);
    return { first_visible_decoded_ms: window.__issue56Artwork.firstVisibleDecoded, visible_grid_30_decoded_ms: window.__issue56Artwork.grid30Decoded, visible_decoded_images: window.__issue56Artwork.visibleDecoded.size, visible_artwork_images: visibleArtworkImages };
  });
}
const initial = await measure(() => page.goto(`${baseURL}/movies`, { waitUntil: 'networkidle' }));
const repeat = await measure(() => page.reload({ waitUntil: 'networkidle' }));
console.log(JSON.stringify({ initial, repeat }, null, 2));
await browser.close();
