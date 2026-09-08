import type Hls from 'hls.js';
import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type CatalogItem, type Episode, type PlaybackCapabilities, type PlaybackPlan } from '../../core/api';
import { initialPlayerState, playerReducer } from './state';
import { isRetryablePlaybackFailure, PlaybackNetworkError, PlaybackRecovery } from './recovery';
import { screenCoordinator } from '../screenCoordinator/runtime';
import { browserCapabilities } from './capabilities';
import { PlayerControls, type Chapter } from './PlayerControls';
import './player.css';

const maxConsecutiveRecoveries = 3;
const finalHeartbeatWaitMS = 2_000;

async function settleWithin(promise: Promise<unknown>, timeoutMS: number): Promise<void> {
  let timer = 0;
  await Promise.race([
    promise.catch(() => undefined),
    new Promise<void>((resolve) => { timer = window.setTimeout(resolve, timeoutMS); }),
  ]);
  window.clearTimeout(timer);
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
  const [seeking, setSeeking] = useState(false);
  const seekQueue = useRef<{ running: boolean; target?: number }>({ running: false });
  const [trackError, setTrackError] = useState<string>();
  const [switchingAudio, setSwitchingAudio] = useState(false);
  const [switchingSubtitle, setSwitchingSubtitle] = useState(false);
  const [audioLocked, setAudioLocked] = useState(false);
  const [autoplay, setAutoplay] = useState<AutoplayState>({ kind: 'idle' });
  const video = useRef<HTMLVideoElement>(null);
  const backButton = useRef<HTMLButtonElement>(null);
  const hls = useRef<Hls | null>(null);
  const sourceVersion = useRef(0);
  const audioSwitchVersion = useRef(0);
  const subtitleSwitchVersion = useRef(0);
  const audioSwitchTask = useRef<Promise<void> | null>(null);
  const replacingSession = useRef<string | undefined>(undefined);
  const playback = useRef<PlaybackPlan | null>(null);
  const observation = useRef(0);
  const initializingPosition = useRef(false);
  const finalizing = useRef(false);
  const recovering = useRef(false);
  const consecutiveRecoveries = useRef(0);
  const recovery = useRef(new PlaybackRecovery());
  const recoverRef = useRef<(error: unknown) => void>(() => undefined);
  const endedPlayback = useRef(false);
  const completionAck = useRef<Promise<boolean> | null>(null);
  const autoplayVersion = useRef(0);
  const autoplayRequest = useRef<AbortController | null>(null);
  const autoStart = useRef(true);
  const initialPlayPending = useRef(true);

  useEffect(() => {
    if (active) stage.current?.focus();
  }, [active]);

  useEffect(() => {
    const root = stage.current;
    let timer = 0;
    const reveal = () => {
      setControlsVisible(true);
      window.clearTimeout(timer);
      if (state.status === 'playing' && !seeking) timer = window.setTimeout(() => {
        const focused = document.activeElement;
        if (!root?.querySelector('details[open]') && (!focused || focused === root || !root?.contains(focused))) setControlsVisible(false);
      }, 3000);
    };
    reveal();
    for (const event of ['pointermove', 'pointerdown', 'keydown', 'focusin', 'focusout', 'toggle']) root?.addEventListener(event, reveal, true);
    return () => { window.clearTimeout(timer); for (const event of ['pointermove', 'pointerdown', 'keydown', 'focusin', 'focusout', 'toggle']) root?.removeEventListener(event, reveal, true); };
  }, [state.status, seeking]);

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

  const attach = useCallback(async (plan: PlaybackPlan) => {
    const element = video.current;
    if (!element) return;
    const version = ++sourceVersion.current;
    subtitleSwitchVersion.current += 1;
    setSwitchingSubtitle(false);
    hls.current?.destroy();
    hls.current = null;
    playback.current = plan;
    dispatch({ type: 'load', source: plan.media_url, resumeMs: plan.resume_ms });
    const rate = element.playbackRate;
    element.removeAttribute('src');
    element.load();
    element.playbackRate = rate;
    // Prefer the bundled engine for live/sliding HLS; native MIME support alone
    // does not establish reliable live playback (notably in Chromium).
    const mse = typeof MediaSource !== 'undefined' && MediaSource.isTypeSupported('video/mp4; codecs="avc1.640028, mp4a.40.2"');
    if (plan.plan.kind === 'direct' || (!mse && element.canPlayType('application/vnd.apple.mpegurl'))) {
      element.src = plan.media_url;
      return;
    }
    const { default: Hls } = await import('hls.js');
    if (version !== sourceVersion.current || !video.current) return;
    if (!Hls.isSupported()) {
      dispatch({ type: 'error', message: 'This browser cannot play the compatibility stream.' });
      return;
    }
    const next = new Hls({ enableWorker: true, lowLatencyMode: false });
    next.on(Hls.Events.ERROR, (_event, data) => {
      if (version !== sourceVersion.current || finalizing.current) return;
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
        recoverRef.current(new PlaybackNetworkError());
      } else if (data.fatal) {
        dispatch({ type: 'error', message: 'The compatibility stream stopped unexpectedly.' });
      }
    });
    next.loadSource(plan.media_url);
    next.attachMedia(video.current);
    hls.current = next;
  }, []);

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
    const continuePlaying = initialPlayPending.current || (video.current ? !video.current.paused : false);
    playback.current = null;
    sourceVersion.current += 1;
    hls.current?.destroy();
    hls.current = null;
    video.current?.removeAttribute('src');
    video.current?.load();
    dispatch({ type: 'buffering' });
    try {
      const next = await recovery.current.run(async () => {
        if (oldPlan) await api.playbackStop(oldPlan.session_id).catch(() => undefined);
        return api.playbackPlan(catalogID, await browserCapabilities(await api.item(catalogID)), 'recovery');
      }, (abandoned) => { void api.playbackStop(abandoned.session_id).catch(() => undefined); });
      if (!next || finalizing.current) return;
      observation.current = 0;
      autoStart.current = continuePlaying;
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
  recoverRef.current = (error) => { void recover(error); };

  const heartbeat = useCallback(async (ended = false) => {
    const plan = playback.current;
    // Stop revokes the session before the media element is unmounted.
    if (!plan || recovering.current || (finalizing.current && !ended) || (endedPlayback.current && !ended)) return false;
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
      if (version !== sourceVersion.current || (finalizing.current && !ended) || playback.current?.session_id !== plan.session_id || replacingSession.current === plan.session_id) return false;
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
    setAutoplay({ kind: 'idle' });
    recoveryController.run(
      async () => {
        const detail = await api.item(catalogID);
        if (!active) throw new Error('player unmounted');
        setItem(detail);
        const capabilities: PlaybackCapabilities = await browserCapabilities(detail);
        if (!active) throw new Error('player unmounted');
        return api.playbackPlan(catalogID, capabilities, continueWatchingIntent);
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
        const message = isRetryablePlaybackFailure(error)
          ? 'Playback could not connect to Flixr after several attempts. Check this device\'s local network connection and try again.'
          : error instanceof ApiError ? error.message : 'Playback could not start.';
        dispatch({ type: 'error', message });
      }
    });
    const pageHide = () => {
      const plan = playback.current;
      if (!plan || finalizing.current || endedPlayback.current) return;
      const body = new Blob([JSON.stringify({ position_ms: currentPosition(), observation: ++observation.current })], { type: 'application/json' });
      navigator.sendBeacon(plan.heartbeat_url, body);
    };
    window.addEventListener('pagehide', pageHide);
    return () => {
      active = false;
      recoveryController.cancel();
      audioSwitchVersion.current += 1;
      autoplayVersion.current += 1;
      autoplayRequest.current?.abort();
      autoplayRequest.current = null;
      sourceVersion.current += 1;
      window.clearInterval(timer);
      window.removeEventListener('pagehide', pageHide);
      hls.current?.destroy();
      hls.current = null;
      const plan = playback.current;
      if (plan && !finalizing.current) {
        const save = endedPlayback.current
          ? completionAck.current?.then(() => undefined).catch(() => undefined) ?? Promise.resolve()
          : api.playbackHeartbeat(plan.session_id, currentPosition(), ++observation.current).catch(() => undefined);
        void settleWithin(save, finalHeartbeatWaitMS)
          .then(() => api.playbackStop(plan.session_id)).catch(() => undefined);
      }
    };
  }, [attach, catalogID, continueWatchingIntent, currentPosition, heartbeat, startPositionMS]);

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
    const plan = playback.current;
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

  const seekTo = useCallback(async (target: number) => {
    const plan = playback.current;
    const element = video.current;
    if (!plan || !element) return;
    if (plan.plan.kind === 'direct') {
      element.currentTime = target / 1000;
      dispatch({ type: 'progress', positionMs: currentPosition() });
      return;
    }
    const version = sourceVersion.current;
    try {
      const updated = await api.playbackSeek(plan.session_id, target, ++observation.current);
      if (version !== sourceVersion.current || finalizing.current || !video.current) {
        if (updated.session_id !== plan.session_id) void api.playbackStop(updated.session_id).catch(() => undefined);
        return;
      }
      if (updated.media_url !== plan.media_url || updated.session_id !== plan.session_id) {
        autoStart.current = !element.paused;
        await attach(updated);
      } else {
        initializingPosition.current = true;
        element.currentTime = Math.max(0, target - updated.stream_offset_ms) / 1000;
        dispatch({ type: 'progress', positionMs: currentPosition() });
      }
    } catch (error: unknown) {
      if (version === sourceVersion.current) {
        if (isRetryablePlaybackFailure(error)) void recover(error);
        else dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'Flixr could not seek in this stream.' });
      }
    }
  }, [attach, recover, currentPosition]);
  const requestSeek = useCallback((target: number) => {
    seekQueue.current.target = target;
    if (seekQueue.current.running) return;
    seekQueue.current.running = true;
    setSeeking(true);
    void (async () => {
      try {
        while (seekQueue.current.target !== undefined && !finalizing.current) {
          const next = seekQueue.current.target;
          seekQueue.current.target = undefined;
          await seekTo(next);
        }
      } finally {
        seekQueue.current.running = false;
        seekQueue.current.target = undefined;
        setSeeking(false);
      }
    })();
  }, [seekTo]);
  useEffect(() => screenCoordinator.onCommand((command) => {
    if (command.type === 'pause') {
      video.current?.pause();
      cancelAutoplay();
    }
    if (command.type === 'seek') requestSeek(command.position_ms);
  }), [cancelAutoplay, requestSeek]);

  const play = async () => {
    try {
      setTrackError(undefined);
      await video.current?.play();
      dispatch({ type: 'play' });
    } catch {
      setTrackError('Playback could not start. Press Play to try again.');
      dispatch({ type: 'pause' });
    }
  };

  const pause = () => {
    initialPlayPending.current = false;
    autoStart.current = false;
    video.current?.pause();
    cancelAutoplay();
  };

  const finish = async () => {
    if (finalizing.current) return;
    finalizing.current = true;
    recovery.current.cancel();
    setAudioLocked(true);
    autoplayVersion.current += 1;
    autoplayRequest.current?.abort();
    autoplayRequest.current = null;
    await audioSwitchTask.current;
    sourceVersion.current += 1;
    const plan = playback.current;
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
    setAudioLocked(true);
    const version = ++autoplayVersion.current;
    autoplayRequest.current?.abort();
    const controller = new AbortController();
    autoplayRequest.current = controller;
    setAutoplay({ kind: 'resolving' });
    const acknowledgement = (async () => {
      await audioSwitchTask.current;
      return heartbeat(true);
    })();
    completionAck.current = acknowledgement;
    const accepted = await acknowledgement;
    if (completionAck.current === acknowledgement) completionAck.current = null;
    const plan = playback.current;
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

  const seeked = async () => {
    const plan = playback.current;
    if (!plan) return;
    if (plan.plan.kind === 'direct') { await heartbeat(); return; }
    const version = sourceVersion.current;
    const target = currentPosition();
    try {
      const updated = await api.playbackSeek(plan.session_id, target, ++observation.current);
      if (version !== sourceVersion.current || finalizing.current) {
        if (updated.session_id !== plan.session_id) void api.playbackStop(updated.session_id).catch(() => undefined);
        return;
      }
      if (updated.session_id !== plan.session_id || updated.media_url !== plan.media_url) void attach(updated);
    } catch (error: unknown) {
      if (version === sourceVersion.current && !finalizing.current) {
        if (isRetryablePlaybackFailure(error)) void recover(error);
        else dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'Flixr could not seek in this stream.' });
      }
    }
  };

  const changeAudio = async (value: string) => {
    const plan = playback.current;
    const element = video.current;
    if (!plan || !element || switchingAudio || audioSwitchTask.current || audioLocked || finalizing.current || endedPlayback.current) return;
    const [source, indexValue] = value.split(':', 2);
    const streamIndex = Number(indexValue);
    if ((source !== 'embedded' && source !== 'external') || !Number.isInteger(streamIndex) || streamIndex < 0) return;
    const version = sourceVersion.current;
    const switchVersion = ++audioSwitchVersion.current;
    const positionMs = currentPosition();
    autoStart.current = !element.paused;
    setSwitchingAudio(true);
    setTrackError(undefined);
    replacingSession.current = plan.session_id;
    const performSwitch = async () => {
      try {
        const item = await api.item(catalogID);
        const selected = plan.audio_tracks?.find((track) => track.index === streamIndex && Boolean(track.external) === (source === 'external'));
        const capabilities = await browserCapabilities(selected ? { ...item, audio: [selected] } : item);
        const updated = await api.playbackAudio(plan.session_id, streamIndex, source === 'external', positionMs, ++observation.current, capabilities);
        if (version !== sourceVersion.current || !video.current) {
          void api.playbackStop(updated.session_id).catch(() => undefined);
          return;
        }
        await attach(updated);
      } catch (error: unknown) {
        if (version === sourceVersion.current) setTrackError(error instanceof ApiError ? error.message : 'Flixr could not change the audio track.');
      } finally {
        if (replacingSession.current === plan.session_id) replacingSession.current = undefined;
        if (switchVersion === audioSwitchVersion.current) setSwitchingAudio(false);
      }
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
  const statusLabel = state.status === 'idle' || state.status === 'loading' ? 'Preparing local playback…' : state.status === 'buffering' ? 'Buffering on your network…' : state.status === 'paused' ? 'Paused' : 'Playing on this device';

  return <main ref={stage} tabIndex={-1} className={`player player-immersive ${controlsVisible ? '' : 'controls-hidden'}`}>
    <header className="player-header">
      <button ref={backButton} className="player-back" onClick={() => { void finish(); }}>← Back to library</button>
      <div className="player-heading"><h1 id="player-title">{seriesTitle ? `${seriesTitle} · ${item?.title}` : item?.title ?? 'Now playing'}</h1>{item?.kind === 'episode' && <p>Season {item.season} · Episode {item.episode}</p>}</div>
    </header>
    {state.status === 'error' ? <section className="state player-error" role="alert" aria-labelledby="player-title"><h2>Playback stopped</h2><p>{state.message}</p><button onClick={() => { void finish(); }}>Return to your library</button></section> :
      <section className="player-stage" aria-labelledby="player-title">
        <div className="player-frame">
          {(state.status === 'idle' || state.status === 'loading' || state.status === 'buffering') && <div className="player-loading" aria-hidden="true"><span className="button-spinner" /><span>{state.status === 'buffering' ? 'Buffering…' : 'Starting playback…'}</span></div>}
          <video
            ref={video}
            preload="auto"
            playsInline
            onDurationChange={() => { const seconds = video.current?.duration; if (seconds && Number.isFinite(seconds)) setDurationMS(seconds * 1000 + (playback.current?.stream_offset_ms ?? 0)); }}
            onLoadedMetadata={() => {
              const plan = playback.current;
              if (!video.current || !plan) return;
              const resumeSeconds = Math.max(0, plan.resume_ms - plan.stream_offset_ms) / 1000;
              if (resumeSeconds > 0) initializingPosition.current = true;
              video.current.currentTime = resumeSeconds;
              if (autoStart.current) {
                autoStart.current = false;
                // Browser autoplay policy may require the receiver's local Play button.
                void video.current.play().catch((error: unknown) => { if (error instanceof DOMException && error.name === 'NotAllowedError') { initialPlayPending.current = false; setTrackError('Press Play to start watching.'); } dispatch({ type: 'pause' }); });
              }
            }}
            onCanPlay={() => dispatch({ type: video.current?.paused ? 'pause' : 'play' })}
            onError={() => {
              if (recovering.current || finalizing.current) return;
              const plan = playback.current;
              if (video.current?.error?.code === 2 || (plan !== null && plan.plan.kind !== 'direct')) void recover(new PlaybackNetworkError());
              else dispatch({ type: 'error', message: 'This media could not be loaded. Check that the file is still available, then start the title again.' });
            }}
            onEnded={() => { dispatch({ type: 'pause' }); void complete(); }}
            onPlaying={() => { initialPlayPending.current = false; dispatch({ type: 'play' }); }}
            onPause={() => {
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
            onTimeUpdate={() => dispatch({ type: 'progress', positionMs: currentPosition() })}
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
          <PlayerControls seeking={seeking} playing={state.status === 'playing'} durationMS={item?.duration_ms || durationMS} positionMS={state.positionMs} disabled={!playback.current || audioLocked || switchingAudio || state.status === 'loading'} onSeek={requestSeek} onPrevious={onAdvance && episodeIndex > 0 ? () => { void advanceTo(episodes[episodeIndex - 1]); } : undefined} onNext={onAdvance && episodeIndex >= 0 && episodeIndex < episodes.length - 1 ? () => { void advanceTo(episodes[episodeIndex + 1]); } : undefined} onToggle={() => { if (video.current?.paused) void play(); else pause(); }} video={video} stage={stage} chapters={chapters} sessionID={sessionID}>
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
