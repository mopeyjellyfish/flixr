import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type PlaybackActivityPage, type PlaybackActivitySession, type PlaybackSettings, type PlaybackStatus } from '../../core/api';
import type { ScreenPresence } from '../../core/screens';
import { Disclosure } from '../../modules/ui/Disclosure';
import { PendingForm } from './Libraries';

const GIB = 1024 ** 3;
const toGiB = (bytes: number) => Math.round((bytes / GIB) * 100) / 100;
const fromGiB = (value: string) => Math.max(1, Math.round(Number(value) * GIB));

export function PlaybackPanel({ settings, status, onChange, onSubmit, locked }: { settings: PlaybackSettings; status?: PlaybackStatus; onChange: (settings: PlaybackSettings) => void; onSubmit: (event: FormEvent) => void; locked: Set<string> }) {
  const active = status?.generations?.length ?? 0;
  return <Disclosure summary="Playback resource limits" detail={active ? `${active} active generation${active === 1 ? '' : 's'}` : `${toGiB(settings.global_bytes)} GiB cache · ${settings.max_generations} concurrent`}>
    <PendingForm onSubmit={onSubmit}>
      <p className="field-hint">{active ? `${active} compatibility generation${active === 1 ? '' : 's'} active. Limits apply once they finish.` : 'No compatibility generations are active.'}{locked.size > 0 ? ' Environment-managed fields are read-only.' : ''}</p>
      <label>Segment directory<input disabled={locked.has('playback.segment_dir')} value={settings.segment_dir} onChange={(event) => onChange({ ...settings, segment_dir: event.target.value })} /></label>
      <div className="field-row">
        <label>Per-generation cache (GiB)<input disabled={locked.has('playback.generation_bytes')} type="number" min="0.01" step="0.01" inputMode="decimal" value={toGiB(settings.generation_bytes)} onChange={(event) => onChange({ ...settings, generation_bytes: fromGiB(event.target.value) })} /></label>
        <label>Total cache (GiB)<input disabled={locked.has('playback.global_bytes')} type="number" min="0.01" step="0.01" inputMode="decimal" value={toGiB(settings.global_bytes)} onChange={(event) => onChange({ ...settings, global_bytes: fromGiB(event.target.value) })} /></label>
        <label>Concurrent generations<input disabled={locked.has('playback.max_generations')} type="number" min="1" step="1" inputMode="numeric" value={settings.max_generations} onChange={(event) => onChange({ ...settings, max_generations: Math.max(1, Math.round(Number(event.target.value))) })} /></label>
      </div>
      <div className="actions"><button className="primary">Save playback limits</button></div>
    </PendingForm>
  </Disclosure>;
}

const screenStateLabel: Record<ScreenPresence['state'], string> = { available: 'Available', playing: 'Playing', paused: 'Paused' };

export function ConnectedScreens({ screens }: { screens: ScreenPresence[] }) {
  return <div className="owner-panel">
    <div className="panel-heading"><div><h3>Connected screens</h3><p>Flixr screens on this network that can take a handoff.</p></div></div>
    {screens.length
      ? <ul className="screen-list">{screens.map((screen) => <li key={screen.id} className="owner-card screen-card"><span className={`status-dot ${screen.state === 'available' || screen.state === 'playing' ? 'is-healthy' : ''}`} aria-hidden="true" /><strong>{screen.name}</strong><span className="muted">{screenStateLabel[screen.state] ?? screen.state}</span></li>)}</ul>
      : <p className="muted">No Flixr screens are connected.</p>}
  </div>;
}

const formatBytes = (bytes: number) => bytes >= GIB ? `${toGiB(bytes)} GiB` : `${Math.round(bytes / (1024 ** 2))} MiB`;

type ConfirmStop = (options: { title: string; message: string; confirmLabel: string; danger?: boolean }) => Promise<boolean>;

