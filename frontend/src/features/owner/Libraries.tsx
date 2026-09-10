import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { LoadingButton, useAsyncAction } from '../../vendor/interior/loading-button';
import { ProgressBar } from '../../vendor/interior/progress-bar';
import type { Library, LibraryLocation, Readiness, Scan, ScanJob, ScanPolicy } from '../../core/api';
import { Readiness as ReadinessPanel } from '../setup/Setup';
import { Disclosure } from '../../modules/ui/Disclosure';
import { HoldButton } from '../../modules/ui/HoldButton';

export type LibraryActions = {
  onCreate: (name: string, kind: Library['kind']) => Promise<void>;
  onRename: (library: Library, name: string) => Promise<void>;
  onDelete: (library: Library) => Promise<void>;
  onAddLocation: (library: Library, path: string) => Promise<void>;
  onChangeLocation: (location: LibraryLocation, path: string) => Promise<void>;
  onConfirmPending: (location: LibraryLocation) => Promise<void>;
  onCancelPending: (location: LibraryLocation) => Promise<void>;
  onSavePolicy: (policy: ScanPolicy) => Promise<void>;
  onRunScan: (libraryID: string) => Promise<void>;
};

export type ScanActions = {
  onStart: () => Promise<void>;
  onConfirmRemovals: (location: LibraryLocation) => Promise<void>;
  onCancelJob: (id: string) => Promise<void>;
  onRetry: (job: ScanJob, files?: ScanJob['files']) => Promise<void>;
};

type LibrariesSectionProps = {
  readiness?: Readiness;
  onRecheck: () => Promise<void>;
  libraries: Library[];
  locked: Set<string>;
  policies: Record<string, ScanPolicy>;
  scheduleLocked: boolean;
  scan?: Scan;
  locations: LibraryLocation[];
  jobs: ScanJob[];
  actions: LibraryActions;
  scanActions: ScanActions;
};

export function LibrariesSection({ readiness, onRecheck, libraries, locked, policies, scheduleLocked, scan, locations, jobs, actions, scanActions }: LibrariesSectionProps) {
  return <>
    {readiness && <div className="readiness-row"><ReadinessPanel readiness={readiness} /><LoadingButton className="quiet-button" onAction={onRecheck} pendingLabel="Checking…" successLabel="Recheck readiness">Recheck readiness</LoadingButton></div>}
    <div className="panel-heading"><div><h3>Your libraries</h3><p>Group folders from local disks and mounted shares. Overlapping folders are rejected.</p></div></div>
    {libraries.length === 0 && <div className="empty-state"><p className="eyebrow">NO LIBRARIES YET</p><h4>Create the first library</h4><p>A library groups folders of the same kind. Add one for films and one for TV to get started.</p></div>}
    <div className="stack">{libraries.map((library) => <LibraryCard key={library.id} library={library} locked={locked} policy={policies[library.id]} scheduleLocked={scheduleLocked} actions={actions} />)}</div>
    <CreateLibrary onCreate={actions.onCreate} />
    <ScanActivity scan={scan} locations={locations} libraries={libraries} jobs={jobs} actions={scanActions} />
  </>;
}

const kindLabel = (kind: Library['kind']) => kind === 'film' ? 'Films' : 'TV';

