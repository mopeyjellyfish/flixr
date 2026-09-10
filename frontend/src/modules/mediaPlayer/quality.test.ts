import { afterEach, expect, it } from 'vitest';
import { AdaptiveQualityPolicy, initialAutoQualityTier, loadQualityPreference, qualityRequest, rememberAutoThroughput, saveQualityPreference } from './quality';

afterEach(() => localStorage.clear());

it('defaults to Auto and persists an explicit device preference', () => {
  expect(loadQualityPreference()).toBe('auto');
  saveQualityPreference('data_saver');
  expect(loadQualityPreference()).toBe('data_saver');
  localStorage.setItem('flixr.playback.quality', 'unbounded');
  expect(loadQualityPreference()).toBe('auto');
});

it('maps controls to the supported server quality contract', () => {
  expect(qualityRequest('auto', 'balanced')).toEqual({ mode: 'auto', max_video_bitrate: 2_500_000, max_width: 1280, max_height: 720 });
  expect(qualityRequest('auto', 'saver')).toEqual({ mode: 'auto', max_video_bitrate: 1_000_000, max_width: 854, max_height: 480 });
  expect(qualityRequest('auto', 'low')).toEqual({ mode: 'auto', max_video_bitrate: 500_000, max_width: 640, max_height: 360 });
  expect(qualityRequest('data_saver', 'balanced')).toEqual({ mode: 'data_saver', max_video_bitrate: 1_000_000, max_width: 854, max_height: 480 });
  expect(qualityRequest('original', 'balanced')).toEqual({ mode: 'original' });
});

it('starts unknown and stale connections at bounded HD while honoring fresh same-server evidence', () => {
  expect(initialAutoQualityTier('https://flixr.local', 1_000)).toBe('balanced');

  rememberAutoThroughput('https://flixr.local', 900_000, 2_000);
  expect(initialAutoQualityTier('https://flixr.local', 3_000)).toBe('low');
  expect(initialAutoQualityTier('https://another.local', 3_000)).toBe('balanced');
  expect(initialAutoQualityTier('https://flixr.local', 24 * 60 * 60 * 1_000 + 2_001)).toBe('balanced');
});

it('requires sustained stalls, then applies cooldown and stable recovery hysteresis', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  expect(policy.stall(1_000)).toBe(false);
  expect(policy.stall(8_000)).toBe(true);
  policy.commit('low', 8_000);
  expect(policy.stall(20_000)).toBe(false);
  expect(policy.throughput(5_000_000, 12, 100_000)).toBeUndefined();
  for (const now of [190_000, 210_000, 230_000]) policy.throughput(5_000_000, 12, now);
  expect(policy.throughput(5_000_000, 12, 250_000)).toBe('up');
  policy.commit('saver', 250_000);
  expect(policy.stall(310_000)).toBe(false);
  expect(policy.stall(320_000)).toBe(true);
});

it('recovers after sustained headroom sampled at irregular fragment times', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  policy.stall(1_000);
  expect(policy.stall(8_000)).toBe(true);
  policy.commit('low', 8_000);
  for (const now of [190_000, 211_000, 232_000]) {
    expect(policy.throughput(5_000_000, 12, now)).toBeUndefined();
  }
  expect(policy.throughput(5_000_000, 12, 252_000)).toBe('up');
});

it('ramps the conservative startup tier after measured fast-link headroom', () => {
  const policy = new AdaptiveQualityPolicy();
  for (const now of [1_000, 5_000, 9_000]) expect(policy.throughput(5_000_000, 12, now)).toBeUndefined();
  expect(policy.throughput(5_000_000, 12, 16_000)).toBe('up');
  expect(policy.proposedTier).toBe('saver');
});

it('exposes only sustained conservative throughput as rememberable evidence', () => {
  const policy = new AdaptiveQualityPolicy('balanced');
  for (const [now, rate] of [[1_000, 8_000_000], [4_000, 7_000_000], [7_000, 2_000_000]] as const) {
    policy.throughput(rate, 10, now);
    expect(policy.rememberableThroughput).toBeUndefined();
  }
  policy.throughput(6_000_000, 10, 10_000);
  expect(policy.rememberableThroughput).toBe(2_000_000);

  const burst = new AdaptiveQualityPolicy('balanced');
  for (const now of [1_000, 20_000, 20_100, 20_200, 20_300]) burst.throughput(6_000_000, 10, now);
  expect(burst.rememberableThroughput).toBeUndefined();
});

it('accounts for declared video plus audio demand when detecting insufficient delivery', () => {
  const policy = new AdaptiveQualityPolicy('balanced');
  for (const now of [1_000, 3_000, 5_000, 7_000]) expect(policy.throughput(3_000_000, 3, now, 3_400_000)).toBeUndefined();
  expect(policy.throughput(3_000_000, 3, 9_000, 3_400_000)).toBe('down');
});

