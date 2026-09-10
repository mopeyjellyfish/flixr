export type QualityPreference = 'auto' | 'data_saver' | 'original';
export type AutoQualityTier = 'balanced' | 'saver' | 'low';
export type QualityRequest = { mode: QualityPreference; max_video_bitrate?: number; max_width?: number; max_height?: number };

const storageKey = 'flixr.playback.quality';

export function loadQualityPreference(): QualityPreference {
  try {
    const value = localStorage.getItem(storageKey);
    return value === 'auto' || value === 'data_saver' || value === 'original' ? value : 'auto';
  } catch { return 'auto'; }
}

export function saveQualityPreference(preference: QualityPreference): void {
  try { localStorage.setItem(storageKey, preference); } catch { /* device storage is optional */ }
}

export function qualityRequest(preference: QualityPreference, tier: AutoQualityTier): QualityRequest {
  if (preference === 'original') return { mode: 'original' };
  if (preference === 'data_saver' || tier === 'saver') return { mode: preference, max_video_bitrate: 1_000_000, max_width: 854, max_height: 480 };
  if (tier === 'low') return { mode: 'auto', max_video_bitrate: 500_000, max_width: 640, max_height: 360 };
  return { mode: 'auto', max_video_bitrate: 2_500_000, max_width: 1280, max_height: 720 };
}

// Auto proposes one rendition step at a time. The caller commits only after the
// new source is attached, so a failed request cannot leave policy ahead of playback.
export class AdaptiveQualityPolicy {
  tier: AutoQualityTier;
  private stalls: number[] = [];
  private stalledAt?: number;
  private lastStallAt?: number;
  private cooldownUntil = 0;
  private recoveryAfter = 15_000;
  private recoverySpan = 12_000;
  private retryAfter = 0;
  private rates: Array<{ now: number; bitsPerSecond: number }> = [];
  private recoveryHeadroom?: { since: number; last: number; samples: number };
  private bufferHeadroom?: { since: number; last: number; samples: number };
  private pendingTier?: AutoQualityTier;

  constructor(initialTier: AutoQualityTier = 'low') { this.tier = initialTier; }

  get proposedTier(): AutoQualityTier | undefined { return this.pendingTier; }

  stall(now: number): boolean {
    if (this.stalledAt === undefined) this.stalledAt = now;
    this.lastStallAt = now;
    if (this.tier === 'low' || !this.outsideCooldown(now)) return false;
    this.stalls = [...this.stalls.filter((value) => now - value <= 15_000), now];
    if (this.stalls.length < 2) return false;
    return this.propose(this.lowerTier());
  }

  prolongedStall(now: number): boolean {
    return this.tier !== 'low' && this.stalledAt !== undefined && now - this.stalledAt >= 8_000 && this.outsideCooldown(now)
      ? this.propose(this.lowerTier())
      : false;
  }

  playing(): void { this.stalledAt = undefined; }

  buffer(bufferSeconds: number, playing: boolean, now: number): boolean {
    if (!playing || bufferSeconds < 8 || this.tier === 'balanced' || (this.lastStallAt !== undefined && now - this.lastStallAt < 30_000) || !this.outsideCooldown(now)) {
      this.bufferHeadroom = undefined;
      return false;
    }
    if (!this.bufferHeadroom || now - this.bufferHeadroom.last > 15_000) this.bufferHeadroom = { since: now, last: now, samples: 1 };
    else { this.bufferHeadroom.last = now; this.bufferHeadroom.samples += 1; }
    return now >= this.recoveryAfter && this.bufferHeadroom.samples >= 4 && now - this.bufferHeadroom.since >= 30_000
      ? this.propose(this.higherTier())
      : false;
  }

  resetBufferEvidence(): void { this.bufferHeadroom = undefined; }

  throughput(bitsPerSecond: number, bufferSeconds: number, now: number): 'down' | 'up' | undefined {
    if (!Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0) return undefined;
    this.rates = [...this.rates.filter((sample) => now - sample.now <= 60_000), { now, bitsPerSecond }];
    if (!this.outsideCooldown(now)) return undefined;
    const required = this.tier === 'balanced' ? 2_900_000 : this.tier === 'saver' ? 1_210_000 : 0;
    if (required > 0 && bufferSeconds < 6) {
      const low = this.rates.filter((sample) => sample.bitsPerSecond < required);
      if (low.length >= 5 && now - low[0].now >= 8_000 && this.propose(this.lowerTier())) return 'down';
    }
    if (this.tier !== 'balanced') {
      const recoveryRate = this.tier === 'low' ? 1_600_000 : 3_900_000;
      if (bitsPerSecond < recoveryRate) {
        this.recoveryHeadroom = undefined;
        return undefined;
      }
      if (!this.recoveryHeadroom || now - this.recoveryHeadroom.last > 30_000) this.recoveryHeadroom = { since: now, last: now, samples: 1 };
      else { this.recoveryHeadroom.last = now; this.recoveryHeadroom.samples += 1; }
      if (now >= this.recoveryAfter && this.recoveryHeadroom.samples >= 4 && now - this.recoveryHeadroom.since >= this.recoverySpan && this.propose(this.higherTier())) return 'up';
    }
    return undefined;
  }

  commit(tier: AutoQualityTier, now: number): void {
    if (this.pendingTier !== tier) return;
    const upgrading = this.rank(tier) > this.rank(this.tier);
    this.tier = tier;
    this.cooldownUntil = now + (upgrading ? 15_000 : 45_000);
    this.recoveryAfter = now + (upgrading ? 15_000 : 180_000);
    this.recoverySpan = upgrading ? 12_000 : 60_000;
    this.resetEvidence();
  }

  reject(tier: AutoQualityTier, now: number): void {
    if (this.pendingTier !== tier) return;
    this.retryAfter = now + 10_000;
    this.resetEvidence();
  }

  private outsideCooldown(now: number): boolean {
    return this.pendingTier === undefined && now >= this.retryAfter && now >= this.cooldownUntil;
  }
  private lowerTier(): AutoQualityTier { return this.tier === 'balanced' ? 'saver' : 'low'; }
  private higherTier(): AutoQualityTier { return this.tier === 'low' ? 'saver' : 'balanced'; }
  private rank(tier: AutoQualityTier): number { return tier === 'low' ? 0 : tier === 'saver' ? 1 : 2; }
  private propose(tier: AutoQualityTier): boolean {
    if (this.tier === tier || this.pendingTier !== undefined) return false;
    this.pendingTier = tier;
    return true;
  }
  private resetEvidence(): void {
    this.pendingTier = undefined;
    this.stalls = [];
    this.stalledAt = undefined;
    this.rates = [];
    this.recoveryHeadroom = undefined;
    this.bufferHeadroom = undefined;
  }
}