function LibraryCard({ library, locked, policy, scheduleLocked, actions }: { library: Library; locked: Set<string>; policy?: ScanPolicy; scheduleLocked: boolean; actions: LibraryActions }) {
  const [renaming, setRenaming] = useState(false);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState(library.name);
  const [path, setPath] = useState('');
  const files = library.locations.reduce((total, location) => total + (location.state === 'available' ? location.items : 0), 0);
  const locationLocked = (location: LibraryLocation) => (location.id === 'films-root' && locked.has('library.films_root')) || (location.id === 'tv-root' && locked.has('library.tv_root'));
  return <article className="owner-card library-card" aria-labelledby={`library-${library.id}-title`}>
    <header className="card-head">
      <div className="card-title"><h3 id={`library-${library.id}-title`}>{library.name}</h3><span className="pill">{kindLabel(library.kind)}</span><span className="muted">{library.locations.length === 1 ? '1 folder' : `${library.locations.length} folders`} · {files} files</span></div>
      <div className="card-tools">
        {!renaming && <button type="button" className="quiet-button" onClick={() => { setName(library.name); setRenaming(true); }}>Rename</button>}
        {library.locations.length === 0 && <HoldButton className="danger" onConfirm={() => void actions.onDelete(library)} confirmedLabel="Deleting…">Delete library</HoldButton>}
      </div>
    </header>
    {renaming && <PendingForm className="inline-form" onSubmit={async (event) => { event.preventDefault(); const next = name.trim(); if (next && next !== library.name) await actions.onRename(library, next); setRenaming(false); }}>
      <label>Library name<input value={name} autoFocus onChange={(event) => setName(event.target.value)} /></label>
      <div className="actions"><button type="button" className="quiet-button" onClick={() => setRenaming(false)}>Cancel</button><button className="primary">Save name</button></div>
    </PendingForm>}
    {library.locations.length > 0 && <ul className="folder-list">{library.locations.map((location) => <FolderRow key={location.id} location={location} managed={locationLocked(location)} actions={actions} />)}</ul>}
    {adding
      ? <PendingForm className="inline-form" onSubmit={async (event) => { event.preventDefault(); const next = path.trim(); if (!next) return; await actions.onAddLocation(library, next); setPath(''); setAdding(false); }}>
        <label>Add folder to {library.name}<input value={path} autoFocus placeholder="/media/library" onChange={(event) => setPath(event.target.value)} /></label>
        <div className="actions"><button type="button" className="quiet-button" onClick={() => { setAdding(false); setPath(''); }}>Cancel</button><button className="primary">Add folder</button></div>
      </PendingForm>
      : <button type="button" className="quiet-button card-add" onClick={() => setAdding(true)}>+ Add folder</button>}
    <ScheduleDisclosure library={library} policy={policy} locked={scheduleLocked} onSave={actions.onSavePolicy} onRun={actions.onRunScan} />
  </article>;
}

const stateLabel = (location: LibraryLocation) => {
  if (location.state === 'available') return location.scan_complete ? `${location.items} files` : 'Scan incomplete';
  if (location.state === 'unavailable') return 'Unavailable';
  if (location.state === 'review_required') return `${location.missing} missing · needs review`;
  if (location.state === 'topology_review') return 'Needs review';
  return 'Not scanned yet';
};

function FolderRow({ location, managed, actions }: { location: LibraryLocation; managed: boolean; actions: LibraryActions }) {
  const [moving, setMoving] = useState(false);
  const [path, setPath] = useState(location.path ?? '');
  const attention = location.state === 'unavailable' || location.state === 'review_required' || location.state === 'topology_review';
  return <li className={`folder-row ${attention ? 'needs-attention' : ''}`}>
    <div className="folder-main">
      <code>{location.path}</code>
      <span className={`pill ${attention ? 'is-warning' : ''}`}>{stateLabel(location)}</span>
      {location.message && <p className="field-hint">{location.message}</p>}
      {location.pending_change_id && <p className="field-hint pending-change">{location.pending_change_origin === 'environment' ? 'The server environment requested' : 'Pending change:'} {location.pending_path ? <>a move to <code>{location.pending_path}</code></> : 'removal of this folder'}.</p>}
    </div>
    {moving
      ? <PendingForm className="inline-form folder-move" onSubmit={async (event) => { event.preventDefault(); const next = path.trim(); if (next && next !== location.path) await actions.onChangeLocation(location, next); setMoving(false); }}>
        <label>New folder path<input value={path} autoFocus onChange={(event) => setPath(event.target.value)} /></label>
        <div className="actions"><button type="button" className="quiet-button" onClick={() => setMoving(false)}>Cancel</button><button className="primary">Move</button></div>
      </PendingForm>
      : <div className="folder-actions">
        {location.pending_change_id && <><button type="button" className="primary" onClick={() => void actions.onConfirmPending(location)}>Review and apply</button><button type="button" className="quiet-button" onClick={() => void actions.onCancelPending(location)}>Discard</button></>}
        {managed
          ? <span className="pill is-locked">Managed by environment</span>
          : <><button type="button" className="quiet-button" onClick={() => { setPath(location.path ?? ''); setMoving(true); }}>Move folder</button><button type="button" className="quiet-button" onClick={() => void actions.onChangeLocation(location, '')}>Remove folder</button></>}
      </div>}
  </li>;
}

function CreateLibrary({ onCreate }: { onCreate: LibraryActions['onCreate'] }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [kind, setKind] = useState<Library['kind']>('film');
  if (!open) return <button type="button" className="quiet-button card-add" onClick={() => setOpen(true)}>+ Create library</button>;
  return <PendingForm className="owner-card inline-form" onSubmit={async (event) => { event.preventDefault(); if (!name.trim()) return; await onCreate(name, kind); setName(''); setOpen(false); }}>
    <h3>Create library</h3>
    <div className="field-row">
      <label>Name<input value={name} autoFocus onChange={(event) => setName(event.target.value)} placeholder="Kids films" /></label>
      <label>Media type<select value={kind} onChange={(event) => setKind(event.target.value as Library['kind'])}><option value="film">Films</option><option value="episode">TV episodes</option></select></label>
    </div>
    <div className="actions"><button type="button" className="quiet-button" onClick={() => setOpen(false)}>Cancel</button><button className="primary">Create library</button></div>
  </PendingForm>;
}

