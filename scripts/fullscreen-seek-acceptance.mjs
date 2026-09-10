import { writeFileSync } from 'node:fs';
import { chromium } from '../frontend/node_modules/playwright/index.mjs';

const baseURL = process.env.FLIXR_TEST_BASE;
const output = process.env.FLIXR_TEST_OUTPUT;
const password = process.env.FLIXR_TEST_PASSWORD ?? 'mobile-acceptance-password';
const query = process.env.FLIXR_TEST_QUERY ?? 'Fullscreen';
if (!baseURL || !output) throw new Error('FLIXR_TEST_BASE and FLIXR_TEST_OUTPUT are required');
const testURL = new URL(baseURL);
if (testURL.hostname !== '127.0.0.1' && testURL.hostname !== 'localhost') throw new Error('Fullscreen seek acceptance only runs against an isolated loopback server');

const browser = await chromium.launch();
const context = await browser.newContext({ baseURL, viewport: { width: 390, height: 844 } });
const page = await context.newPage();
const result = { revision: process.env.FLIXR_TEST_REVISION, image: process.env.FLIXR_TEST_IMAGE, query };
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const post = async (path, data) => {
  const response = await page.request.post(`/api/v1/${path}`, { data, headers: { origin: baseURL } });
  assert(response.ok(), `${path} status ${response.status()}`);
  return response.json();
};
const fullscreen = () => page.evaluate(() => Boolean(document.fullscreenElement));
const sample = () => page.locator('video').evaluate((video) => {
  const canvas = document.createElement('canvas');
  canvas.width = 1;
  canvas.height = 1;
  const drawing = canvas.getContext('2d');
  if (!drawing) throw new Error('Canvas is unavailable');
  drawing.drawImage(video, 0, 0, 1, 1);
  return {
    rgb: Array.from(drawing.getImageData(0, 0, 1, 1).data).slice(0, 3),
    time: video.currentTime,
    paused: video.paused,
  };
});
const matches = (rgb, channel) => rgb[channel] > 150 && rgb.filter((_, index) => index !== channel).every((value) => value < 50);

try {
  await post('owner/login', { password });
  await post('owner/scan', {});
  const profile = await post('profiles', { name: `Fullscreen acceptance ${Date.now()}`, pin: '' });
  await post(`profiles/${profile.id}/select`, { pin: '' });
  let item;
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const response = await page.request.get(`/api/v1/catalog/search?q=${encodeURIComponent(query)}`);
    const catalog = await response.json();
    item = catalog.items?.find((candidate) => candidate.kind === 'film');
    if (item) break;
    await page.waitForTimeout(500);
  }
  assert(item, `No generated color probe matching ${query} was indexed`);

  await page.goto(`/play/${item.id}`);
  await page.waitForFunction(() => {
    const video = document.querySelector('video');
    return video && !video.paused && video.currentTime > 0;
  }, null, { timeout: 60_000 });
  await page.mouse.move(200, 700);
  await page.getByRole('button', { name: 'Fullscreen', exact: true }).click();
  await page.waitForFunction(() => Boolean(document.fullscreenElement));
  await page.evaluate(() => {
    window.__fullscreenSeekVideo = document.querySelector('video');
    window.__fullscreenSeekEvents = [];
    document.addEventListener('fullscreenchange', () => window.__fullscreenSeekEvents.push(Boolean(document.fullscreenElement)));
  });

  await page.route('**/seek', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1_500));
    await route.continue();
  });
  const forwardReply = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/seek'));
  const slider = page.getByRole('slider', { name: 'Seek', exact: true });
  const sliderBox = await slider.boundingBox();
  assert(sliderBox, 'Seek slider has no layout box');
  await page.mouse.click(sliderBox.x + sliderBox.width * (50 / 120), sliderBox.y + sliderBox.height / 2);
  await page.waitForTimeout(500);
  result.fullscreenWhileSeeking = await fullscreen();
  result.loadingText = await page.locator('.player-loading').textContent();
  const forwardResponse = await forwardReply;
  assert(forwardResponse.ok(), `Forward seek status ${forwardResponse.status()}`);
  const forwardPlan = await forwardResponse.json();
  const forwardTarget = forwardResponse.request().postDataJSON().position_ms;
  await page.waitForFunction(({ offset, target }) => {
    const video = document.querySelector('video');
    return video && !video.paused && video.readyState >= 2 && Math.abs(video.currentTime * 1000 + offset - target) < 5_000;
  }, { offset: forwardPlan.stream_offset_ms, target: forwardTarget }, { timeout: 30_000 });
  const forwardSamples = [];
  for (let attempt = 0; attempt < 15; attempt += 1) {
    forwardSamples.push(await sample());
    await page.waitForTimeout(100);
  }
  result.forwardSample = forwardSamples.find(({ rgb }) => matches(rgb, 2));
  assert(result.forwardSample, 'Forward seek counter advanced without decoding the target blue frame');

  const reverseReply = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/seek'));
  const reverseBox = await slider.boundingBox();
  assert(reverseBox, 'Seek slider has no reverse-seek layout box');
  await page.mouse.click(reverseBox.x + reverseBox.width * (5 / 120), reverseBox.y + reverseBox.height / 2);
  const reverseResponse = await reverseReply;
  assert(reverseResponse.ok(), `Reverse seek status ${reverseResponse.status()}`);
  const reversePlan = await reverseResponse.json();
  await page.waitForFunction(({ offset }) => {
    const video = document.querySelector('video');
    return video && !video.paused && video.readyState >= 2 && Math.abs(video.currentTime * 1000 + offset - 5_000) < 4_000;
  }, { offset: reversePlan.stream_offset_ms }, { timeout: 30_000 });
  result.reverseSample = await sample();
  result.fullscreenAfterSeeks = await fullscreen();
  result.sameVideo = await page.evaluate(() => window.__fullscreenSeekVideo === document.querySelector('video'));
  result.fullscreenEvents = await page.evaluate(() => window.__fullscreenSeekEvents);
  assert(matches(result.reverseSample.rgb, 0), 'Reverse seek did not decode the target red frame');
  assert(result.fullscreenWhileSeeking && result.fullscreenAfterSeeks && result.fullscreenEvents.every(Boolean), 'Fullscreen was lost during source replacement');
  assert(result.sameVideo, 'Source replacement mounted a different video element');
  assert(!result.loadingText?.trim(), 'Loading overlay contains visible status text');
  result.pass = true;
} catch (error) {
  result.failureState = await page.locator('video').evaluate((video) => ({
    paused: video.paused,
    time: video.currentTime,
    ready: video.readyState,
    seeking: video.seeking,
    buffered: Array.from({ length: video.buffered.length }, (_, index) => [video.buffered.start(index), video.buffered.end(index)]),
  })).catch(() => null);
  result.error = String(error).slice(0, 250);
  result.pass = false;
  process.exitCode = 1;
} finally {
  await browser.close();
  writeFileSync(output, JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result));
}
