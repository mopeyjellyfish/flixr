export type QualityPreference = 'auto' | 'data_saver' | 'original';
export type AutoQualityTier = 'balanced' | 'saver' | 'low';
export type QualityRequest = { mode: QualityPreference; max_video_bitrate?: number; max_width?: number; max_height?: number };

const storageKey = 'flixr.playback.quality';
const throughputStorageKey = 'flixr.playback.auto-throughput';
const throughputEvidenceMaxAgeMS = 24 * 60 * 60 * 1_000;

type ThroughputEvidence = { server: string; bitsPerSecond: number; measuredAt: number };

export function initialAutoQualityTier(server: string, now = Date.now()): AutoQualityTier {
  try {
    const evidence = JSON.parse(localStorage.getItem(throughputStorageKey) ?? 'null') as ThroughputEvidence | null;
    if (!evidence || evidence.server !== server || !Number.isFinite(evidence.bitsPerSecond) || !Number.isFinite(evidence.measuredAt)
      || evidence.bitsPerSecond <= 0 || now - evidence.measuredAt > throughputEvidenceMaxAgeMS || evidence.measuredAt > now) return 'balanced';
    if (evidence.bitsPerSecond < 1_600_000) return 'low';
    if (evidence.bitsPerSecond < 3_900_000) return 'saver';
    return 'balanced';
  } catch { return 'balanced'; }
}

export function rememberAutoThroughput(server: string, bitsPerSecond: number, measuredAt = Date.now()): void {
  if (!server || !Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0 || !Number.isFinite(measuredAt)) return;
  try { localStorage.setItem(throughputStorageKey, JSON.stringify({ server, bitsPerSecond, measuredAt } satisfies ThroughputEvidence)); }
  catch { /* device storage is optional */ }
}

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
  private rates: Array<{ now: number; bitsPerSecond: number; sampleID?: string }> = [];
  private recoveryHeadroom?: { since: number; last: number; samples: number };
  private bufferHeadroom?: { since: number; last: number; samples: number };
  private bufferSamples: Array<{ now: number; seconds: number }> = [];
  private pendingTier?: AutoQualityTier;
  private startupAt?: number;

  constructor(initialTier: AutoQualityTier = 'low') { this.tier = initialTier; }

  get proposedTier(): AutoQualityTier | undefined { return this.pendingTier; }
  get rememberableThroughput(): number | undefined {
    const selected = this.rates.slice(-4);
    if (selected.length < 4 || selected[selected.length - 1].now - selected[0].now < 8_000) return undefined;
    return Math.min(...selected.map((sample) => sample.bitsPerSecond));
  }

  stall(now: number): boolean {
    if (this.stalledAt === undefined) this.stalledAt = now;
    this.lastStallAt = now;
    if (this.tier === 'low' || !this.canEmergency(now)) return false;
    this.stalls = [...this.stalls.filter((value) => now - value <= 15_000), now];
    if (this.stalls.length < 2) return false;
    return this.propose(this.lowerTier());
  }

  prolongedStall(now: number): boolean {
    return this.tier !== 'low' && this.stalledAt !== undefined && now - this.stalledAt >= 8_000 && this.canEmergency(now)
      ? this.propose(this.lowerTier())
      : false;
  }

  beginStartup(now: number): void { this.startupAt = now; }

  startup(now: number): boolean {
    if (this.startupAt === undefined || now - this.startupAt < 3_000 || this.tier === 'low' || !this.canEmergency(now)) return false;
    const measured = this.rates.filter((sample) => sample.sampleID !== undefined).map((sample) => sample.bitsPerSecond);
    return this.propose(this.tier === 'balanced' && measured.length > 0 && Math.min(...measured) < 1_600_000 ? 'low' : this.lowerTier());
  }

  playing(): void { this.stalledAt = undefined; this.startupAt = undefined; }

  buffer(bufferSeconds: number, playing: boolean, now: number): boolean {
    if (!playing) this.bufferSamples = [];
    else {
      this.bufferSamples = [...this.bufferSamples.filter((sample) => now - sample.now <= 15_000), { now, seconds: bufferSeconds }];
      const first = this.bufferSamples[0];
      if (this.tier !== 'low' && bufferSeconds <= 6 && this.bufferSamples.length >= 3 && now - first.now >= 4_000
        && first.seconds - bufferSeconds >= 3 && this.canEmergency(now)) return this.propose(this.lowerTier());
    }
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

  throughput(bitsPerSecond: number, bufferSeconds: number, now: number, mediaBitsPerSecond = 0, sampleID?: string): 'down' | 'up' | undefined {
    if (!Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0) return undefined;
    const retained = this.rates.filter((sample) => now - sample.now <= 60_000);
    const existing = sampleID ? retained.findIndex((sample) => sample.sampleID === sampleID) : -1;
    if (existing >= 0) retained[existing] = { ...retained[existing], bitsPerSecond };
    else retained.push({ now, bitsPerSecond, sampleID });
    this.rates = retained.slice(-24);
    const tierRequired = this.tier === 'balanced' ? 2_900_000 : this.tier === 'saver' ? 1_210_000 : 0;
    const required = Math.max(tierRequired, mediaBitsPerSecond * 1.15);
    if (required > 0 && bufferSeconds < 6 && this.canEmergency(now)) {
      const low = this.rates.filter((sample) => sample.bitsPerSecond < required);
      if (low.length >= 5 && now - low[0].now >= 8_000 && this.propose(this.lowerTier())) return 'down';
    }
    if (!this.outsideCooldown(now)) return undefined;
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
  private canEmergency(now: number): boolean { return this.pendingTier === undefined && now >= this.retryAfter; }
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
    this.bufferSamples = [];
  }
}