const scheduleSummary = (policy?: ScanPolicy) => {
  if (!policy) return 'Loading schedule…';
  if (!policy.enabled) return 'Not scheduled';
  const cadence = policy.schedule_kind === 'interval' ? `Every ${Math.max(1, Math.round(policy.interval_seconds / 3600))} h` : `Daily at ${policy.local_time}`;
  return policy.next_run_at ? `${cadence} · next ${new Date(policy.next_run_at * 1000).toLocaleString()}` : cadence;
};

function ScheduleDisclosure({ library, policy, locked, onSave, onRun }: { library: Library; policy?: ScanPolicy; locked: boolean; onSave: (policy: ScanPolicy) => Promise<void>; onRun: (libraryID: string) => Promise<void> }) {
  const [draft, setDraft] = useState<ScanPolicy>();
  useEffect(() => { if (policy) setDraft(policy); }, [policy]);
  return <Disclosure className="schedule" summary="Scan schedule" detail={scheduleSummary(policy)}>
    {!draft ? <p className="muted">Loading schedule…</p> : <form className="stack" onSubmit={(event) => { event.preventDefault(); void onSave(draft); }}>
      {locked && <p className="field-hint">Schedule timing is managed by FLIXR_SCAN_SCHEDULE. Excluded paths remain editable.</p>}
      <label className="choice"><input type="checkbox" checked={draft.enabled} disabled={locked} onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })} /> Enable scheduled scans</label>
      <div className="field-row">
        <label>Frequency<select value={draft.schedule_kind} disabled={locked} onChange={(event) => setDraft({ ...draft, schedule_kind: event.target.value as ScanPolicy['schedule_kind'] })}><option value="interval">Interval</option><option value="daily">Daily</option></select></label>
        {draft.schedule_kind === 'interval'
          ? <label>Every (hours)<input type="number" min="1" max="8760" inputMode="numeric" value={Math.max(1, Math.round(draft.interval_seconds / 3600))} disabled={locked} onChange={(event) => setDraft({ ...draft, interval_seconds: Number(event.target.value) * 3600 })} /></label>
          : <><label>Local time<input type="time" value={draft.local_time} disabled={locked} onChange={(event) => setDraft({ ...draft, local_time: event.target.value })} /></label><label>Timezone<input value={draft.timezone} disabled={locked} onChange={(event) => setDraft({ ...draft, timezone: event.target.value })} /></label></>}
      </div>
      <label>Excluded paths<textarea rows={3} value={draft.exclusions.join('\n')} placeholder={'Extras/**\n**/Samples/**'} onChange={(event) => setDraft({ ...draft, exclusions: event.target.value.split('\n').map((value) => value.trim()).filter(Boolean) })} /></label>
      <p className="field-hint">Daily scans use the chosen timezone; daylight-saving gaps run at the first valid local minute. Last successful scan: {draft.last_success_at ? <Timestamp label="" value={draft.last_success_at} /> : 'never'}.</p>
      <div className="actions"><button type="button" className="quiet-button" aria-label={`Run now for ${library.name}`} onClick={() => void onRun(library.id)}>Run now</button><button type="submit" className="primary" aria-label={`${locked ? 'Save exclusions' : 'Save schedule'} for ${library.name}`}>{locked ? 'Save exclusions' : 'Save schedule'}</button></div>
    </form>}
  </Disclosure>;
}

const scanStatusLine = (scan?: Scan) => scan ? `${scan.status}: ${scan.scanned} scanned, ${scan.unmatched} unmatched, ${scan.failed} failed.` : 'No scan has started.';

