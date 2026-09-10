import { afterEach, expect, it } from 'vitest';
import { AdaptiveQualityPolicy, loadQualityPreference, qualityRequest, saveQualityPreference } from './quality';

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

it('uses sustained buffered headroom for native HLS without fragment telemetry', () => {
  const policy = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 11_000, 21_000]) expect(policy.buffer(15, true, now)).toBe(false);
  expect(policy.buffer(15, true, 31_000)).toBe(true);
  expect(policy.proposedTier).toBe('balanced');

  const interrupted = new AdaptiveQualityPolicy('saver');
  for (const now of [1_000, 11_000, 21_000]) expect(interrupted.buffer(15, true, now)).toBe(false);
  expect(interrupted.buffer(15, false, 25_000)).toBe(false);
  expect(interrupted.buffer(15, true, 31_000)).toBe(false);

  interrupted.stall(40_000);
  for (const now of [50_000, 60_000, 70_000, 80_000]) expect(interrupted.buffer(15, true, now)).toBe(false);
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