export function PlaybackActivity({ confirm, refreshKey = 0, onOwnerRequired }: { confirm: ConfirmStop; refreshKey?: number; onOwnerRequired?: () => void }) {
  const [page, setPage] = useState<PlaybackActivityPage>();
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [stopping, setStopping] = useState('');
  const [error, setError] = useState('');
  const requestSequence = useRef(0);
  const fullRefreshPending = useRef(false);
  const heading = useRef<HTMLHeadingElement>(null);

  const load = useCallback(async (cursor = '', append = false) => {
    if (append && fullRefreshPending.current) return;
    const sequence = ++requestSequence.current;
    if (append) setLoadingMore(true); else { fullRefreshPending.current = true; setLoading(true); }
    setError('');
    try {
      const result = await api.playbackActivity(cursor);
      if (sequence !== requestSequence.current) return;
      const normalized = { ...result, sessions: result.sessions ?? [] };
      setPage((current) => append && current ? { ...normalized, sessions: [...current.sessions, ...normalized.sessions] } : normalized);
    } catch (caught) {
      if (sequence !== requestSequence.current) return;
      if (caught instanceof ApiError && caught.code === 'owner_required') onOwnerRequired?.();
      else setError(append ? 'More playback activity could not be loaded. Try again.' : 'Playback activity is unavailable. Try refreshing.');
    } finally {
      if (sequence === requestSequence.current) { if (!append) fullRefreshPending.current = false; setLoading(false); setLoadingMore(false); }
    }
  }, [onOwnerRequired]);

  useEffect(() => { void load(); }, [load, refreshKey]);

  const stop = async (session: PlaybackActivitySession) => {
    const approved = await confirm({ title: `Stop ${session.title}?`, message: 'This stops only this playback session. Other active playback continues.', confirmLabel: 'Stop playback', danger: true });
    if (!approved) return;
    setStopping(session.owner_handle);
    setError('');
    try {
      await api.stopOwnerPlaybackSession(session.owner_handle);
      await load();
      heading.current?.focus();
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'playback_session_invalid') {
        setError('That playback session already ended. Refresh activity to see the current sessions.');
      } else if (caught instanceof ApiError && caught.code === 'owner_required') onOwnerRequired?.();
      else setError('This playback session could not be stopped. Try again.');
    } finally { setStopping(''); }
  };

  const capacity = page?.capacity;
  return <div className="owner-panel playback-activity" aria-labelledby="playback-activity-title">
    <div className="panel-heading"><div><h3 id="playback-activity-title" ref={heading} tabIndex={-1}>Active playback</h3><p>Private session details and compatibility capacity for this server.</p></div><button type="button" className="quiet-button" disabled={loading || Boolean(stopping)} onClick={() => void load()}>Refresh playback activity</button></div>
    {capacity && <div className="playback-capacity" aria-label="Playback capacity">
      <strong>{capacity.active_sessions} active session{capacity.active_sessions === 1 ? '' : 's'}</strong>
      <span>{capacity.generations?.length ?? 0} of {capacity.max_generations} compatibility generations</span>
      <span>{capacity.starting} starting</span>
      <span>{formatBytes(capacity.cache_bytes)} of {formatBytes(capacity.global_bytes)} total cache</span>
      <span>{formatBytes(capacity.generation_bytes)} per generation</span>
    </div>}
    {capacity?.generations?.length ? <ul className="playback-generation-usage" aria-label="Compatibility generation cache usage">{capacity.generations.map((generation, index) => <li key={generation.id}>Generation {index + 1}: {formatBytes(generation.bytes)} used · {generation.leases} session{generation.leases === 1 ? '' : 's'} · {generation.running ? 'running' : 'complete'}</li>)}</ul> : null}
    {error && <p role="alert" className="inline-error">{error}</p>}
    {loading && !page ? <p className="muted">Loading playback activity…</p> : page?.sessions.length
      ? <ul className="card-list playback-session-list" aria-label="Active playback sessions">{page.sessions.map((session) => <li className="owner-card" key={session.owner_handle}>
        <div className="card-head"><div><strong>{session.title || 'Unknown title'}</strong><p className="muted">{session.device || 'Unknown device'} · <time dateTime={new Date(session.started_at * 1000).toISOString()}>{new Date(session.started_at * 1000).toLocaleString()}</time></p></div><span className="status-badge">{session.kind}</span></div>
        <p>{session.reason}</p>
        <p className="muted">{session.audio_stream_index === undefined ? 'Default audio' : `Audio stream ${session.audio_stream_index}${session.audio_external ? ' (external)' : ''}`} · {session.subtitle_stream_index === undefined ? 'Subtitles off' : `Subtitle stream ${session.subtitle_stream_index}${session.subtitle_external ? ' (external)' : ''}`} · {session.width && session.height ? `${session.width}×${session.height}` : 'Original dimensions'} · {session.quality_mode === 'data_saver' ? 'Data saver' : session.quality_mode === 'auto' ? 'Automatic quality' : 'Original quality'}</p>
        <div className="actions"><button type="button" className="quiet-button" disabled={stopping === session.owner_handle} onClick={() => void stop(session)}>{stopping === session.owner_handle ? 'Stopping…' : `Stop ${session.title || 'playback'}`}</button></div>
      </li>)}</ul>
      : <p className="muted">No playback sessions are active.</p>}
    {page?.next_cursor && <div className="actions"><button type="button" className="quiet-button" disabled={loading || loadingMore || Boolean(stopping)} onClick={() => void load(page.next_cursor, true)}>{loadingMore ? 'Loading…' : 'Load more'}</button></div>}
  </div>;
}