it('treats repeated progress callbacks from one fragment as one throughput sample', () => {
  const policy = new AdaptiveQualityPolicy('balanced');
  for (const now of [1_000, 3_000, 5_000, 7_000, 9_000]) expect(policy.throughput(800_000, 3, now, 2_600_000, 'fragment-1')).toBeUndefined();
  expect(policy.rememberableThroughput).toBeUndefined();
  for (const [now, id] of [[11_000, 'fragment-2'], [14_000, 'fragment-3'], [17_000, 'fragment-4'], [20_000, 'fragment-5']] as const) policy.throughput(800_000, 3, now, 2_600_000, id);
  expect(policy.rememberableThroughput).toBe(800_000);
});

it('uses sustained buffered headroom for native HLS without fragment telemetry', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  for (const [now, buffer] of [[1_000, 8.2], [11_000, 11.8], [21_000, 9.1]]) expect(policy.buffer(buffer, true, now)).toBe(false);
  expect(policy.buffer(10.4, true, 31_000)).toBe(true);
  expect(policy.proposedTier).toBe('balanced');

  const interrupted = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 11_000, 21_000]) expect(interrupted.buffer(15, true, now)).toBe(false);
  expect(interrupted.buffer(15, false, 25_000)).toBe(false);
  expect(interrupted.buffer(15, true, 31_000)).toBe(false);

  interrupted.stall(40_000);
  for (const now of [50_000, 60_000, 70_000, 80_000]) expect(interrupted.buffer(15, true, now)).toBe(false);

  const recovered = new AdaptiveQualityPolicy('saver');
  recovered.stall(1_000);
  expect(recovered.stall(8_000)).toBe(true);
  recovered.commit('low', 8_000);
  for (let now = 60_000; now <= 180_000; now += 10_000) expect(recovered.buffer(10, true, now)).toBe(false);
  expect(recovered.buffer(10, true, 190_000)).toBe(true);
  expect(recovered.proposedTier).toBe('saver');
});

it('downshifts a draining native buffer before it empties, even during upgrade cooldown', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 5_000, 9_000, 16_000]) policy.throughput(5_000_000, 12, now);
  policy.commit('balanced', 16_000);

  expect(policy.buffer(8, true, 18_000)).toBe(false);
  expect(policy.buffer(5, true, 21_000)).toBe(false);
  expect(policy.buffer(2, true, 24_000)).toBe(true);
  expect(policy.proposedTier).toBe('saver');
});

it('does not let an upgrade cooldown block an emergency prolonged-stall downshift', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 5_000, 9_000, 16_000]) policy.throughput(5_000_000, 12, now);
  policy.commit('balanced', 16_000);
  policy.stall(18_000);
  expect(policy.prolongedStall(26_100)).toBe(true);
  expect(policy.proposedTier).toBe('saver');
});

it('does not let an upgrade cooldown block sustained insufficient throughput', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 5_000, 9_000, 16_000]) policy.throughput(5_000_000, 12, now);
  policy.commit('balanced', 16_000);
  for (const now of [18_000, 20_000, 22_000, 24_000]) expect(policy.throughput(800_000, 3, now)).toBeUndefined();
  expect(policy.throughput(800_000, 3, 26_000)).toBe('down');
});

it('rescues a constrained startup before the first playing event', () => {
  const policy = new AdaptiveQualityPolicy('balanced');
  policy.beginStartup(1_000);
  expect(policy.startup(3_999)).toBe(false);
  expect(policy.startup(4_000)).toBe(true);
  expect(policy.proposedTier).toBe('saver');

  const measured = new AdaptiveQualityPolicy('balanced');
  measured.beginStartup(1_000);
  measured.throughput(800_000, 0, 2_000, 2_628_000, 'initial-fragment');
  expect(measured.startup(4_000)).toBe(true);
  expect(measured.proposedTier).toBe('low');

  const started = new AdaptiveQualityPolicy('balanced');
  started.beginStartup(1_000);
  started.playing();
  expect(started.startup(20_000)).toBe(false);
});

it('reduces after one prolonged stall and on measured insufficient throughput', () => {
  const prolonged = new AdaptiveQualityPolicy('saver');
  expect(prolonged.stall(1_000)).toBe(false);
  expect(prolonged.prolongedStall(9_100)).toBe(true);

  const measured = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 3_000, 5_000, 7_000]) expect(measured.throughput(800_000, 3, now)).toBeUndefined();
  expect(measured.throughput(800_000, 3, 9_000)).toBe('down');
});

it('uses a transport-safe low rung when saver throughput remains insufficient', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 3_000, 5_000, 7_000]) expect(policy.throughput(800_000, 3, now)).toBeUndefined();
  expect(policy.throughput(800_000, 3, 9_000)).toBe('down');
  expect(policy.proposedTier).toBe('low');
  policy.commit('low', 9_000);
  expect(policy.stall(120_000)).toBe(false);
  expect(policy.prolongedStall(130_000)).toBe(false);
});

it('retries an adaptive downgrade after a failed rendition request', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  policy.stall(1_000);
  expect(policy.stall(8_000)).toBe(true);
  expect(policy.tier).toBe('saver');
  policy.reject('low', 8_000);
  expect(policy.stall(12_000)).toBe(false);
  expect(policy.stall(19_000)).toBe(false);
  expect(policy.stall(27_000)).toBe(true);
  expect(policy.proposedTier).toBe('low');
});
