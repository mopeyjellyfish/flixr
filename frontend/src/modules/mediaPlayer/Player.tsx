import type Hls from 'hls.js';
import type { AttachMediaSourceData } from 'hls.js';
import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type CatalogItem, type Episode, type PlaybackCapabilities, type PlaybackPlan } from '../../core/api';
import { initialPlayerState, playerReducer } from './state';
import { isRetryablePlaybackFailure, PlaybackNetworkError, PlaybackRecovery } from './recovery';
import { screenCoordinator } from '../screenCoordinator/runtime';
import { browserCapabilities } from './capabilities';
import { PlayerControls, type Chapter } from './PlayerControls';
import { AdaptiveQualityPolicy, initialAutoQualityTier, loadQualityPreference, qualityRequest, rememberAutoThroughput, saveQualityPreference, type AutoQualityTier, type QualityPreference } from './quality';
import { retryableLazy } from './lazy';
import './player.css';

const maxConsecutiveRecoveries = 3;
const finalHeartbeatWaitMS = 2_000;
const qualityRequestTimeoutMS = 15_000;
const qualityReadinessTimeoutMS = 8_000;
const sourceBufferUpdateTimeoutMS = 1_000;

async function waitForSourceBuffer(buffer: SourceBuffer): Promise<boolean> {
  if (!buffer.updating) return true;
  return new Promise((resolve) => {
    let timer = 0;
    const done = (ready: boolean) => {
      window.clearTimeout(timer);
      buffer.removeEventListener('updateend', updated);
      buffer.removeEventListener('error', failed);
      buffer.removeEventListener('abort', failed);
      resolve(ready);
    };
    const updated = () => done(true);
    const failed = () => done(false);
    buffer.addEventListener('updateend', updated, { once: true });
    buffer.addEventListener('error', failed, { once: true });
    buffer.addEventListener('abort', failed, { once: true });
    timer = window.setTimeout(() => done(false), sourceBufferUpdateTimeoutMS);
  });
}

async function clearTransferredBuffers(transfer: AttachMediaSourceData): Promise<boolean> {
  const buffers = new Set(Object.values(transfer.tracks).flatMap((track) => track?.buffer ? [track.buffer] : []));
  return (await Promise.all(Array.from(buffers, async (buffer) => {
    if (!await waitForSourceBuffer(buffer)) return false;
    for (let attempt = 0; buffer.buffered.length && attempt < 8; attempt += 1) {
      try { buffer.remove(buffer.buffered.start(0), buffer.buffered.end(buffer.buffered.length - 1)); }
      catch { return false; }
      if (!await waitForSourceBuffer(buffer)) return false;
    }
    return buffer.buffered.length === 0;
  }))).every(Boolean);
}

async function releaseTransferredMedia(transfer: AttachMediaSourceData, objectURL: string): Promise<void> {
  const source = transfer.mediaSource;
  if (source?.readyState === 'open') {
    const buffers = Array.from(source.sourceBuffers);
    await Promise.all(buffers.map((buffer) => waitForSourceBuffer(buffer)));
    for (const buffer of buffers) {
      try { source.removeSourceBuffer(buffer); }
      catch { /* the direct source is already active; release is best-effort */ }
    }
    try { if (source.readyState === 'open') source.endOfStream(); }
    catch { /* a closing MediaSource no longer needs explicit completion */ }
  }
  try { if (objectURL.startsWith('blob:')) URL.revokeObjectURL(objectURL); }
  catch { /* the browser may already have released the replaced object URL */ }
}

function supportsHlsMSE(): boolean {
  const managed = (globalThis as typeof globalThis & { ManagedMediaSource?: typeof MediaSource }).ManagedMediaSource;
  const source = typeof MediaSource !== 'undefined' ? MediaSource : managed;
  return Boolean(source?.isTypeSupported('video/mp4; codecs="avc1.640028, mp4a.40.2"'));
}

async function settleWithin(promise: Promise<unknown>, timeoutMS: number): Promise<void> {
  let timer = 0;
  await Promise.race([
    promise.catch(() => undefined),
    new Promise<void>((resolve) => { timer = window.setTimeout(resolve, timeoutMS); }),
  ]);
  window.clearTimeout(timer);
}

async function commitQualityHandoff(url: string): Promise<'committed' | 'aborted' | 'unknown'> {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try { await api.playbackHandoff(url, true); return 'committed'; }
    catch { /* retry the idempotent outcome before resolving the ambiguity */ }
  }
  try { await api.playbackHandoff(url, false); return 'aborted'; }
  catch (error: unknown) { return error instanceof ApiError && error.status === 409 ? 'committed' : 'unknown'; }
}

const languageNames = new Intl.DisplayNames(['en'], { type: 'language' });

function languageLabel(language?: string): string {
  if (!language) return 'Unknown language';
  try {
    return languageNames.of(language) ?? language;
  } catch {
    return language;
  }
}

function audioLabel(track: NonNullable<PlaybackPlan['audio_tracks']>[number]): string {
  const language = languageLabel(track.language);
  const parts = [track.title || language];
  if (track.title && track.title.toLocaleLowerCase() !== language.toLocaleLowerCase()) parts.push(language);
  if (track.default) parts.push('Default');
  if (track.external) parts.push('External');
  return parts.join(' · ');
}

function subtitleLabel(track: NonNullable<PlaybackPlan['subtitle_tracks']>[number]): string {
  const language = languageLabel(track.language);
  const parts = [track.title || language];
  if (track.title && track.title.toLocaleLowerCase() !== language.toLocaleLowerCase()) parts.push(language);
  if (track.default) parts.push('Default');
  if (track.forced) parts.push('Forced');
  if (track.sdh) parts.push('SDH');
  if (track.external) parts.push('External');
  return parts.join(' · ');
}

type AutoplayState =
  | { kind: 'idle' }
  | { kind: 'resolving' }
  | { kind: 'countdown'; episode: Episode; seconds: number }
  | { kind: 'paused'; episode?: Episode }
  | { kind: 'end'; contextUnavailable: boolean }
  | { kind: 'advancing'; episode: Episode };

const autoplaySeconds = 10;

