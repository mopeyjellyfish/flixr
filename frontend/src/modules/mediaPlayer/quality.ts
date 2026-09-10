export type QualityPreference = 'auto' | 'data_saver' | 'original';
export type AutoQualityTier = 'balanced' | 'saver';
export type QualityRequest = {
  mode: QualityPreference;
  max_video_bitrate?: number;
  max_width?: number;
  max_height?: number;
};

const storageKey = 'flixr.playback.quality';

export function loadQualityPreference(): QualityPreference {
  try {
    const value = localStorage.getItem(storageKey);
    return value === 'auto' || value === 'data_saver' || value === 'original' ? value : 'auto';
  } catch {
    return 'auto';
  }
}

export function saveQualityPreference(preference: QualityPreference): void {
  try { localStorage.setItem(storageKey, preference); } catch { /* device storage is optional */ }
}

export function qualityRequest(preference: QualityPreference, tier: AutoQualityTier): QualityRequest {
  if (preference === 'original') return { mode: 'original' };
  if (preference === 'data_saver' || tier === 'saver') {
    return { mode: preference, max_video_bitrate: 1_000_000, max_width: 854, max_height: 480 };
  }
  return { mode: 'auto', max_video_bitrate: 2_500_000, max_width: 1280, max_height: 720 };
}

// Auto changes rendition only after sustained evidence, with cooldown and
// measured headroom before recovery. A new title starts a fresh policy.
export class AdaptiveQualityPolicy {
  tier: AutoQualityTier = 'balanced';
  private stalls: number[] = [];
  private stalledAt?: number;
  private changedAt = 0;
  private rates: Array<{ now: number; bitsPerSecond: number }> = [];

  stall(now: number): boolean {
    if (this.stalledAt === undefined) this.stalledAt = now;
    if (this.tier === 'saver' || !this.outsideCooldown(now)) return false;
    this.stalls = [...this.stalls.filter((value) => now - value <= 15_000), now];
    if (this.stalls.length < 2) return false;
    return this.change('saver', now);
  }

  prolongedStall(now: number): boolean {
    return this.tier === 'balanced' && this.stalledAt !== undefined && now - this.stalledAt >= 8_000 && this.outsideCooldown(now)
      ? this.change('saver', now)
      : false;
  }

  playing(): void { this.stalledAt = undefined; }

  throughput(bitsPerSecond: number, bufferSeconds: number, now: number): 'down' | 'up' | undefined {
    if (!Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0) return undefined;
    this.rates = [...this.rates.filter((sample) => now - sample.now <= 60_000), { now, bitsPerSecond }];
    if (!this.outsideCooldown(now)) return undefined;
    if (this.tier === 'balanced' && bufferSeconds < 6) {
      const low = this.rates.filter((sample) => sample.bitsPerSecond < 2_900_000);
      if (low.length >= 5 && now - low[0].now >= 8_000 && this.change('saver', now)) return 'down';
    }
    if (this.tier === 'saver' && now - this.changedAt >= 180_000) {
      const high = this.rates.filter((sample) => sample.bitsPerSecond >= 3_900_000);
      if (high.length >= 4 && now - high[0].now >= 60_000 && this.change('balanced', now)) return 'up';
    }
    return undefined;
  }

  private outsideCooldown(now: number): boolean { return this.changedAt === 0 || now - this.changedAt >= 45_000; }

  private change(tier: AutoQualityTier, now: number): boolean {
    if (this.tier === tier) return false;
    this.tier = tier;
    this.changedAt = now;
    this.stalls = [];
    this.stalledAt = undefined;
    this.rates = [];
    return true;
  }
}
