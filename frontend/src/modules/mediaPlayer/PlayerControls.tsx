import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import type { QualityPreference } from './quality';

export type Chapter = { title: string; start_ms: number; end_ms: number };
export function playerTime(ms: number): string {
  const seconds = Math.max(0, Math.floor((Number.isFinite(ms) ? ms : 0) / 1000));
  const hours = Math.floor(seconds / 3600);
  return `${hours ? `${hours}:` : ''}${String(Math.floor(seconds / 60) % 60).padStart(hours ? 2 : 1, '0')}:${String(seconds % 60).padStart(2, '0')}`;
}
function Icon({ name }: { name: 'play' | 'pause' | 'volume' | 'muted' | 'fullscreen' | 'pip' }) {
  const paths = { play: 'M8 5v14l11-7z', pause: 'M7 5v14M17 5v14', volume: 'M3 9h4l5-4v14l-5-4H3zM16 8c3 2 3 6 0 8m3-11c5 4 5 10 0 14', muted: 'M3 9h4l5-4v14l-5-4H3zM16 9l6 6m0-6-6 6', fullscreen: 'M9 3H3v6m12-6h6v6M3 15v6h6m6 0h6v-6', pip: 'M3 4h18v16H3zM12 11h7v7h-7z' };
  return <svg width="24" height="24" viewBox="0 0 24 24" fill={name === 'play' ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={paths[name]} /></svg>;
}
function SeekIcon({ direction }: { direction: 'back' | 'forward' }) {
  return <svg viewBox="0 0 32 32" aria-hidden="true">
    <path className="player-seek-arrow" d="M9 10H4V5M5 10a12 12 0 1 1-1 10" transform={direction === 'forward' ? 'translate(32 0) scale(-1 1)' : undefined} />
    <text x="16" y="20">15</text>
  </svg>;
}

type Props = {
  playing: boolean; durationMS: number; positionMS: number; disabled?: boolean; seeking?: boolean;
  onSeek: (ms: number) => void; onToggle: () => void; onPrevious?: () => void; onNext?: () => void;
  video: RefObject<HTMLVideoElement | null>; stage: RefObject<HTMLElement | null>;
  chapters?: Chapter[]; sessionID?: string; playbackRate?: number; onPlaybackRateChange?: (rate: number) => void; children?: ReactNode;
  quality?: QualityPreference; effectiveQuality?: string; onQualityChange?: (quality: QualityPreference) => void;
};
export function PlayerControls({ playing, durationMS, positionMS, disabled, seeking = false, onSeek, onToggle, onPrevious, onNext, video, stage, chapters = [], sessionID, playbackRate = 1, onPlaybackRateChange, quality, effectiveQuality, onQualityChange, children }: Props) {
  const [scrub, setScrub] = useState<number>();
  const [hover, setHover] = useState<number>();
  const [preview, setPreview] = useState<{ key: string; url: string }>();
  const [volume, setVolume] = useState(1);
  const [muted, setMuted] = useState(false);
  const [speed, setSpeed] = useState(playbackRate);
  const [full, setFull] = useState(false);
  const [fill, setFill] = useState(false);
  const [notice, setNotice] = useState('');
  const [remaining, setRemaining] = useState(false);
  const dragging = useRef(false);
  const pendingSeek = useRef<number | undefined>(undefined);
  const max = Number.isFinite(durationMS) ? Math.max(0, durationMS) : 0;
  const bound = (ms: number) => Math.max(0, Math.min(Math.max(0, max - 1), ms));
  const seekAnchor = useRef(positionMS);
  useEffect(() => { if (!seeking) seekAnchor.current = positionMS; }, [positionMS, seeking]);
  useEffect(() => setSpeed(playbackRate), [playbackRate]);
  const seek = useCallback((target: number) => {
    const bounded = Math.max(0, Math.min(Math.max(0, max - 1), target));
    seekAnchor.current = bounded;
    onSeek(bounded);
  }, [max, onSeek]);
  const stepSeek = useCallback((delta: number) => seek(seekAnchor.current + delta), [seek]);
  const previewMS = scrub ?? hover;
  const previewAt = previewMS === undefined ? undefined : Math.floor(bound(previewMS) / 10000) * 10000;
  const previewKey = sessionID && previewAt !== undefined ? `${sessionID}:${previewAt}` : '';
  useEffect(() => {
    if (!previewKey || !sessionID || previewAt === undefined) return;
    const controller = new AbortController(); let objectURL: string | undefined;
    const timer = window.setTimeout(() => {
      void fetch(`/api/v1/playback/sessions/${encodeURIComponent(sessionID)}/preview.jpg?position_ms=${previewAt}`, { signal: controller.signal, credentials: 'same-origin' })
        .then(async (response) => {
          if (!response.ok) return;
          const blob = await response.blob();
          if (controller.signal.aborted || !blob.type.startsWith('image/')) return;
          objectURL = URL.createObjectURL(blob); setPreview({ key: previewKey, url: objectURL });
        }).catch(() => undefined);
    }, 180);
    return () => { window.clearTimeout(timer); controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [previewKey, previewAt, sessionID]);
  useEffect(() => {
    const element = video.current;
    const sync = () => { if (element) { setVolume(element.volume); setMuted(element.muted); } };
    const fullscreen = () => setFull(document.fullscreenElement === stage.current);
    element?.addEventListener('volumechange', sync); element?.addEventListener('ratechange', sync);
    document.addEventListener('fullscreenchange', fullscreen);
    return () => { element?.removeEventListener('volumechange', sync); element?.removeEventListener('ratechange', sync); document.removeEventListener('fullscreenchange', fullscreen); };
  }, [video, stage]);
  const fullscreen = async () => {
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else if (stage.current?.requestFullscreen) await stage.current.requestFullscreen();
      else {
        const native = video.current as (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null;
        if (native?.webkitEnterFullscreen) native.webkitEnterFullscreen();
        else setNotice('Fullscreen is not available in this browser.');
      }
    } catch { setNotice('Fullscreen could not start. You can keep watching here.'); }
  };
  const toggleMute = () => { if (video.current) { video.current.muted = !video.current.muted; setMuted(video.current.muted); } };
  useEffect(() => {
    const root = stage.current;
    const key = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      if (event.key === 'Escape') { const menu = root?.querySelector<HTMLDetailsElement>('details[open]'); if (menu) { menu.open = false; menu.querySelector('summary')?.focus(); event.preventDefault(); } return; }
      if (target.closest('button,input,select,textarea,summary,[contenteditable="true"]') || event.altKey || event.ctrlKey || event.metaKey) return;
      if (disabled) return;
      switch (event.key.toLowerCase()) {
        case ' ': case 'k': event.preventDefault(); if (!event.repeat) onToggle(); break;
        case 'arrowleft': event.preventDefault(); if (!event.repeat) stepSeek(-15000); break;
        case 'arrowright': event.preventDefault(); if (!event.repeat) stepSeek(15000); break;
        case 'm': event.preventDefault(); if (!event.repeat && video.current) video.current.muted = !video.current.muted; break;
        case 'f': event.preventDefault(); if (!event.repeat) root?.querySelector<HTMLButtonElement>('[data-fullscreen]')?.click(); break;
        case 'arrowup': case 'arrowdown': event.preventDefault(); if (video.current) video.current.volume = Math.max(0, Math.min(1, video.current.volume + (event.key === 'ArrowUp' ? .05 : -.05))); break;
      }
    };
    root?.addEventListener('keydown', key);
    return () => root?.removeEventListener('keydown', key);
  }, [stage, video, onToggle, stepSeek, disabled]);
  const commit = () => { dragging.current = false; if (pendingSeek.current !== undefined) seek(pendingSeek.current); pendingSeek.current = undefined; setScrub(undefined); };
  const chapter = chapters.find((item) => (previewMS ?? positionMS) >= item.start_ms && (previewMS ?? positionMS) < item.end_ms);
  return <div className="player-controls">
    <div className="player-timeline" onPointerLeave={() => { if (!dragging.current) setHover(undefined); }}>
      {previewMS !== undefined && <div className="player-seek-preview" style={{ left: `${max ? Math.max(8, Math.min(92, previewMS / max * 100)) : 50}%` }}>
        {preview?.key === previewKey && <img src={preview.url} alt="" />}
        <span>{playerTime(previewMS)}{chapter ? ` · ${chapter.title}` : ''}</span>
      </div>}
      <div className="player-chapter-markers" aria-hidden="true">{chapters.filter((c) => c.start_ms > 0 && c.start_ms < max).map((c) => <i key={c.start_ms} style={{ left: `${c.start_ms / max * 100}%` }} />)}</div>
      <input aria-label="Seek" aria-busy={seeking} aria-valuetext={`${playerTime(scrub ?? positionMS)} of ${playerTime(max)}`} type="range" min="0" max={max} step="1000" disabled={disabled || max <= 0} value={scrub ?? Math.min(positionMS, max)}
        onPointerDown={(event) => { dragging.current = true; event.currentTarget.setPointerCapture?.(event.pointerId); }}
        onPointerMove={(event) => { const rect = event.currentTarget.getBoundingClientRect(); if (rect.width) setHover(bound((event.clientX - rect.left) / rect.width * max)); }}
        onChange={(event) => { const target = Number(event.target.value); setScrub(target); setHover(target); pendingSeek.current = target; if (!dragging.current) { seek(target); pendingSeek.current = undefined; setScrub(undefined); } }}
        onPointerUp={commit} onPointerCancel={() => { dragging.current = false; pendingSeek.current = undefined; setScrub(undefined); setHover(undefined); }} onBlur={() => { if (dragging.current) commit(); setHover(undefined); }} />
    </div>
    <div className="player-control-row">
      <button aria-label={playing ? 'Pause' : 'Play'} title={playing ? 'Pause (Space)' : 'Play (Space)'} disabled={disabled} onClick={onToggle}><Icon name={playing ? 'pause' : 'play'} /></button>
      <button className="player-seek-step" aria-label="Back 15 seconds" title="Back 15 seconds (←)" disabled={disabled || !max} onClick={() => stepSeek(-15000)}><SeekIcon direction="back" /></button>
      <button className="player-seek-step" aria-label="Forward 15 seconds" title="Forward 15 seconds (→)" disabled={disabled || !max} onClick={() => stepSeek(15000)}><SeekIcon direction="forward" /></button>
      <button className="player-clock" aria-label={remaining ? "Show elapsed time" : "Show remaining time"} onClick={() => setRemaining(!remaining)}>{remaining ? `−${playerTime(max - (scrub ?? positionMS))}` : playerTime(scrub ?? positionMS)} <span>/ {playerTime(max)}</span></button>
      <div className="player-volume"><button aria-label={muted ? 'Unmute' : 'Mute'} title="Mute (M)" onClick={toggleMute}><Icon name={muted ? 'muted' : 'volume'} /></button><input aria-label="Volume" type="range" min="0" max="1" step="0.05" value={muted ? 0 : volume} onChange={(event) => { const value = Number(event.target.value); if (video.current) { video.current.volume = value; video.current.muted = false; } setVolume(value); setMuted(false); }} /></div>
      <div className="player-control-spacer" />
      {onPrevious && <button aria-label="Previous episode" title="Previous episode" disabled={disabled} onClick={onPrevious}><svg width="24" height="24" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M5 5h2v14H5zm14 0v14L8 12z" /></svg></button>}
      {onNext && <button aria-label="Next episode" title="Next episode" disabled={disabled} onClick={onNext}><svg width="24" height="24" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M17 5h2v14h-2zM5 5v14l11-7z" /></svg></button>}

      {chapters.length > 0 && <details className="player-menu"><summary>Chapters</summary><div className="player-menu-panel">{chapters.map((c) => <button key={c.start_ms} disabled={disabled} onClick={(event) => { seek(c.start_ms); const details = event.currentTarget.closest('details'); if (details) { details.open = false; details.querySelector('summary')?.focus(); } }}><span>{c.title}</span><small>{playerTime(c.start_ms)}</small></button>)}</div></details>}
      <details className="player-menu"><summary aria-label="Playback settings">Settings</summary><div className="player-menu-panel">
        {children}
        {quality && <label>Quality<select aria-label="Streaming quality" value={quality} onChange={(event) => onQualityChange?.(event.target.value as QualityPreference)}><option value="auto">Auto</option><option value="data_saver">Data saver</option><option value="original">Original</option></select><small>{quality === 'auto' ? 'Adjusts after sustained buffering.' : quality === 'data_saver' ? 'Uses less mobile data.' : 'Uses the source quality.'}</small>{effectiveQuality && <small>Actual: {effectiveQuality}</small>}</label>}
        <label>Speed<select aria-label="Playback speed" value={speed} onChange={(event) => { const value = Number(event.target.value); if (video.current) video.current.playbackRate = value; setSpeed(value); onPlaybackRateChange?.(value); }}>{[.5,.75,1,1.25,1.5,1.75,2].map((value) => <option key={value} value={value}>{value === 1 ? 'Normal' : `${value}×`}</option>)}</select></label>
        <button aria-pressed={fill} onClick={() => { if (video.current) video.current.style.objectFit = fill ? 'contain' : 'cover'; setFill(!fill); }}>{fill ? 'Fit picture' : 'Fill screen'}</button>
      </div></details>
      {document.pictureInPictureEnabled && <button aria-label="Picture in picture" onClick={() => { void (document.pictureInPictureElement ? document.exitPictureInPicture() : video.current?.requestPictureInPicture())?.catch(() => setNotice('Picture in picture is unavailable for this video.')); }}><Icon name="pip" /></button>}
      <button data-fullscreen aria-label={full ? 'Exit fullscreen' : 'Fullscreen'} title="Fullscreen (F)" onClick={() => { void fullscreen(); }}><Icon name="fullscreen" /></button>
    </div>
    {notice && <p className="player-control-notice" role="status">{notice}</p>}
  </div>;
}
