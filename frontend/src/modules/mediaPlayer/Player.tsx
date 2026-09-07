import type Hls from 'hls.js';
import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type PlaybackCapabilities, type PlaybackPlan } from '../../core/api';
import { initialPlayerState, playerReducer } from './state';
import { screenCoordinator } from '../screenCoordinator/runtime';

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

function browserCapabilities(): PlaybackCapabilities {
  const probe = document.createElement('video');
  const mp4 = probe.canPlayType('video/mp4; codecs="avc1.64001f, mp4a.40.2"') !== '';
  const webm = probe.canPlayType('video/webm; codecs="vp9, opus"') !== '';
  const nativeHLS = probe.canPlayType('application/vnd.apple.mpegurl') !== '';
  const mediaSourceHLS = typeof MediaSource !== 'undefined' && MediaSource.isTypeSupported('video/mp4; codecs="avc1.64001f, mp4a.40.2"');
  return {
    containers: [...(mp4 ? ['mp4'] : []), ...(webm ? ['webm'] : [])],
    video_codecs: [...(mp4 ? ['h264'] : []), ...(webm ? ['vp9'] : [])],
    video_profiles: mp4 ? ['Baseline', 'Main', 'High'] : [],
    audio_codecs: [...(mp4 ? ['aac'] : []), ...(webm ? ['opus'] : [])],
    supports_fmp4_hls: nativeHLS || mediaSourceHLS,
  };
}

