import { chromium } from '../frontend/node_modules/playwright/index.mjs';
import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';

const baseURL = process.env.FLIXR_TEST_BASE;
const container = process.env.FLIXR_TEST_CONTAINER;
const output = process.env.FLIXR_TEST_OUTPUT;
if (!baseURL || !container || !output) throw new Error('The mobile acceptance environment is incomplete');
const initialMbps = Number(process.env.FLIXR_TEST_NETWORK_MBPS ?? 2);
const dipMbps = Number(process.env.FLIXR_TEST_DIP_MBPS ?? 1);
const dipStart = Number(process.env.FLIXR_TEST_DIP_START ?? 20);
const dipEnd = Number(process.env.FLIXR_TEST_DIP_END ?? 35);

const browser = await chromium.launch();
const context = await browser.newContext({ baseURL, viewport: { width: 390, height: 844 } });
const page = await context.newPage();
const result = {
  revision: process.env.FLIXR_TEST_REVISION,
  image: process.env.FLIXR_TEST_IMAGE,
  plans: [],
  samples: [],
  errors: [],
  network: { initial_mbps: initialMbps, dip_mbps: dipMbps, dip_start_second: dipStart, dip_end_second: dipEnd, latency_ms: 100 },
};
const post = async (path, data) => {
  const response = await page.request.post(`/api/v1/${path}`, { data, headers: { origin: baseURL } });
  if (!response.ok()) throw new Error(`${path} status ${response.status()}`);
  return response.json();
};

page.on('response', async (response) => {
  if (response.request().method() !== 'POST' || !/\/playback\//.test(response.url()) || /heartbeat|stop/.test(response.url())) return;
  try {
    const body = await response.json();
    if (body.plan) result.plans.push({ kind: body.plan.kind, video_bitrate: body.plan.video_bitrate, audio_bitrate: body.plan.audio_bitrate, width: body.plan.width, height: body.plan.height, offset: body.stream_offset_ms });
  } catch { /* only playback plan responses are evidence */ }
});

try {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try { if ((await page.request.get('/api/v1/setup/status')).ok()) break; } catch { /* server is starting */ }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const status = await (await page.request.get('/api/v1/setup/status')).json();
  if (!status.claimed) {
    const log = execFileSync('docker', ['logs', container], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
    const token = log.match(/Flixr setup token: (\S+)/)?.[1];
    if (!token) throw new Error('The isolated setup token was not found');
    await post('setup/claim', { token, password: 'mobile-acceptance-password' });
    await post('owner/roots', { films: '/media/films', tv: '/media/tv' });
    await post('profiles', { name: 'Mobile tester', pin: '' });
    await post('owner/scan', {});
  } else {
    await post('owner/login', { password: 'mobile-acceptance-password' });
  }
  const metadata = await page.request.put('/api/v1/owner/settings/tmdb', { data: { enabled: false }, headers: { origin: baseURL } });
  if (!metadata.ok()) throw new Error('Metadata could not be disabled in the isolated fixture');
  const profiles = await (await page.request.get('/api/v1/profiles')).json();
  await post(`profiles/${profiles.profiles[0].id}/select`, { pin: '' });
  let item;
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const search = await (await page.request.get('/api/v1/catalog/search?q=Mobile')).json();
    item = search.items?.find((candidate) => candidate.kind === 'film');
    if (item) break;
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  if (!item) throw new Error('The generated fixture was not indexed');

  await page.goto('/home');
  const cdp = await context.newCDPSession(page);
  await cdp.send('Network.enable');
  const network = (mbps) => cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 100, downloadThroughput: mbps * 1_000_000 / 8, uploadThroughput: 1_000_000 / 8 });
  await network(initialMbps);
  await page.addInitScript(() => {
    window.__mobileEvents = [];
    for (const event of ['waiting', 'playing']) document.addEventListener(event, ({ target }) => {
      if (target instanceof HTMLVideoElement) window.__mobileEvents.push({ event, at: performance.now(), time: target.currentTime });
    }, true);
  });
  const started = Date.now();
  await page.goto(`/play/${item.id}`);
  await page.waitForFunction(() => {
    const video = document.querySelector('video');
    return video && video.currentTime > 0 && !video.paused;
  }, null, { timeout: 90_000 });
  result.startupMS = Date.now() - started;

  const observed = Date.now();
  for (let second = 0; second < 70; second += 1) {
    if (second === dipStart) await network(dipMbps);
    if (second === dipEnd) await network(initialMbps);
    result.samples.push(await page.locator('video').evaluate((video) => ({
      time: video.currentTime,
      paused: video.paused,
      ready: video.readyState,
      buffer: video.buffered.length ? video.buffered.end(video.buffered.length - 1) - video.currentTime : 0,
      frames: video.getVideoPlaybackQuality().totalVideoFrames,
    })));
    await page.waitForTimeout(1000);
  }
  result.observationMS = Date.now() - observed;
  result.events = await page.evaluate(() => window.__mobileEvents);
  result.advancedSeconds = result.samples.at(-1).time - result.samples[0].time;
  result.stalledSamples = result.samples.slice(1).filter((sample, index) => sample.time - result.samples[index].time < 0.2).length;
  result.decodedAdvancementSeconds = result.samples.slice(1).reduce((sum, sample, index) => sum + Math.max(0, Math.min(1.5, sample.time - result.samples[index].time)), 0);
  result.pass = result.decodedAdvancementSeconds >= 55 && result.stalledSamples <= 10;
  if (!result.pass) process.exitCode = 1;
} catch (error) {
  result.error = String(error).slice(0, 250);
  process.exitCode = 1;
} finally {
  await browser.close();
  writeFileSync(output, JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ ...result, samples: undefined, events: undefined }));
}