function ScanActivity({ scan, locations, libraries, jobs, actions }: { scan?: Scan; locations: LibraryLocation[]; libraries: Library[]; jobs: ScanJob[]; actions: ScanActions }) {
  const [showAll, setShowAll] = useState(false);
  const running = scan?.status === 'running';
  const review = locations.filter((location) => location.state === 'review_required');
  const visible = showAll ? jobs : jobs.slice(0, 4);
  return <section className="owner-panel" aria-labelledby="scan-activity-title">
    <div className="panel-heading"><div><h3 id="scan-activity-title">Scan activity</h3><p>{scanStatusLine(scan)}</p></div><LoadingButton onAction={actions.onStart} disabled={running} pendingLabel="Starting…" successLabel="Scan all libraries">{running ? 'Scan in progress' : 'Scan all libraries'}</LoadingButton></div>
    {scan?.message && <p role="alert" className="form-error">{scan.message}</p>}
    {running && <ProgressBar value={null} label="Library scan" pendingLabel="Scanning your library…" />}
    {review.map((location) => <div key={location.id} className="inline-alert review-alert"><p><strong>{location.library_name ?? kindLabel(location.root_kind)}:</strong> {location.missing} missing files need owner review. Titles stay in history and lists until you confirm.</p><button type="button" onClick={() => void actions.onConfirmRemovals(location)}>Confirm removal</button></div>)}
    {jobs.length === 0 ? <p className="muted">No scheduled or manual scan jobs yet.</p> : <div className="stack">{visible.map((job) => <ScanJobCard key={job.id} job={job} library={libraries.find((library) => library.id === job.library_id)?.name ?? 'Library'} actions={actions} />)}</div>}
    {jobs.length > 4 && <button type="button" className="link-button" onClick={() => setShowAll((value) => !value)}>{showAll ? 'Show fewer' : `Show all ${jobs.length} jobs`}</button>}
  </section>;
}

function ScanJobCard({ job, library, actions }: { job: ScanJob; library: string; actions: ScanActions }) {
  const active = job.status === 'queued' || job.status === 'running';
  const retryableFiles = (job.files ?? []).filter((file) => file.retryable);
  const retryable = retryableFiles.length > 0 || job.status === 'failed' || job.status === 'interrupted';
  return <article className="owner-card job-card">
    <div className="card-head">
      <div className="card-title"><strong>{library} · {job.status}</strong><span className="muted">{job.trigger} · {job.scanned} scanned · {job.skipped} unchanged · {job.failed} failed{job.total !== undefined ? ` · ${job.total} total` : ''}{job.started_at ? ` · ${formatElapsed(job.started_at, job.finished_at)}` : ''}</span></div>
      <div className="card-tools">{active && <button type="button" className="quiet-button" onClick={() => void actions.onCancelJob(job.id)}>Cancel</button>}{retryable && <button type="button" className="quiet-button" onClick={() => void actions.onRetry(job, retryableFiles)}>Retry failed files</button>}</div>
    </div>
    <p className="muted job-times"><Timestamp label="Queued" value={job.queued_at} />{job.started_at && <> · <Timestamp label="Started" value={job.started_at} /></>}{job.finished_at && <> · <Timestamp label="Finished" value={job.finished_at} /></>}</p>
    {job.message && <p role="alert" className="form-error">{job.message}</p>}
    {job.status === 'running' && <ProgressBar value={null} label="Library scan" pendingLabel="Scanning…" />}
    {job.files && job.files.length > 0 && <ul className="job-files">{job.files.map((file) => <li key={`${file.location_id}:${file.relative_path}`}><span className="muted"><code>{file.relative_path}</code>: {file.message ?? file.outcome}</span>{file.retryable && <button type="button" className="link-button" aria-label={`Retry ${file.relative_path} from ${file.location_id}`} onClick={() => void actions.onRetry(job, [file])}>Retry file</button>}</li>)}</ul>}
  </article>;
}

export function formatElapsed(startedAt: number, finishedAt?: number) {
  const seconds = Math.max(0, (finishedAt ?? Math.floor(Date.now() / 1000)) - startedAt);
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}

export function Timestamp({ label, value }: { label: string; value: number }) {
  const date = new Date(value * 1000);
  return <>{label && `${label} `}<time dateTime={date.toISOString()}>{date.toLocaleString()}</time></>;
}

/** Form wrapper that disables its fields and shows a saving state while the async submit handler runs. */
export function PendingForm({ onSubmit, children, className }: { onSubmit: (event: FormEvent) => unknown; children: ReactNode; className?: string }) {
  const submitted = useRef<FormEvent | null>(null);
  const { run, pending } = useAsyncAction({ action: () => onSubmit(submitted.current!), resetAfter: 450 });
  return <form className={className} aria-busy={pending} onSubmit={(event) => { event.preventDefault(); submitted.current = event; run(); }}><fieldset disabled={pending}>{children}</fieldset>{pending && <p role="status" className="save-status"><span className="button-spinner" aria-hidden="true" />Saving changes…</p>}</form>;
}