export function Player({ catalogID, startPositionMS, active = true, onExit }: { catalogID: string; startPositionMS?: number; active?: boolean; onExit: () => void }) {
  const [state, dispatch] = useReducer(playerReducer, initialPlayerState);
  const [trackError, setTrackError] = useState<string>();
  const [switchingAudio, setSwitchingAudio] = useState(false);
  const video = useRef<HTMLVideoElement>(null);
  const backButton = useRef<HTMLButtonElement>(null);
  const hls = useRef<Hls | null>(null);
  const sourceVersion = useRef(0);
  const audioSwitchVersion = useRef(0);
  const playback = useRef<PlaybackPlan | null>(null);
  const observation = useRef(0);
  const initializingPosition = useRef(false);
  const finalizing = useRef(false);
  const autoStart = useRef(startPositionMS !== undefined);

  useEffect(() => {
    if (active) backButton.current?.focus();
  }, [active]);

  const currentPosition = useCallback(() => {
    const relative = Math.round((video.current?.currentTime ?? 0) * 1000);
    return Math.max(0, (playback.current?.stream_offset_ms ?? 0) + relative);
  }, []);

  const attach = useCallback(async (plan: PlaybackPlan) => {
    const element = video.current;
    if (!element) return;
    const version = ++sourceVersion.current;
    hls.current?.destroy();
    hls.current = null;
    playback.current = plan;
    dispatch({ type: 'load', source: plan.media_url, resumeMs: plan.resume_ms });
    element.removeAttribute('src');
    element.load();
    if (plan.plan.kind === 'direct' || element.canPlayType('application/vnd.apple.mpegurl')) {
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
      if (data.fatal) dispatch({ type: 'error', message: 'The compatibility stream stopped unexpectedly.' });
    });
    next.loadSource(plan.media_url);
    next.attachMedia(video.current);
    hls.current = next;
  }, []);

  const heartbeat = useCallback(async (ended = false) => {
    const plan = playback.current;
    // Stop revokes the session before the media element is unmounted.
    if (!plan || finalizing.current) return;
    const positionMs = currentPosition();
    try {
      await api.playbackHeartbeat(plan.session_id, positionMs, ++observation.current, ended);
    } catch (error: unknown) {
      playback.current = null;
      dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'The local playback connection was interrupted.' });
    }
  }, [currentPosition]);

  useEffect(() => {
    let active = true;
    let timer = 0;
    api.playbackPlan(catalogID, browserCapabilities()).then(async (initial) => {
      if (!active) { void api.playbackStop(initial.session_id); return; }
      let plan = initial;
      playback.current = initial;
      observation.current = 0;
      if (startPositionMS !== undefined) {
        plan = initial.plan.kind === 'direct'
          ? { ...initial, resume_ms: startPositionMS }
          : await api.playbackSeek(initial.session_id, startPositionMS, ++observation.current);
      }
      if (!active) return;
      void attach(plan);
      const leaseRemaining = Math.max(3_000, plan.expires_at * 1_000 - Date.now());
      const heartbeatEvery = Math.max(1_000, Math.min(15_000, Math.floor(leaseRemaining / 3)));
      timer = window.setInterval(() => { void heartbeat(); }, heartbeatEvery);
    }).catch((error: unknown) => {
      if (active) dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'Playback could not start.' });
    });
    const pageHide = () => {
      const plan = playback.current;
      if (!plan || finalizing.current) return;
      const body = new Blob([JSON.stringify({ position_ms: currentPosition(), observation: ++observation.current })], { type: 'application/json' });
      navigator.sendBeacon(plan.heartbeat_url, body);
    };
    window.addEventListener('pagehide', pageHide);
    return () => {
      active = false;
      audioSwitchVersion.current += 1;
      sourceVersion.current += 1;
      window.clearInterval(timer);
      window.removeEventListener('pagehide', pageHide);
      hls.current?.destroy();
      hls.current = null;
      const plan = playback.current;
      if (plan && !finalizing.current) {
        void api.playbackHeartbeat(plan.session_id, currentPosition(), ++observation.current).catch(() => undefined).then(() => api.playbackStop(plan.session_id)).catch(() => undefined);
      }
    };
  }, [attach, catalogID, currentPosition, heartbeat, startPositionMS]);

  const seekTo = useCallback(async (target: number) => {
    const plan = playback.current;
    const element = video.current;
    if (!plan || !element) return;
    if (plan.plan.kind === 'direct') { element.currentTime = target / 1000; return; }
    const version = sourceVersion.current;
    try {
      const updated = await api.playbackSeek(plan.session_id, target, ++observation.current);
      if (version !== sourceVersion.current || !video.current) return;
      if (updated.media_url !== plan.media_url || updated.session_id !== plan.session_id) {
        autoStart.current = !element.paused;
        void attach(updated);
      } else {
        initializingPosition.current = true;
        element.currentTime = Math.max(0, target - updated.stream_offset_ms) / 1000;
      }
    } catch (error: unknown) {
      if (version === sourceVersion.current) dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'Flixr could not seek in this stream.' });
    }
  }, [attach]);
  useEffect(() => screenCoordinator.onCommand((command) => {
    if (command.type === 'pause') video.current?.pause();
    if (command.type === 'seek') void seekTo(command.position_ms);
  }), [seekTo]);

  const play = async () => {
    try {
      await video.current?.play();
      dispatch({ type: 'play' });
    } catch {
      dispatch({ type: 'error', message: 'Playback was blocked. Try play again.' });
    }
  };

  const finish = async () => {
    if (finalizing.current) return;
    finalizing.current = true;
    const plan = playback.current;
    if (plan) {
      try {
        // A failed progress write must not leave an FFmpeg session running.
        await api.playbackHeartbeat(plan.session_id, currentPosition(), ++observation.current).catch(() => undefined);
        await api.playbackStop(plan.session_id);
      } catch {
        // The durable progress write is best-effort during an explicit exit.
      }
    }
    onExit();
  };

  const seeked = async () => {
    const plan = playback.current;
    if (!plan) return;
    if (plan.plan.kind === 'direct') { await heartbeat(); return; }
    const target = currentPosition();
    try {
      const updated = await api.playbackSeek(plan.session_id, target, ++observation.current);
      if (updated.session_id !== plan.session_id || updated.media_url !== plan.media_url) void attach(updated);
    } catch (error: unknown) {
      dispatch({ type: 'error', message: error instanceof ApiError ? error.message : 'Flixr could not seek in this stream.' });
    }
  };

  const changeAudio = async (value: string) => {
    const plan = playback.current;
    const element = video.current;
    if (!plan || !element || switchingAudio) return;
    const [source, indexValue] = value.split(':', 2);
    const streamIndex = Number(indexValue);
    if ((source !== 'embedded' && source !== 'external') || !Number.isInteger(streamIndex) || streamIndex < 0) return;
    const version = sourceVersion.current;
    const switchVersion = ++audioSwitchVersion.current;
    const positionMs = currentPosition();
    autoStart.current = !element.paused;
    setSwitchingAudio(true);
    setTrackError(undefined);
    try {
      const updated = await api.playbackAudio(plan.session_id, streamIndex, source === 'external', positionMs, ++observation.current, browserCapabilities());
      if (version !== sourceVersion.current || !video.current) {
        void api.playbackStop(updated.session_id).catch(() => undefined);
        return;
      }
      await attach(updated);
    } catch (error: unknown) {
      if (version === sourceVersion.current) setTrackError(error instanceof ApiError ? error.message : 'Flixr could not change the audio track.');
    } finally {
      if (switchVersion === audioSwitchVersion.current) setSwitchingAudio(false);
    }
  };

  const statusLabel = state.status === 'idle' || state.status === 'loading' ? 'Preparing local playback…' : state.status === 'buffering' ? 'Buffering on your network…' : state.status === 'paused' ? 'Paused' : 'Playing on this device';

  return <main className="player">
    <header className="player-header">
      <button ref={backButton} className="player-back" onClick={() => { void finish(); }}>← Back to library</button>
      <div className="player-heading">
        <p className="eyebrow">YOUR LOCAL SCREEN</p>
        <h1 id="player-title">Now playing</h1>
      </div>
      <span className="player-local"><span aria-hidden="true" />LOCAL STREAM</span>
    </header>
    {state.status === 'error' ? <section className="state player-error" role="alert" aria-labelledby="player-title"><h2>Playback stopped</h2><p>{state.message}</p><button onClick={() => { void finish(); }}>Return to your library</button></section> :
      <section className="player-stage" aria-labelledby="player-title">
        <div className="player-frame">
          {(state.status === 'idle' || state.status === 'loading' || state.status === 'buffering') && <div className="player-loading" aria-hidden="true"><span className="button-spinner" /><span>{state.status === 'buffering' ? 'Buffering…' : 'Preparing your film…'}</span></div>}
          <video
            ref={video}
            controls
            playsInline
            onLoadedMetadata={() => {
              const plan = playback.current;
              if (!video.current || !plan) return;
              const resumeSeconds = Math.max(0, plan.resume_ms - plan.stream_offset_ms) / 1000;
              if (resumeSeconds > 0) initializingPosition.current = true;
              video.current.currentTime = resumeSeconds;
              if (autoStart.current) {
                autoStart.current = false;
                // Browser autoplay policy may require the receiver's local Play button.
                void video.current.play().catch(() => dispatch({ type: 'pause' }));
              }
            }}
            onCanPlay={() => dispatch({ type: video.current?.paused ? 'pause' : 'play' })}
            onError={() => dispatch({ type: 'error', message: 'This media could not be loaded. Check that the file is still available, then start the title again.' })}
            onEnded={() => { dispatch({ type: 'pause' }); void heartbeat(true); }}
            onPlaying={() => dispatch({ type: 'play' })}
            onPause={() => {
              dispatch({ type: 'pause' });
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
          />
        </div>
        <div className="player-toolbar">
          <p role="status" className="player-status"><span className={`player-status-dot ${state.status}`} aria-hidden="true" />{statusLabel}</p>
          {(playback.current?.audio_tracks?.length ?? 0) > 1 && <label className="player-audio">Audio track
            <select
              aria-busy={switchingAudio}
              disabled={switchingAudio}
              value={`${playback.current?.plan.audio_external ? 'external' : 'embedded'}:${playback.current?.plan.audio_stream_index ?? ''}`}
              onChange={(event) => { void changeAudio(event.target.value); }}
            >
              {playback.current?.audio_tracks?.map((track) => <option key={`${track.external ? 'external' : 'embedded'}:${track.index}`} value={`${track.external ? 'external' : 'embedded'}:${track.index}`}>{audioLabel(track)}</option>)}
            </select>
          </label>}
          <div className="player-actions">
            <button className="primary" onClick={() => void play()}>Play</button>
            <button onClick={() => video.current?.pause()}>Pause</button>
          </div>
        </div>
        {trackError && <p className="player-track-error" role="alert">{trackError}</p>}
        <p className="player-hint">Progress stays with this profile across your local devices.</p>
      </section>}
  </main>;
}
