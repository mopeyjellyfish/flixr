import type { FormEvent } from 'react';
import type { PlaybackSettings, PlaybackStatus } from '../../core/api';
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