export function Player({ catalogID, startPositionMS, active = true, continueWatchingIntent = 'user', onAdvance, onExit }: { catalogID: string; startPositionMS?: number; active?: boolean; continueWatchingIntent?: 'user' | 'automatic'; onAdvance?: (catalogID: string, intent: 'user' | 'automatic') => void; onExit: () => void }) {
  const [state, dispatch] = useReducer(playerReducer, initialPlayerState);
  const stage = useRef<HTMLElement>(null);
  const [item, setItem] = useState<CatalogItem>();
  const [durationMS, setDurationMS] = useState(0);
  const [episodes, setEpisodes] = useState<Episode[]>([]);
  const [seriesTitle, setSeriesTitle] = useState<string>();
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [controlsVisible, setControlsVisible] = useState(true);
  const [keyboardControls, setKeyboardControls] = useState(true);
  const [seeking, setSeeking] = useState(false);
  const [pendingSeekMS, setPendingSeekMS] = useState<number>();
  const [seekPlaying, setSeekPlaying] = useState(true);
  const [playbackRate, setPlaybackRate] = useState(1);
  const [qualityPreference, setQualityPreference] = useState<QualityPreference>(loadQualityPreference);
  const [originalFallback, setOriginalFallback] = useState(false);
  const [planAttempt, setPlanAttempt] = useState(0);
  const seekQueue = useRef<{ running: boolean; target?: number }>({ running: false });
  const [trackError, setTrackError] = useState<string>();
  const [switchingAudio, setSwitchingAudio] = useState(false);
  const [switchingSubtitle, setSwitchingSubtitle] = useState(false);
  const [audioLocked, setAudioLocked] = useState(false);
  const [autoplay, setAutoplay] = useState<AutoplayState>({ kind: 'idle' });
  const video = useRef<HTMLVideoElement>(null);
  const backButton = useRef<HTMLButtonElement>(null);
  const hls = useRef<Hls | null>(null);
  const loadHls = useRef(retryableLazy(() => import('hls.js'))).current;
  const sourceVersion = useRef(0);
  const activatedSourceVersion = useRef(0);
  const audioSwitchVersion = useRef(0);
  const subtitleSwitchVersion = useRef(0);
  const audioSwitchTask = useRef<Promise<void> | null>(null);
  const replacingSession = useRef<string | undefined>(undefined);
  const retainedPlayback = useRef<PlaybackPlan | null>(null);
  const playback = useRef<PlaybackPlan | null>(null);
  const observation = useRef(0);
  const initializingPosition = useRef(false);
  const finalizing = useRef(false);
  const recovering = useRef(false);
  const consecutiveRecoveries = useRef(0);
  const recovery = useRef(new PlaybackRecovery());
  const recoverRef = useRef<(error: unknown, source?: number) => void>(() => undefined);
  const endedPlayback = useRef(false);
  const completionAck = useRef<Promise<boolean> | null>(null);
  const autoplayVersion = useRef(0);
  const autoplayRequest = useRef<AbortController | null>(null);
  const autoStart = useRef(true);
  const initialPlayPending = useRef(true);
  const playIntent = useRef(true);
  const seekingRef = useRef(false);
  const seekPlayingIntent = useRef(true);
  const keyboardInteraction = useRef(true);
  const playbackRateIntent = useRef(1);
  const qualityPreferenceRef = useRef(qualityPreference);
  const qualityPolicy = useRef(new AdaptiveQualityPolicy(initialAutoQualityTier(window.location.origin)));
  const qualitySwitching = useRef(false);
  const pendingQuality = useRef<{ preference: QualityPreference; tier: AutoQualityTier } | undefined>(undefined);
  const sourceOperations = useRef<Promise<void>>(Promise.resolve());
  const sourceOperationActive = useRef(false);
  const sourceRequestController = useRef<AbortController | null>(null);
  const qualityRateRef = useRef<(bitsPerSecond: number, sampleID?: string) => void>(() => undefined);
  const qualityStartupRef = useRef<() => void>(() => undefined);
  const qualityStartupTimer = useRef(0);
  const attachedPlayback = useRef<PlaybackPlan | null>(null);
  const deferredActivationVersion = useRef<number | undefined>(undefined);
  const seekSourceTransitioning = useRef(false);
  const retainedSeekFallback = useRef<{ plan: PlaybackPlan; target: number } | undefined>(undefined);
  const playerStatus = useRef(state.status);
  playerStatus.current = state.status;

  const enqueueSourceOperation = useCallback(<T,>(operation: () => Promise<T>): Promise<T> => {
    const run = async () => {
      sourceOperationActive.current = true;
      try { return await operation(); } finally { sourceOperationActive.current = false; }
    };
    const task = sourceOperations.current.then(run, run);
    sourceOperations.current = task.then(() => undefined, () => undefined);
    return task;
  }, []);

  useEffect(() => {
    if (active) stage.current?.focus();
  }, [active]);

  useEffect(() => {
    const root = stage.current;
    let timer = 0;
    const reveal = () => {
      setControlsVisible(true);
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        if (playerStatus.current !== 'playing' || seekingRef.current) return;
        const focused = document.activeElement;
        const keyboardFocus = keyboardInteraction.current && focused && focused !== root && root?.contains(focused);
        if (!root?.querySelector('details[open]') && !keyboardFocus) setControlsVisible(false);
      }, 3000);
    };
    const pointer = () => { keyboardInteraction.current = false; setKeyboardControls(false); reveal(); };
    const keyboard = () => { keyboardInteraction.current = true; setKeyboardControls(true); reveal(); };
    reveal();
    root?.addEventListener('pointerdown', pointer, true);
    root?.addEventListener('pointermove', reveal, true);
    root?.addEventListener('keydown', keyboard, true);
    for (const event of ['focusin', 'focusout', 'toggle']) root?.addEventListener(event, reveal, true);
    return () => {
      window.clearTimeout(timer);
      root?.removeEventListener('pointerdown', pointer, true);
      root?.removeEventListener('pointermove', reveal, true);
      root?.removeEventListener('keydown', keyboard, true);
      for (const event of ['focusin', 'focusout', 'toggle']) root?.removeEventListener(event, reveal, true);
    };
  }, []);

  useEffect(() => {
    const tracks = video.current?.textTracks;
    if (!tracks?.addEventListener) return;
    const adjusted = new Map<VTTCue, number | 'auto'>();
    const position = () => {
      for (const track of Array.from(tracks)) {
        for (const cue of Array.from(track.activeCues ?? [])) {
          if (typeof VTTCue === 'undefined' || !(cue instanceof VTTCue)) continue;
          if (controlsVisible && cue.line === 'auto') { adjusted.set(cue, cue.line); cue.line = -6; }
        }
      }
    };
    const listen = () => { for (const track of Array.from(tracks)) track.addEventListener('cuechange', position); position(); };
    tracks.addEventListener('addtrack', listen);
    listen();
    return () => {
      tracks.removeEventListener('addtrack', listen);
      for (const track of Array.from(tracks)) track.removeEventListener('cuechange', position);
      for (const [cue, line] of adjusted) cue.line = line;
    };
  }, [controlsVisible]);

  const sessionID = playback.current?.session_id;
  useEffect(() => {
    if (!sessionID || state.status !== 'playing') return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void fetch(`/api/v1/playback/sessions/${encodeURIComponent(sessionID)}/chapters`, { signal: controller.signal, credentials: 'same-origin' })
        .then(async (response) => {
          if (!response.ok) return;
          const data = await response.json() as { chapters?: Chapter[] };
          if (!controller.signal.aborted && Array.isArray(data.chapters)) setChapters(data.chapters);
        }).catch(() => undefined);
    }, 300);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [sessionID, state.status]);

  useEffect(() => {
    if (!item?.series_id || state.status !== 'playing') return;
    let active = true;
    const timer = window.setTimeout(() => {
      void api.series(item.series_id!).then((series) => {
        if (!active) return;
        setSeriesTitle(series.title);
        setEpisodes(series.seasons.flatMap((season) => season.episodes).filter((episode) => episode.playable));
      }).catch(() => undefined);
    }, 500);
    return () => { active = false; window.clearTimeout(timer); };
  }, [item?.series_id, state.status]);

  const currentPosition = useCallback(() => {
    const relative = Math.round((video.current?.currentTime ?? 0) * 1000);
    return Math.max(0, (playback.current?.stream_offset_ms ?? 0) + relative);
  }, []);

  const positionAttachedSource = useCallback((version: number) => {
    const element = video.current;
    const plan = playback.current;
    if (!element || !plan || version !== sourceVersion.current) return false;
    element.playbackRate = playbackRateIntent.current;
    const resumeSeconds = Math.max(0, plan.resume_ms - plan.stream_offset_ms) / 1000;
    if (Math.abs(element.currentTime - resumeSeconds) > 0.001) {
      initializingPosition.current = true;
      element.currentTime = resumeSeconds;
    }
    return true;
  }, []);

  const activateAttachedSource = useCallback((version: number) => {
    if (activatedSourceVersion.current === version || !positionAttachedSource(version)) return;
    const element = video.current!;
    activatedSourceVersion.current = version;
    if (autoStart.current) {
      autoStart.current = false;
      // Browser autoplay policy may require the receiver's local Play button.
      void element.play().catch((error: unknown) => {
        playIntent.current = false;
        if (error instanceof DOMException && error.name === 'NotAllowedError') {
          initialPlayPending.current = false;
          setTrackError('Press Play to start watching.');
        }
        dispatch({ type: 'pause' });
      });
    } else {
      // A transferred MediaSource can retain metadata without emitting canplay
      // again. The replacement is attached and intentionally paused, so expose
      // Play immediately; a later waiting event will report actual buffering.
      dispatch({ type: 'pause' });
    }
    seekSourceTransitioning.current = false;
  }, [positionAttachedSource]);

  const attach = useCallback(async (plan: PlaybackPlan, activate = true) => {
    const element = video.current;
    if (!element) return undefined;
    const version = ++sourceVersion.current;
    deferredActivationVersion.current = activate ? undefined : version;
    qualityPolicy.current.resetBufferEvidence();
    subtitleSwitchVersion.current += 1;
    setSwitchingSubtitle(false);
    playback.current = plan;
    attachedPlayback.current = null;
    window.clearTimeout(qualityStartupTimer.current);
    if (qualityPreferenceRef.current === 'auto' && plan.plan.kind !== 'direct') {
      qualityPolicy.current.beginStartup(Date.now());
      qualityStartupTimer.current = window.setTimeout(() => qualityStartupRef.current(), 6_000);
    }
    dispatch({ type: 'load', source: plan.media_url, resumeMs: plan.resume_ms });
    element.playbackRate = playbackRateIntent.current;
    // Prefer the bundled engine for live/sliding HLS; native MIME support alone
    // does not establish reliable live playback (notably in Chromium). Avoid an
    // explicit empty source/load cycle here: mobile browsers end native video
    // fullscreen when the active media element is reset.
    const mse = supportsHlsMSE();
    if (plan.plan.kind === 'direct' || (!mse && element.canPlayType('application/vnd.apple.mpegurl'))) {
      const objectURL = element.currentSrc || element.src;
      const transferredMedia = hls.current?.transferMedia();
      hls.current?.destroy();
      hls.current = null;
      element.src = plan.media_url;
      attachedPlayback.current = plan;
      if (transferredMedia) await releaseTransferredMedia(transferredMedia, objectURL);
      return version;
    }
    let Hls: typeof import('hls.js').default;
    try { ({ default: Hls } = await loadHls()); }
    catch {
      if (version !== sourceVersion.current || finalizing.current || endedPlayback.current || !video.current) return;
      if (element.canPlayType('application/vnd.apple.mpegurl')) {
        hls.current?.destroy();
        hls.current = null;
        element.src = plan.media_url;
        attachedPlayback.current = plan;
        return version;
      }
      dispatch({ type: 'error', message: 'The compatibility player could not load. Try again.' });
      return undefined;
    }
    if (version !== sourceVersion.current || finalizing.current || endedPlayback.current || !video.current) return;
    if (!Hls.isSupported()) {
      if (element.canPlayType('application/vnd.apple.mpegurl')) {
        hls.current?.destroy();
        hls.current = null;
        element.src = plan.media_url;
        attachedPlayback.current = plan;
        return version;
      }
      dispatch({ type: 'error', message: 'This browser cannot play the compatibility stream.' });
      return undefined;
    }
    const startPosition = Math.max(0, (plan.resume_ms - plan.stream_offset_ms) / 1000);
    // Hls.destroy() normally detaches and empties its media element. Transfer the
    // existing MediaSource first so the same video presentation remains active.
    const transferredMedia = hls.current?.transferMedia();
    hls.current?.destroy();
    hls.current = null;
    const clearedTransfer = transferredMedia && await clearTransferredBuffers(transferredMedia) ? transferredMedia : undefined;
    if (version !== sourceVersion.current || finalizing.current || endedPlayback.current || !video.current) return;
    const next = new Hls({
      enableWorker: true,
      lowLatencyMode: false,
      startPosition,
      maxBufferLength: 20,
      maxMaxBufferLength: 30,
      maxBufferSize: 32 * 1024 * 1024,
      backBufferLength: 30,
      xhrSetup: (xhr, _url, context) => {
        if (context.type !== 'media-fragment') return;
        const started = performance.now();
        xhr.addEventListener('progress', (event) => {
          const elapsed = performance.now() - started;
          if (event.loaded >= 64 * 1024 && elapsed >= 100) qualityRateRef.current(event.loaded * 8 * 1000 / elapsed, `${context.url}:${context.rangeStart ?? 0}:${context.rangeEnd ?? ''}`);
        });
      },
    });
    next.on(Hls.Events.ERROR, (_event, data) => {
      if (version !== sourceVersion.current || finalizing.current) return;
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
        recoverRef.current(new PlaybackNetworkError(), version);
      } else if (data.fatal) {
        dispatch({ type: 'error', message: 'The compatibility stream stopped unexpectedly.' });
      }
    });
    next.on(Hls.Events.FRAG_LOADED, (_event, data) => {
      const stats = data.part?.stats ?? data.frag.stats;
      const elapsed = stats.loading.end - stats.loading.start;
      if (data.payload.byteLength >= 64 * 1024 && elapsed >= 100) qualityRateRef.current(data.payload.byteLength * 8 * 1000 / elapsed, `${data.frag.url}:${data.frag.byteRangeStartOffset ?? 0}:${data.frag.byteRangeEndOffset ?? ''}`);
    });
    next.loadSource(plan.media_url);
    next.attachMedia(clearedTransfer ?? video.current);
    hls.current = next;
    attachedPlayback.current = plan;
    // Transferring keeps the element's loaded metadata, so browsers are not
    // required to emit loadedmetadata again for the replacement source.
    if (clearedTransfer) {
      positionAttachedSource(version);
      if (activate) activateAttachedSource(version);
    }
    return version;
  }, [activateAttachedSource, loadHls, positionAttachedSource]);

  const waitForUsableSource = useCallback((version: number, timeoutMS = qualityReadinessTimeoutMS): Promise<boolean> => {
    const element = video.current;
    if (!element || version !== sourceVersion.current) return Promise.resolve(false);
    const ready = () => {
      if (version !== sourceVersion.current || video.current !== element) return false;
      let buffered = false;
      for (let index = 0; index < element.buffered.length; index += 1) {
        if (element.buffered.start(index) <= element.currentTime && element.buffered.end(index) > element.currentTime) { buffered = true; break; }
      }
      return hls.current ? buffered : element.readyState >= element.HAVE_FUTURE_DATA;
    };
    if (ready()) return Promise.resolve(true);
    return new Promise((resolve) => {
      let timer = 0;
      let fenceTimer = 0;
      const done = (usable: boolean) => {
        window.clearTimeout(timer);
        window.clearInterval(fenceTimer);
        for (const event of ['loadedmetadata', 'loadeddata', 'canplay', 'progress', 'error', 'abort']) element.removeEventListener(event, observed);
        resolve(usable);
      };
      const observed = (event: Event) => {
        if (version !== sourceVersion.current || finalizing.current || endedPlayback.current || video.current !== element) done(false);
        else if (event.type === 'error' || event.type === 'abort') done(false);
        else if (ready() || event.type === 'canplay' || (!hls.current && event.type === 'loadeddata')) done(true);
      };
      for (const event of ['loadedmetadata', 'loadeddata', 'canplay', 'progress', 'error', 'abort']) element.addEventListener(event, observed);
      fenceTimer = window.setInterval(() => { if (version !== sourceVersion.current || finalizing.current || endedPlayback.current) done(false); }, 100);
      timer = window.setTimeout(() => done(false), timeoutMS);
    });
  }, []);

  const replaceQuality = useCallback(async (preference: QualityPreference, tier: AutoQualityTier) => {
    pendingQuality.current = { preference, tier };
    if (qualitySwitching.current || finalizing.current || endedPlayback.current) return;
    qualitySwitching.current = true;
    try {
      await enqueueSourceOperation(async () => {
        while (pendingQuality.current && !finalizing.current && !endedPlayback.current) {
          const intent = pendingQuality.current;
          pendingQuality.current = undefined;
          const adaptiveProposal = intent.preference === 'auto' && qualityPolicy.current.proposedTier === intent.tier;
          const current = playback.current;
          const element = video.current;
          if (!current || !element) return;
          const version = sourceVersion.current;
          const position = currentPosition();
          autoStart.current = playIntent.current;
          replacingSession.current = current.session_id;
          retainedPlayback.current = current;
          let requestTimeout = 0;
          try {
            const detail = await api.item(catalogID);
            const capabilities = await browserCapabilities(detail);
            if (version !== sourceVersion.current || finalizing.current || endedPlayback.current) {
              if (adaptiveProposal) qualityPolicy.current.reject(intent.tier, Date.now());
              return;
            }
            const controller = new AbortController();
            sourceRequestController.current = controller;
            requestTimeout = window.setTimeout(() => controller.abort(), qualityRequestTimeoutMS);
            const updated = await api.playbackQuality(current.session_id, position, ++observation.current, capabilities, qualityRequest(intent.preference, intent.tier), controller.signal, true);
            window.clearTimeout(requestTimeout);
            if (sourceRequestController.current === controller) sourceRequestController.current = null;
            if (version !== sourceVersion.current || finalizing.current || endedPlayback.current) {
              if (updated.handoff_url) void api.playbackHandoff(updated.handoff_url, false).catch(() => undefined);
              else void api.playbackStop(updated.session_id).catch(() => undefined);
              if (adaptiveProposal) qualityPolicy.current.reject(intent.tier, Date.now());
              return;
            }
            const resumeMS = Math.max(updated.resume_ms, currentPosition());
            if (seekingRef.current) autoStart.current = false;
            const attachedVersion = await attach({ ...updated, resume_ms: resumeMS }, !updated.handoff_url);
            const usable = attachedVersion !== undefined && (!updated.handoff_url || await waitForUsableSource(attachedVersion));
            let candidateWon = usable;
            let handoffOutcome: 'committed' | 'aborted' | 'unknown' = usable ? 'committed' : 'aborted';
            if (updated.handoff_url) {
              if (usable) {
                handoffOutcome = await commitQualityHandoff(updated.handoff_url);
                candidateWon = handoffOutcome === 'committed';
              } else {
                try { await api.playbackHandoff(updated.handoff_url, false); handoffOutcome = 'aborted'; }
                catch { handoffOutcome = 'unknown'; }
                candidateWon = false;
              }
              if (candidateWon && attachedVersion !== undefined) {
                deferredActivationVersion.current = undefined;
                activateAttachedSource(attachedVersion);
              } else if (!finalizing.current && !endedPlayback.current && (version === sourceVersion.current || attachedVersion === sourceVersion.current)) {
                autoStart.current = playIntent.current;
                const restoredVersion = await attach({ ...current, resume_ms: Math.max(current.resume_ms, resumeMS) }, false);
                const restored = restoredVersion !== undefined && await waitForUsableSource(restoredVersion);
                if (restored && restoredVersion !== undefined) {
                  deferredActivationVersion.current = undefined;
                  activateAttachedSource(restoredVersion);
                } else if (handoffOutcome === 'unknown' && usable) {
                  const candidateVersion = await attach({ ...updated, resume_ms: resumeMS }, false);
                  if (candidateVersion !== undefined && await waitForUsableSource(candidateVersion)) {
                    deferredActivationVersion.current = undefined;
                    activateAttachedSource(candidateVersion);
                    candidateWon = true;
                  }
                }
              } else {
                deferredActivationVersion.current = undefined;
                playback.current = current;
              }
            }
            if (adaptiveProposal) {
              if (candidateWon && attachedPlayback.current?.session_id === updated.session_id && attachedPlayback.current.media_url === updated.media_url) qualityPolicy.current.commit(intent.tier, Date.now());
              else qualityPolicy.current.reject(intent.tier, Date.now());
            }
          } catch (error: unknown) {
            window.clearTimeout(requestTimeout);
            sourceRequestController.current = null;
            if (adaptiveProposal) qualityPolicy.current.reject(intent.tier, Date.now());
            if (version === sourceVersion.current && !finalizing.current && !endedPlayback.current) setTrackError(error instanceof ApiError ? error.message : 'Flixr could not change streaming quality.');
          } finally {
            if (replacingSession.current === current.session_id) replacingSession.current = undefined;
            if (retainedPlayback.current?.session_id === current.session_id) retainedPlayback.current = null;
          }
        }
      });
    } catch (error: unknown) {
      if (!finalizing.current) setTrackError(error instanceof ApiError ? error.message : 'Flixr could not change streaming quality.');
    } finally {
      qualitySwitching.current = false;
    }
  }, [activateAttachedSource, attach, catalogID, currentPosition, enqueueSourceOperation, waitForUsableSource]);

  qualityStartupRef.current = () => {
    if (qualityPreferenceRef.current !== 'auto' || !qualityPolicy.current.startup(Date.now())) return;
    const tier = qualityPolicy.current.proposedTier;
    if (tier) void replaceQuality('auto', tier);
  };

  qualityRateRef.current = (bitsPerSecond, sampleID) => {
    if (qualityPreferenceRef.current !== 'auto' || !video.current) return;
    const element = video.current;
    const bufferSeconds = element.buffered.length ? Math.max(0, element.buffered.end(element.buffered.length - 1) - element.currentTime) : 0;
    const plan = playback.current?.plan;
    const mediaBitsPerSecond = plan?.bandwidth || (plan?.video_bitrate ?? 0) + (plan?.audio_bitrate ?? 0);
    const now = Date.now();
    const change = qualityPolicy.current.throughput(bitsPerSecond, bufferSeconds, now, mediaBitsPerSecond, sampleID);
    const remembered = qualityPolicy.current.rememberableThroughput;
    if (remembered !== undefined) rememberAutoThroughput(window.location.origin, remembered, now);
    const tier = qualityPolicy.current.proposedTier;
    if ((change === 'down' || change === 'up') && tier) void replaceQuality('auto', tier);
  };

  useEffect(() => {
    const element = video.current;
    if (!element) return;
    let prolongedTimer = 0;
    const nativeHeadroom = () => {
      const current = playback.current;
      if (qualityPreferenceRef.current !== 'auto' || !current || current.plan.kind === 'direct' || sourceOperationActive.current) return;
      const buffered = element.buffered.length ? Math.max(0, element.buffered.end(element.buffered.length - 1) - element.currentTime) : 0;
      if (qualityPolicy.current.buffer(buffered, !element.paused, Date.now())) {
        const tier = qualityPolicy.current.proposedTier;
        if (tier) void replaceQuality('auto', tier);
      }
    };
    const stalled = () => {
      if (!playback.current || finalizing.current || element.paused) return;
      dispatch({ type: 'buffering' });
      if (qualityPreferenceRef.current === 'auto' && qualityPolicy.current.stall(Date.now())) {
        const tier = qualityPolicy.current.proposedTier;
        if (tier) void replaceQuality('auto', tier);
        return;
      }
      window.clearTimeout(prolongedTimer);
      prolongedTimer = window.setTimeout(() => {
        if (qualityPreferenceRef.current === 'auto' && qualityPolicy.current.prolongedStall(Date.now())) {
          const tier = qualityPolicy.current.proposedTier;
          if (tier) void replaceQuality('auto', tier);
        }
      }, 8_000);
    };
    const playing = () => { window.clearTimeout(prolongedTimer); window.clearTimeout(qualityStartupTimer.current); qualityPolicy.current.playing(); };
    element.addEventListener('waiting', stalled);
    element.addEventListener('stalled', stalled);
    element.addEventListener('playing', playing);
    element.addEventListener('progress', nativeHeadroom);
    element.addEventListener('timeupdate', nativeHeadroom);
    const nativeTimer = window.setInterval(nativeHeadroom, 5_000);
    return () => {
      window.clearTimeout(prolongedTimer);
      window.clearInterval(nativeTimer);
      element.removeEventListener('waiting', stalled);
      element.removeEventListener('stalled', stalled);
      element.removeEventListener('playing', playing);
      element.removeEventListener('progress', nativeHeadroom);
      element.removeEventListener('timeupdate', nativeHeadroom);
    };
  }, [replaceQuality]);

  const changeQuality = useCallback((preference: QualityPreference) => {
    qualityPreferenceRef.current = preference;
    setQualityPreference(preference);
    saveQualityPreference(preference);
    qualityPolicy.current = new AdaptiveQualityPolicy(initialAutoQualityTier(window.location.origin));
    void replaceQuality(preference, qualityPolicy.current.tier);
  }, [replaceQuality]);

  const recover = useCallback(async (failure: unknown) => {
    if (!isRetryablePlaybackFailure(failure)) {
      playback.current = null;
      dispatch({ type: 'error', message: failure instanceof ApiError ? failure.message : 'Playback stopped unexpectedly. Start the title again.' });
      return;
    }
    if (finalizing.current || recovering.current) return;
    if (consecutiveRecoveries.current >= maxConsecutiveRecoveries) {
      const abandoned = playback.current;
      playback.current = null;
      sourceVersion.current += 1;
      hls.current?.destroy();
      hls.current = null;
      video.current?.removeAttribute('src');
      video.current?.load();
      if (abandoned) void api.playbackStop(abandoned.session_id).catch(() => undefined);
      dispatch({ type: 'error', message: 'Playback could not reconnect to Flixr after several attempts. Check this device\'s local network connection, then start the title again.' });
      return;
    }
    consecutiveRecoveries.current += 1;
    recovering.current = true;
    const oldPlan = playback.current;
    const wasSeeking = seekingRef.current;
    const continuePlaying = initialPlayPending.current || (video.current ? !video.current.paused : false);
    video.current?.pause();
    hls.current?.stopLoad();
    playback.current = null;
    sourceVersion.current += 1;
    attachedPlayback.current = null;
    dispatch({ type: 'buffering' });
    try {
      const next = await recovery.current.run(async () => {
        if (oldPlan) await api.playbackStop(oldPlan.session_id).catch(() => undefined);
        return api.playbackPlan(catalogID, await browserCapabilities(await api.item(catalogID)), 'recovery', qualityRequest(qualityPreferenceRef.current, qualityPolicy.current.tier));
      }, (abandoned) => { void api.playbackStop(abandoned.session_id).catch(() => undefined); });
      if (!next || finalizing.current) return;
      observation.current = 0;
      autoStart.current = wasSeeking ? seekPlayingIntent.current : continuePlaying;
      if (wasSeeking && seekQueue.current.target !== undefined) {
        playback.current = next;
        return;
      }
      await attach(next);
    } catch (error: unknown) {
      if (!finalizing.current) {
        const message = isRetryablePlaybackFailure(error)
          ? 'Playback could not reconnect to Flixr after several attempts. Check this device\'s local network connection, then start the title again.'
          : error instanceof ApiError ? error.message : 'Playback could not restart.';
        dispatch({ type: 'error', message });
      }
    } finally {
      recovering.current = false;
    }
  }, [attach, catalogID]);
  recoverRef.current = (error, expectedSource) => {
    if (sourceOperationActive.current) {
      void enqueueSourceOperation(async () => {
        if ((expectedSource === undefined || expectedSource === sourceVersion.current) && !finalizing.current) await recover(error);
      });
      return;
    }
    void recover(error);
  };

  const heartbeat = useCallback(async (ended = false) => {
    const plan = retainedPlayback.current ?? playback.current;
    // Stop revokes the session before the media element is unmounted.
    if (!plan || recovering.current || (sourceOperationActive.current && !ended) || ((seekingRef.current || seekSourceTransitioning.current) && !ended) || (finalizing.current && !ended) || (endedPlayback.current && !ended)) return false;
    const version = sourceVersion.current;
    const positionMs = currentPosition();
    try {
      const acknowledgement = await api.playbackHeartbeat(plan.session_id, positionMs, ++observation.current, ended);
      if (version !== sourceVersion.current || finalizing.current || playback.current?.session_id !== plan.session_id) return false;
      if (ended && !acknowledgement.accepted) {
        dispatch({ type: 'error', message: 'Episode completion could not be saved. Return to your library and try again.' });
        return false;
      }
      consecutiveRecoveries.current = 0;
      return true;
    } catch (error: unknown) {
      if (version !== sourceVersion.current || sourceOperationActive.current || (finalizing.current && !ended) || playback.current?.session_id !== plan.session_id || replacingSession.current === plan.session_id) return false;
      if (ended || !isRetryablePlaybackFailure(error)) {
        playback.current = null;
        dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'The local playback connection was interrupted.' });
      } else {
        void recover(error instanceof ApiError ? error : new PlaybackNetworkError());
      }
      return false;
    }
  }, [currentPosition, recover]);

  useEffect(() => {
    let active = true;
    let timer = 0;
    const recoveryController = recovery.current;
    autoplayVersion.current += 1;
    autoplayRequest.current?.abort();
    autoplayRequest.current = null;
    finalizing.current = false;
    endedPlayback.current = false;
    completionAck.current = null;
    setAudioLocked(false);
    setOriginalFallback(false);
    setAutoplay({ kind: 'idle' });
    recoveryController.run(
      async () => {
        const detail = await api.item(catalogID);
        if (!active) throw new Error('player unmounted');
        setItem(detail);
        // Keep HLS out of the main bundle while overlapping its lazy download
        // with capability assessment and rendition preparation on MSE browsers.
        if (supportsHlsMSE()) void loadHls();
        const capabilities: PlaybackCapabilities = await browserCapabilities(detail);
        if (!active) throw new Error('player unmounted');
        return api.playbackPlan(catalogID, capabilities, continueWatchingIntent, qualityRequest(qualityPreferenceRef.current, qualityPolicy.current.tier));
      },
      (abandoned) => { void api.playbackStop(abandoned.session_id).catch(() => undefined); },
    ).then(async (initial) => {
      if (!initial) return;
      if (!active) { void api.playbackStop(initial.session_id); return; }
      let plan = initial;
      playback.current = initial;
      observation.current = 0;
      if (startPositionMS !== undefined) {
        plan = initial.plan.kind === 'direct'
          ? { ...initial, resume_ms: startPositionMS }
          : await api.playbackSeek(initial.session_id, startPositionMS, ++observation.current);
      }
      if (!active || finalizing.current) {
        if (plan.session_id !== initial.session_id) void api.playbackStop(plan.session_id).catch(() => undefined);
        return;
      }
      void attach(plan);
      const leaseRemaining = Math.max(3_000, plan.expires_at * 1_000 - Date.now());
      const heartbeatEvery = Math.max(1_000, Math.min(15_000, Math.floor(leaseRemaining / 3)));
      timer = window.setInterval(() => { void heartbeat(); }, heartbeatEvery);
    }).catch((error: unknown) => {
      if (active) {
        setOriginalFallback(error instanceof ApiError && (error.code === 'playback_unsupported' || error.code === 'ffmpeg_unavailable') && qualityPreferenceRef.current !== 'original');
        const message = isRetryablePlaybackFailure(error)
          ? 'Playback could not connect to Flixr after several attempts. Check this device\'s local network connection and try again.'
          : error instanceof ApiError ? error.message : 'Playback could not start.';
        dispatch({ type: 'error', message });
      }
    });
    const pageHide = () => {
      const plan = retainedPlayback.current ?? playback.current;
      if (!plan || finalizing.current || endedPlayback.current) return;
      const body = new Blob([JSON.stringify({ position_ms: currentPosition(), observation: ++observation.current })], { type: 'application/json' });
      navigator.sendBeacon(plan.heartbeat_url, body);
    };
    window.addEventListener('pagehide', pageHide);
    return () => {
      active = false;
      recoveryController.cancel();
      sourceRequestController.current?.abort();
      sourceRequestController.current = null;
      audioSwitchVersion.current += 1;
      pendingQuality.current = undefined;
      autoplayVersion.current += 1;
      autoplayRequest.current?.abort();
      autoplayRequest.current = null;
      sourceVersion.current += 1;
      window.clearTimeout(qualityStartupTimer.current);
      window.clearInterval(timer);
      window.removeEventListener('pagehide', pageHide);
      hls.current?.destroy();
      hls.current = null;
      const plan = retainedPlayback.current ?? playback.current;
      if (plan && !finalizing.current) {
        const save = endedPlayback.current
          ? completionAck.current?.then(() => undefined).catch(() => undefined) ?? Promise.resolve()
          : api.playbackHeartbeat(plan.session_id, currentPosition(), ++observation.current).catch(() => undefined);
        void settleWithin(save, finalHeartbeatWaitMS)
          .then(() => api.playbackStop(plan.session_id)).catch(() => undefined);
      }
    };
  }, [attach, catalogID, continueWatchingIntent, currentPosition, heartbeat, loadHls, planAttempt, startPositionMS]);

  const tryOriginal = useCallback(() => {
    qualityPreferenceRef.current = 'original';
    setQualityPreference('original');
    saveQualityPreference('original');
    qualityPolicy.current = new AdaptiveQualityPolicy(initialAutoQualityTier(window.location.origin));
    playIntent.current = true;
    autoStart.current = true;
    setOriginalFallback(false);
    dispatch({ type: 'stop' });
    setPlanAttempt((attempt) => attempt + 1);
  }, []);

  const cancelAutoplay = useCallback(() => {
    autoplayVersion.current += 1;
    autoplayRequest.current?.abort();
    autoplayRequest.current = null;
    setAutoplay((current) => {
      if (current.kind === 'countdown') return { kind: 'paused', episode: current.episode };
      if (current.kind === 'resolving') return { kind: 'paused' };
      if (current.kind === 'advancing') return { kind: 'paused', episode: current.episode };
      return current;
    });
  }, []);

  const advanceTo = useCallback(async (episode: Episode, intent: 'user' | 'automatic' = 'user') => {
    if (finalizing.current) return;
    setAudioLocked(true);
    const version = ++autoplayVersion.current;
    autoplayRequest.current?.abort();
    autoplayRequest.current = null;
    setAutoplay({ kind: 'advancing', episode });
    const plan = retainedPlayback.current ?? playback.current;
    if (plan) {
      finalizing.current = true;
      try {
        if (!endedPlayback.current) {
          video.current?.pause();
          await settleWithin(api.playbackHeartbeat(plan.session_id, currentPosition(), ++observation.current), finalHeartbeatWaitMS);
        }
        await api.playbackStop(plan.session_id);
      } catch (error: unknown) {
        finalizing.current = false;
        dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'Flixr could not stop the completed episode.' });
        return;
      }
      playback.current = null;
    }
    if (version !== autoplayVersion.current) {
      finalizing.current = false;
      return;
    }
    autoStart.current = true;
    onAdvance?.(episode.id, intent);
  }, [onAdvance, currentPosition]);

  useEffect(() => {
    if (autoplay.kind !== 'countdown') return;
    if (document.hidden) {
      cancelAutoplay();
      return;
    }
    if (autoplay.seconds <= 0) {
      void advanceTo(autoplay.episode, 'automatic');
      return;
    }
    const timer = window.setTimeout(() => setAutoplay((current) => current.kind === 'countdown' ? { ...current, seconds: current.seconds - 1 } : current), 1_000);
    return () => window.clearTimeout(timer);
  }, [advanceTo, autoplay, cancelAutoplay]);

  useEffect(() => {
    const visibility = () => {
      if (document.hidden) cancelAutoplay();
    };
    document.addEventListener('visibilitychange', visibility);
    return () => document.removeEventListener('visibilitychange', visibility);
  }, [cancelAutoplay]);

  const seekTo = useCallback(async (target: number): Promise<'retained' | 'replacement' | 'failed' | 'superseded' | 'transitioned'> => {
    return enqueueSourceOperation(async () => {
    const plan = retainedPlayback.current ?? playback.current;
    const element = video.current;
    if (!plan || !element || finalizing.current || endedPlayback.current) return 'failed';
    if (plan.plan.kind === 'direct') {
      element.currentTime = target / 1000;
      dispatch({ type: 'progress', positionMs: currentPosition() });
      return 'retained';
    }
    const version = sourceVersion.current;
    setTrackError(undefined);
    const controller = new AbortController();
    sourceRequestController.current = controller;
    try {
      const updated = await api.playbackSeek(plan.session_id, target, ++observation.current, controller.signal);
      if (sourceRequestController.current === controller) sourceRequestController.current = null;
      if (version !== sourceVersion.current || finalizing.current || endedPlayback.current || !video.current) {
        if (updated.session_id !== plan.session_id) void api.playbackStop(updated.session_id).catch(() => undefined);
        return 'failed';
      }
      playback.current = updated;
      const attached = attachedPlayback.current;
      if (seekQueue.current.target !== undefined) {
        if (updated.media_url === attached?.media_url && updated.session_id === attached.session_id) retainedSeekFallback.current = { plan: updated, target };
        return 'superseded';
      }
      retainedSeekFallback.current = undefined;
      if (updated.media_url !== attached?.media_url || updated.session_id !== attached.session_id) {
        autoStart.current = seekPlayingIntent.current;
        seekSourceTransitioning.current = true;
        await attach(updated);
        return 'replacement';
      } else {
        initializingPosition.current = true;
        element.currentTime = Math.max(0, target - updated.stream_offset_ms) / 1000;
        dispatch({ type: 'progress', positionMs: currentPosition() });
        return 'retained';
      }
    } catch (error: unknown) {
      if (sourceRequestController.current === controller) sourceRequestController.current = null;
      if (finalizing.current || endedPlayback.current) return 'failed';
      const sessionInvalid = error instanceof ApiError && error.code === 'playback_session_invalid';
      if (sessionInvalid) retainedSeekFallback.current = undefined;
      if (!sessionInvalid && seekQueue.current.target === undefined) {
        const current = playback.current;
        const attached = attachedPlayback.current;
        if (current && (current.media_url !== attached?.media_url || current.session_id !== attached.session_id)) {
          retainedSeekFallback.current = undefined;
          autoStart.current = seekPlayingIntent.current;
          seekSourceTransitioning.current = true;
          await attach(current);
          return 'replacement';
        }
        const fallback = retainedSeekFallback.current;
        if (current && fallback && fallback.plan.media_url === current.media_url && fallback.plan.session_id === current.session_id) {
          retainedSeekFallback.current = undefined;
          initializingPosition.current = true;
          element.currentTime = Math.max(0, fallback.target - current.stream_offset_ms) / 1000;
          dispatch({ type: 'progress', positionMs: currentPosition() });
          return 'retained';
        }
      }
      if (version === sourceVersion.current) {
        if (isRetryablePlaybackFailure(error)) {
          await recover(error);
          return 'transitioned';
        }
        else setTrackError(error instanceof ApiError ? error.message : 'Flixr could not seek in this stream. Try again.');
      }
      return 'failed';
    }
    });
  }, [attach, recover, currentPosition, enqueueSourceOperation]);
  const requestSeek = useCallback((target: number) => {
    const element = video.current;
    if (!element) return;
    qualityPolicy.current.resetBufferEvidence();
    if (seekQueue.current.running) {
      seekQueue.current.target = target;
      setPendingSeekMS(target);
      return;
    }
    const plan = playback.current;
    if (!plan) return;
    if (plan.plan.kind === 'direct') {
      void seekTo(target);
      return;
    }
    seekQueue.current.target = target;
    setPendingSeekMS(target);
    seekQueue.current.running = true;
    seekingRef.current = true;
    seekPlayingIntent.current = initialPlayPending.current || !element.paused;
    setSeekPlaying(seekPlayingIntent.current);
    setSeeking(true);
    element.pause();
    void (async () => {
      let outcome: Awaited<ReturnType<typeof seekTo>> = 'failed';
      let attachedDuringSeek = false;
      try {
        while (seekQueue.current.target !== undefined && !finalizing.current) {
          const next = seekQueue.current.target;
          seekQueue.current.target = undefined;
          outcome = await seekTo(next);
          if (outcome === 'replacement') attachedDuringSeek = true;
        }
      } finally {
        seekQueue.current.running = false;
        seekQueue.current.target = undefined;
        seekingRef.current = false;
        setPendingSeekMS(undefined);
        setSeeking(false);
        if (!finalizing.current && !attachedDuringSeek && outcome !== 'transitioned') {
          if (seekPlayingIntent.current) {
            void video.current?.play().catch(() => {
              setTrackError('Playback could not resume after seeking. Press Play to try again.');
              dispatch({ type: 'pause' });
            });
          } else {
            dispatch({ type: 'pause' });
          }
        }
      }
    })();
  }, [seekTo]);
  const play = async () => {
    if (seekingRef.current) {
      seekPlayingIntent.current = true;
      playIntent.current = true;
      autoStart.current = true;
      setSeekPlaying(true);
      return;
    }
    try {
      playIntent.current = true;
      autoStart.current = true;
      setTrackError(undefined);
      await video.current?.play();
      dispatch({ type: 'play' });
    } catch {
      playIntent.current = false;
      autoStart.current = false;
      setTrackError('Playback could not start. Press Play to try again.');
      dispatch({ type: 'pause' });
    }
  };

  const pause = useCallback(() => {
    initialPlayPending.current = false;
    playIntent.current = false;
    autoStart.current = false;
    if (seekingRef.current) {
      seekPlayingIntent.current = false;
      setSeekPlaying(false);
      cancelAutoplay();
      return;
    }
    video.current?.pause();
    cancelAutoplay();
  }, [cancelAutoplay]);

  useEffect(() => screenCoordinator.onCommand((command) => {
    if (command.type === 'pause') pause();
    if (command.type === 'seek') requestSeek(command.position_ms);
  }), [pause, requestSeek]);

  const togglePlayback = () => {
    if (seekingRef.current) {
      if (seekPlayingIntent.current) pause();
      else void play();
      return;
    }
    if (video.current?.paused) void play();
    else pause();
  };

  const finish = async () => {
    if (finalizing.current) return;
    finalizing.current = true;
    recovery.current.cancel();
    sourceRequestController.current?.abort();
    sourceRequestController.current = null;
    setAudioLocked(true);
    autoplayVersion.current += 1;
    autoplayRequest.current?.abort();
    autoplayRequest.current = null;
    await settleWithin(sourceOperations.current, finalHeartbeatWaitMS);
    sourceVersion.current += 1;
    const plan = retainedPlayback.current ?? playback.current;
    if (plan) {
      const save = endedPlayback.current
        ? completionAck.current?.then(() => undefined).catch(() => undefined) ?? Promise.resolve()
        : api.playbackHeartbeat(plan.session_id, currentPosition(), ++observation.current).catch(() => undefined);
      await settleWithin(save, finalHeartbeatWaitMS);
      void api.playbackStop(plan.session_id).catch(() => undefined);
    }
    onExit();
  };

  const complete = async () => {
    if (!playback.current || finalizing.current || endedPlayback.current) return;
    endedPlayback.current = true;
    sourceRequestController.current?.abort();
    sourceRequestController.current = null;
    setAudioLocked(true);
    const version = ++autoplayVersion.current;
    autoplayRequest.current?.abort();
    const controller = new AbortController();
    autoplayRequest.current = controller;
    setAutoplay({ kind: 'resolving' });
    const acknowledgement = (async () => {
      await settleWithin(sourceOperations.current, finalHeartbeatWaitMS);
      return heartbeat(true);
    })();
    completionAck.current = acknowledgement;
    const accepted = await acknowledgement;
    if (completionAck.current === acknowledgement) completionAck.current = null;
    const plan = retainedPlayback.current ?? playback.current;
    if (!plan) return;
    if (!accepted || controller.signal.aborted || version !== autoplayVersion.current) return;
    try {
      const next = await api.playbackNext(plan.session_id, false, controller.signal);
      if (controller.signal.aborted || version !== autoplayVersion.current || playback.current?.session_id !== plan.session_id) return;
      autoplayRequest.current = null;
      if (next.state === 'not_episodic') {
        void finish();
      } else if (next.state === 'next') {
        setAutoplay({ kind: 'countdown', episode: next.episode, seconds: autoplaySeconds });
      } else {
        setAutoplay({ kind: 'end', contextUnavailable: next.state === 'context_unavailable' });
      }
    } catch (error: unknown) {
      if (controller.signal.aborted || version !== autoplayVersion.current) return;
      dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'Flixr could not check for the next episode.' });
    }
  };

  const seeked = async () => enqueueSourceOperation(async () => {
    qualityPolicy.current.resetBufferEvidence();
    const plan = playback.current;
    if (!plan || finalizing.current || endedPlayback.current) return;
    if (plan.plan.kind === 'direct') { await heartbeat(); return; }
    const version = sourceVersion.current;
    const target = currentPosition();
    setTrackError(undefined);
    const controller = new AbortController();
    sourceRequestController.current = controller;
    try {
      const updated = await api.playbackSeek(plan.session_id, target, ++observation.current, controller.signal);
      if (sourceRequestController.current === controller) sourceRequestController.current = null;
      if (version !== sourceVersion.current || finalizing.current || endedPlayback.current) {
        if (updated.session_id !== plan.session_id) void api.playbackStop(updated.session_id).catch(() => undefined);
        return;
      }
      if (updated.session_id !== plan.session_id || updated.media_url !== plan.media_url) void attach(updated);
    } catch (error: unknown) {
      if (sourceRequestController.current === controller) sourceRequestController.current = null;
      if (version === sourceVersion.current && !finalizing.current && !endedPlayback.current) {
        if (isRetryablePlaybackFailure(error)) void recover(error);
        else setTrackError(error instanceof ApiError ? error.message : 'Flixr could not seek in this stream. Try again.');
      }
    }
  });

  const changeAudio = async (value: string) => {
    if (!playback.current || !video.current || switchingAudio || audioSwitchTask.current || audioLocked || finalizing.current || endedPlayback.current) return;
    const [source, indexValue] = value.split(':', 2);
    const streamIndex = Number(indexValue);
    if ((source !== 'embedded' && source !== 'external') || !Number.isInteger(streamIndex) || streamIndex < 0) return;
    const switchVersion = ++audioSwitchVersion.current;
    setSwitchingAudio(true);
    setTrackError(undefined);
    const performSwitch = async () => {
      await enqueueSourceOperation(async () => {
      const plan = playback.current;
      const element = video.current;
      if (!plan || !element || finalizing.current || endedPlayback.current) return;
      const version = sourceVersion.current;
      const positionMs = currentPosition();
      autoStart.current = playIntent.current;
      replacingSession.current = plan.session_id;
      try {
        const item = await api.item(catalogID);
        const selected = plan.audio_tracks?.find((track) => track.index === streamIndex && Boolean(track.external) === (source === 'external'));
        const capabilities = await browserCapabilities(selected ? { ...item, audio: [selected] } : item);
        if (finalizing.current || endedPlayback.current || version !== sourceVersion.current) return;
        const controller = new AbortController();
        sourceRequestController.current = controller;
        const updated = await api.playbackAudio(plan.session_id, streamIndex, source === 'external', positionMs, ++observation.current, capabilities, controller.signal);
        if (sourceRequestController.current === controller) sourceRequestController.current = null;
        if (version !== sourceVersion.current || finalizing.current || endedPlayback.current || !video.current) {
          void api.playbackStop(updated.session_id).catch(() => undefined);
          return;
        }
        const resumeMS = Math.max(updated.resume_ms, currentPosition());
        if (seekingRef.current) autoStart.current = false;
        await attach({ ...updated, resume_ms: resumeMS });
      } catch (error: unknown) {
        sourceRequestController.current = null;
        if (version === sourceVersion.current && !finalizing.current && !endedPlayback.current) setTrackError(error instanceof ApiError ? error.message : 'Flixr could not change the audio track.');
      } finally {
        if (replacingSession.current === plan.session_id) replacingSession.current = undefined;
      }
      });
      if (switchVersion === audioSwitchVersion.current) setSwitchingAudio(false);
    };
    const task = performSwitch();
    audioSwitchTask.current = task;
    try {
      await task;
    } finally {
      if (audioSwitchTask.current === task) audioSwitchTask.current = null;
    }
  };

  const changeSubtitle = async (value: string) => {
    const plan = playback.current;
    if (!plan || switchingSubtitle) return;
    const version = sourceVersion.current;
    const switchVersion = ++subtitleSwitchVersion.current;
    setSwitchingSubtitle(true);
    setTrackError(undefined);
    try {
      const selection = value === 'off'
        ? { mode: 'off' as const }
        : (() => {
            const [source, rawIndex] = value.split(':');
            return { mode: 'track' as const, subtitle_stream_index: Number(rawIndex), subtitle_external: source === 'external' };
          })();
      const updated = await api.playbackSubtitle(plan.session_id, selection);
      if (version === sourceVersion.current && switchVersion === subtitleSwitchVersion.current && playback.current?.session_id === plan.session_id) playback.current = updated;
    } catch (error: unknown) {
      if (version === sourceVersion.current && switchVersion === subtitleSwitchVersion.current && playback.current?.session_id === plan.session_id) {
        setTrackError(error instanceof ApiError ? error.message : 'Flixr could not change subtitles.');
      }
    } finally {
      if (version === sourceVersion.current && switchVersion === subtitleSwitchVersion.current && playback.current?.session_id === plan.session_id) setSwitchingSubtitle(false);
    }
  };

  const episodeIndex = episodes.findIndex((episode) => episode.id === catalogID);
  const statusLabel = seeking || state.status === 'idle' || state.status === 'loading' || state.status === 'buffering' ? 'Loading' : state.status === 'paused' ? 'Paused' : 'Playing on this device';
  const effectivePlan = playback.current?.plan;
  const effectiveQuality = effectivePlan
    ? `${effectivePlan.height ? `${effectivePlan.height}p` : 'Original'}${effectivePlan.video_bitrate ? ` · ${(effectivePlan.video_bitrate / 1_000_000).toFixed(effectivePlan.video_bitrate % 1_000_000 ? 1 : 0)} Mbps` : ''}`
    : undefined;

  return <main ref={stage} tabIndex={-1} className={`player player-immersive ${controlsVisible ? '' : 'controls-hidden'} ${keyboardControls ? 'keyboard-controls' : ''}`}>
    <header className="player-header">
      <button ref={backButton} className="player-back" onClick={() => { void finish(); }}>← Back to library</button>
      <div className="player-heading"><h1 id="player-title">{seriesTitle ? `${seriesTitle} · ${item?.title}` : item?.title ?? 'Now playing'}</h1>{item?.kind === 'episode' && <p>Season {item.season} · Episode {item.episode}</p>}</div>
    </header>
    {state.status === 'error' ? <section className="state player-error" role="alert" aria-labelledby="player-title"><h2>Playback stopped</h2><p>{state.message}</p>{originalFallback && <button className="primary" onClick={tryOriginal}>Try Original quality</button>}<button onClick={() => { void finish(); }}>Return to your library</button></section> :
      <section className="player-stage" aria-labelledby="player-title">
        <div className="player-frame">
          {(seeking || state.status === 'idle' || state.status === 'loading' || state.status === 'buffering') && <div className="player-loading" aria-hidden="true"><span className="button-spinner" /></div>}
          <video
            ref={video}
            preload="auto"
            playsInline
            onDurationChange={() => { const seconds = video.current?.duration; if (seconds && Number.isFinite(seconds)) setDurationMS(seconds * 1000 + (playback.current?.stream_offset_ms ?? 0)); }}
            onLoadedMetadata={() => {
              const version = sourceVersion.current;
              if (activatedSourceVersion.current !== version) positionAttachedSource(version);
              if (deferredActivationVersion.current !== version) activateAttachedSource(version);
            }}
            onCanPlay={() => { window.clearTimeout(qualityStartupTimer.current); qualityPolicy.current.playing(); seekSourceTransitioning.current = false; dispatch({ type: video.current?.paused ? 'pause' : 'play' }); }}
            onError={() => {
              if (recovering.current || finalizing.current || sourceOperationActive.current) return;
              const plan = playback.current;
              if (video.current?.error?.code === 2 || (plan !== null && plan.plan.kind !== 'direct')) void recover(new PlaybackNetworkError());
              else dispatch({ type: 'error', message: 'This media could not be loaded. Check that the file is still available, then start the title again.' });
            }}
            onEnded={() => { dispatch({ type: 'pause' }); void complete(); }}
            onPlaying={() => { seekSourceTransitioning.current = false; initialPlayPending.current = false; playIntent.current = true; autoStart.current = true; dispatch({ type: 'play' }); }}
            onPause={() => {
              if (seekingRef.current || seekSourceTransitioning.current) return;
              dispatch({ type: 'pause' });
              if (!endedPlayback.current && !finalizing.current) cancelAutoplay();
              void heartbeat();
            }}
            onWaiting={() => dispatch({ type: 'buffering' })}
            onSeeked={() => {
              if (initializingPosition.current) {
                initializingPosition.current = false;
                return;
              }
              void seeked();
            }}
            onTimeUpdate={() => {
              if (seekingRef.current || seekSourceTransitioning.current) return;
              if (playerStatus.current === 'buffering' && video.current?.paused === false) dispatch({ type: 'play' });
              dispatch({ type: 'progress', positionMs: currentPosition() });
            }}
          >
            {playback.current?.subtitle_url && playback.current.selected_subtitle && <track
              key={playback.current.subtitle_url}
              kind="subtitles"
              src={playback.current.subtitle_url}
              srcLang={playback.current.selected_subtitle.language || 'und'}
              label={subtitleLabel(playback.current.selected_subtitle)}
              default
            />}
          </video>
          {autoplay.kind !== 'idle' && <section className="player-autoplay" role="dialog" aria-modal="true" aria-labelledby="autoplay-title">
            {autoplay.kind === 'advancing' && <><h2 id="autoplay-title">Starting next episode</h2><button onClick={() => { cancelAutoplay(); void finish(); }}>Cancel autoplay</button></>}
            {autoplay.kind === 'resolving' && <><h2 id="autoplay-title">Episode complete</h2><p role="status">Finding the next episode in this version…</p><button onClick={() => { cancelAutoplay(); void finish(); }}>Cancel autoplay</button></>}
            {autoplay.kind === 'countdown' && <><h2 id="autoplay-title">Next episode</h2><p className="player-autoplay-episode">S{autoplay.episode.season} E{autoplay.episode.episode} · {autoplay.episode.title}</p><p role="status">Playing in {autoplay.seconds} seconds.</p><div className="player-autoplay-actions"><button className="primary" onClick={() => { void advanceTo(autoplay.episode); }}>Play now</button><button onClick={() => { cancelAutoplay(); void finish(); }}>Cancel autoplay</button></div></>}
            {autoplay.kind === 'paused' && <><h2 id="autoplay-title">Autoplay paused</h2><p>The next episode will not start automatically.</p><div className="player-autoplay-actions">{autoplay.episode && <button className="primary" onClick={() => { void advanceTo(autoplay.episode!); }}>Play next episode</button>}<button onClick={() => { void finish(); }}>Return to your library</button></div></>}
            {autoplay.kind === 'end' && <><h2 id="autoplay-title">{autoplay.contextUnavailable ? 'No next episode in this version' : 'End of series'}</h2><p>{autoplay.contextUnavailable ? 'Flixr will not switch to another library or cut automatically.' : 'You have watched every later available episode in this series.'}</p><button className="primary" onClick={() => { void finish(); }}>Return to your library</button></>}
          </section>}
        </div>
          <p role="status" className="player-status player-sr-only"><span className={`player-status-dot ${state.status}`} aria-hidden="true" />{statusLabel}</p>
        <div className="player-toolbar">
          <PlayerControls seeking={seeking} playing={seeking ? seekPlaying : state.status === 'playing'} durationMS={item?.duration_ms || durationMS} positionMS={pendingSeekMS ?? state.positionMs} disabled={(!playback.current && !seeking) || audioLocked || switchingAudio || state.status === 'loading'} onSeek={requestSeek} onPrevious={onAdvance && episodeIndex > 0 ? () => { void advanceTo(episodes[episodeIndex - 1]); } : undefined} onNext={onAdvance && episodeIndex >= 0 && episodeIndex < episodes.length - 1 ? () => { void advanceTo(episodes[episodeIndex + 1]); } : undefined} onToggle={togglePlayback} video={video} stage={stage} chapters={chapters} sessionID={sessionID} playbackRate={playbackRate} onPlaybackRateChange={(rate) => { playbackRateIntent.current = rate; setPlaybackRate(rate); }} quality={qualityPreference} effectiveQuality={effectiveQuality} onQualityChange={changeQuality}>
          {(playback.current?.audio_tracks?.length ?? 0) > 1 && <label className="player-audio">Audio track
            <select
              aria-busy={switchingAudio}
              disabled={switchingAudio || audioLocked}
              value={`${playback.current?.plan.audio_external ? 'external' : 'embedded'}:${playback.current?.plan.audio_stream_index ?? ''}`}
              onChange={(event) => { void changeAudio(event.target.value); }}
            >
              {playback.current?.audio_tracks?.map((track) => <option key={`${track.external ? 'external' : 'embedded'}:${track.index}`} value={`${track.external ? 'external' : 'embedded'}:${track.index}`}>{audioLabel(track)}</option>)}
            </select>
          </label>}
          {(playback.current?.subtitle_tracks?.length ?? 0) > 0 && <label className="player-audio">Subtitles
            <select
              aria-busy={switchingSubtitle}
              disabled={switchingSubtitle || audioLocked}
              value={playback.current?.selected_subtitle ? `${playback.current.selected_subtitle.external ? 'external' : 'embedded'}:${playback.current.selected_subtitle.index}` : 'off'}
              onChange={(event) => { void changeSubtitle(event.target.value); }}
            >
              <option value="off">Off</option>
              {playback.current?.subtitle_tracks?.map((track) => <option key={`${track.external ? 'external' : 'embedded'}:${track.index}`} value={`${track.external ? 'external' : 'embedded'}:${track.index}`}>{subtitleLabel(track)}</option>)}
            </select>
          </label>}
          {onAdvance && episodes.length > 1 && <label>Episodes<select aria-label="Episode" value={catalogID} disabled={audioLocked} onChange={(event) => { const episode = episodes.find((entry) => entry.id === event.target.value); if (episode) void advanceTo(episode); }}>
            {episodes.map((episode) => <option key={episode.id} value={episode.id}>S{episode.season} E{episode.episode} · {episode.title}</option>)}
          </select></label>}
          <details className="player-description"><summary>Playback information</summary><p>{playback.current?.plan.kind === 'direct' ? 'Original quality' : 'Compatibility playback'} · {item?.width} × {item?.height}</p></details>
          {item?.synopsis && <details className="player-description"><summary>About this title</summary><p>{item.synopsis}</p></details>}
          </PlayerControls>
        </div>
        {trackError && <p className="player-track-error" role="alert">{trackError}</p>}

      </section>}
  </main>;
}
