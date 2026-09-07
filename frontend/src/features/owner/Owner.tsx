import { LoadingButton, useAsyncAction } from '../../vendor/interior/loading-button';
import { ProgressBar } from '../../vendor/interior/progress-bar';
import { Avatar } from '../../modules/ui/Feedback';
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { api } from '../../api/client';
import { ApiError, type ActiveSession, type IdentityConflict, type IdentityMerge, type IdentityRepairs, type MetadataCandidate, type MetadataField, type MetadataTarget, type PlaybackSettings, type PlaybackStatus, type Readiness, type Scan } from '../../core/api';
import type { ScreenPresence } from '../../core/screens';
import { Readiness as ReadinessPanel } from '../setup/Setup';
import { Wordmark } from '../../modules/productChrome/Wordmark';
import { SettingsPanel } from './SettingsPanel';

type OwnerProps = {
  onLogout: () => void;
  onBrowse: () => void;
};

export function Owner({ onLogout, onBrowse }: OwnerProps) {
  const [readiness, setReadiness] = useState<Readiness>();
  const [scan, setScan] = useState<Scan>();
  const [films, setFilms] = useState('');
  const [tv, setTV] = useState('');
  const [tmdbConfigured, setTMDBConfigured] = useState(false);
  const [tmdbToken, setTMDBToken] = useState('');
  const [unmatched, setUnmatched] = useState<MetadataTarget[]>([]);
  const [candidates, setCandidates] = useState<Record<string, MetadataCandidate[]>>({});
  const [identityRepairs, setIdentityRepairs] = useState<IdentityRepairs>();
  const [identityError, setIdentityError] = useState('');
  const [identityActionError, setIdentityActionError] = useState('');
  const [playbackSettings, setPlaybackSettings] = useState<PlaybackSettings>();
  const [playbackStatus, setPlaybackStatus] = useState<PlaybackStatus>();
  const [screens, setScreens] = useState<ScreenPresence[]>([]);
  const [name, setName] = useState('');
  const [pin, setPin] = useState('');
  const [notice, setNotice] = useState('');
  const [profilesVersion, setProfilesVersion] = useState(0);
  const [ownerRequired, setOwnerRequired] = useState(false);
  const [locked, setLocked] = useState<Set<string>>(new Set());

  const load = () => {
    api.setupStatus().then((status) => setReadiness(status.readiness)).catch((error) => setNotice(error instanceof ApiError ? error.message : 'Readiness is unavailable.'));
    api.scanStatus().then((result) => setScan(result.scan.status ? result.scan : undefined)).catch(() => undefined);
    api.ownerRoots().then((roots) => { setFilms(roots.films); setTV(roots.tv); }).catch((error) => { if (error instanceof ApiError && error.code === 'owner_required') setOwnerRequired(true); else setNotice('Library roots are unavailable.'); });
    api.tmdbSettings().then((settings) => setTMDBConfigured(settings.configured)).catch(() => setNotice('TMDB settings are unavailable.'));
    api.unmatchedMetadata().then((result) => setUnmatched(result.items ?? [])).catch(() => undefined);
    loadIdentityRepairs();
    api.playbackSettings().then(setPlaybackSettings).catch(() => setNotice('Playback settings are unavailable.'));
    api.playbackStatus().then(setPlaybackStatus).catch(() => setNotice('Playback status is unavailable.'));
    api.ownerScreens().then((result) => setScreens(result.screens ?? [])).catch(() => setNotice('Connected screens are unavailable.'));
    api.settingsInventory().then((result) => setLocked(new Set((result.settings ?? []).filter((setting) => !setting.mutable).map((setting) => setting.key)))).catch(() => undefined);
  };

  const loadIdentityRepairs = (clearActionError = true) => {
    setIdentityError('');
    if (clearActionError) setIdentityActionError('');
    api.identityRepairs().then((repairs) => setIdentityRepairs({ conflicts: repairs.conflicts ?? [], merges: repairs.merges ?? [] })).catch((error) => {
      if (error instanceof ApiError && error.code === 'owner_required') setOwnerRequired(true);
      else setIdentityError(error instanceof ApiError ? error.message : 'Identity repairs are unavailable.');
    });
  };

  const recheck = () => api.recheck().then((status) => setReadiness(status.readiness)).catch((error) => setNotice(error instanceof ApiError ? error.message : 'Readiness is unavailable.'));
  const refreshActivity = async () => {
    try {
      const [playback, connected] = await Promise.all([api.playbackStatus(), api.ownerScreens()]);
      setPlaybackStatus(playback);
      setScreens(connected.screens ?? []);
      setNotice('Activity updated.');
    } catch { setNotice('Activity is unavailable. Try refreshing again.'); }
  };
  useEffect(load, []);
  useEffect(() => {
    if (scan?.status !== 'running') return;
    const timer = window.setInterval(() => api.scanStatus().then((result) => setScan(result.scan)).catch(() => undefined), 1500);
    return () => clearInterval(timer);
  }, [scan?.status]);

  const roots = async (event: FormEvent) => {
    event.preventDefault();
    try {
      await api.roots(films, tv);
      setNotice('Library roots saved.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to save roots.');
    }
  };
  const saveTMDB = async (event: FormEvent) => {
    event.preventDefault();
    try {
      const settings = await api.saveTMDBToken(tmdbToken);
      setTMDBConfigured(settings.configured);
      setTMDBToken('');
      setNotice('TMDB credential saved.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to save TMDB credential.');
    }
  };
  const removeTMDB = async () => {
    try {
      const settings = await api.removeTMDBToken();
      setTMDBConfigured(settings.configured);
      setTMDBToken('');
      setNotice('TMDB credential removed.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to remove TMDB credential.');
    }
  };
  const findMatches = async (item: MetadataTarget) => { try { const result = await api.metadataCandidates(item.kind, item.id); setCandidates((current) => ({ ...current, [item.id]: result.candidates })); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Matches are unavailable.'); } };
  const selectMatch = async (item: MetadataTarget, candidate: MetadataCandidate) => { try { const matched = await api.matchMetadata(item.kind, item.id, candidate.id); setUnmatched((current) => current.map((value) => value.id === item.id ? matched : value)); setCandidates((current) => { const next = { ...current }; delete next[item.id]; return next; }); setNotice(`Matched ${item.title}.`); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to save this match.'); } };
  const clearMatch = async (item: MetadataTarget) => { try { await api.unmatchMetadata(item.kind, item.id); setUnmatched((current) => current.map((value) => value.id === item.id ? { ...value, provider_id: '', owner_matched: false } : value)); setNotice(`Unmatched ${item.title}.`); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to unmatch this title.'); } };
  const mergeIdentity = async (conflict: IdentityConflict, survivorID: string) => {
    const source = conflict.left.id === survivorID ? conflict.right : conflict.left;
    setIdentityActionError('');
    try {
      const merge = await api.mergeIdentity(conflict.kind, survivorID, source.id);
      setIdentityRepairs((current) => current && { conflicts: current.conflicts.filter((item) => item.id !== conflict.id), merges: [...current.merges.filter((item) => item.id !== merge.id), merge] });
      setNotice(`Identity repair merged ${merge.survivor.title} and ${merge.source.title}. Affected titles: ${merge.survivor.title} and ${merge.source.title}.`);
    } catch (error) {
      if (error instanceof ApiError && error.code === 'owner_required') setOwnerRequired(true);
      else if (error instanceof ApiError && error.code === 'identity_conflict') {
        setIdentityActionError('The repair list changed before this merge. Refresh it to review the current titles and decisions.');
        loadIdentityRepairs(false);
      } else setIdentityActionError(error instanceof ApiError ? error.message : 'Unable to merge these titles.');
    }
  };
  const unmergeIdentity = async (merge: IdentityMerge) => {
    setIdentityActionError('');
    try {
      const result = await api.unmergeIdentity(merge.id);
      setIdentityRepairs((current) => current && { ...current, merges: current.merges.map((item) => item.id === result.id ? result : item) });
      setNotice(`Unmerged ${result.survivor.title} and ${result.source.title}. Review the retained-state decision below.`);
    } catch (error) {
      if (error instanceof ApiError && error.code === 'owner_required') setOwnerRequired(true);
      else if (error instanceof ApiError && error.code === 'identity_conflict') {
        setIdentityActionError('The repair list changed before this unmerge. Refresh it to review the current titles and decisions.');
        loadIdentityRepairs(false);
      } else setIdentityActionError(error instanceof ApiError ? error.message : 'Unable to unmerge these titles.');
    }
  };
  const savePlayback = async (event: FormEvent) => {
    event.preventDefault();
    if (!playbackSettings) return;
    try {
      const result = await api.savePlaybackSettings(playbackSettings);
      setNotice(result.restart_required ? 'Playback settings saved. Restart Flixr to use the new segment directory.' : 'Playback settings saved.');
      setPlaybackStatus(await api.playbackStatus());
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to save playback settings.');
    }
  };
  const startScan = async () => {
    try {
      setScan((await api.scan()).scan);
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to start scan.');
    }
  };
  const create = async (event: FormEvent) => {
    event.preventDefault();
    try {
      await api.createProfile(name, pin);
      setName('');
      setPin('');
      setNotice('Profile created.');
      setProfilesVersion((value) => value + 1);
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to create profile.');
    }
  };

  if (ownerRequired) return <main className="owner"><OwnerHeader onBrowse={onBrowse} onLogout={onLogout} /><section className="auth-panel"><h1>Owner sign in required</h1><p>Your owner session has expired or this device has not signed in.</p><a className="button-link primary" href="/login">Sign in to administer Flixr</a></section></main>;

  return (
    <main className="owner">
      <OwnerHeader onBrowse={onBrowse} onLogout={onLogout} />
      <div className="owner-intro"><p className="eyebrow">YOUR SERVER</p><h1>Server settings</h1><p>Manage your libraries, household, and playback from this device.</p></div>
      {notice && <p className="owner-notice" role="status">{notice}</p>}
      <div className="owner-layout">
        <nav className="owner-nav" aria-label="Server settings"><a href="#libraries">Libraries</a><a href="#household">Household</a><a href="#playback">Playback & screens</a><a href="#metadata">Metadata</a><a href="#configuration">Configuration</a><a href="#support">Support</a></nav>
        <div className="owner-sections">
          <section id="libraries" className="owner-section" aria-labelledby="libraries-title">
            <h2 id="libraries-title">Libraries</h2><p>Folders are read from this server. Your original media stays untouched.</p>
            {readiness && <ReadinessControls readiness={readiness} onRecheck={recheck} />}
            <div className="owner-columns"><RootsForm films={films} tv={tv} onFilmsChange={setFilms} onTVChange={setTV} onSubmit={roots} locked={locked} /><ScanPanel scan={scan} onStart={startScan} /></div>
          </section>
          <section id="household" className="owner-section" aria-labelledby="household-title">
            <h2 id="household-title">Household</h2><p>Each profile has its own list, viewing progress, and optional PIN.</p>
            <div className="owner-columns"><ProfileForm name={name} pin={pin} onNameChange={setName} onPinChange={setPin} onSubmit={create} /><ProfileManager version={profilesVersion} /><SessionManager /></div>
          </section>
          <section id="playback" className="owner-section" aria-labelledby="playback-title">
            <h2 id="playback-title">Playback & screens</h2><p>Compatible files play directly. FFmpeg handles files that need conversion.</p>
            <LoadingButton onAction={refreshActivity} pendingLabel="Refreshing…" successLabel="Refresh activity">Refresh activity</LoadingButton>
            <ConnectedScreens screens={screens} />
            {playbackSettings && <details><summary>Playback resource limits</summary><PlaybackPanel settings={playbackSettings} status={playbackStatus} onChange={setPlaybackSettings} onSubmit={savePlayback} locked={locked} /></details>}
          </section>
          <section id="metadata" className="owner-section" aria-labelledby="metadata-title">
            <h2 id="metadata-title">Metadata</h2><p>Optional online artwork and descriptions. Browsing and playback work without a provider; downloaded artwork stays on your server.</p>
            <TMDBForm configured={tmdbConfigured} token={tmdbToken} onTokenChange={setTMDBToken} onSave={saveTMDB} onRemove={removeTMDB} locked={locked.has('metadata.tmdb_token')} />
            <MetadataRepair items={unmatched} candidates={candidates} onFind={findMatches} onSelect={selectMatch} onClear={clearMatch} onUpdate={(updated) => setUnmatched((current) => current.map((item) => item.id === updated.id ? updated : item))} />
            <IdentityRepair repairs={identityRepairs} error={identityError} actionError={identityActionError} onReload={() => loadIdentityRepairs()} onMerge={mergeIdentity} onUnmerge={unmergeIdentity} />
          </section>
          <SettingsPanel onNotice={setNotice} />
          <section id="support" className="owner-section" aria-labelledby="support-title">
            <h2 id="support-title">Support diagnostics</h2><p>Download a local ZIP with versions, runtime health, and recent failure IDs. It never includes media names, paths, passwords, or tokens.</p>
            <a className="button-link primary" href="/api/v1/owner/diagnostics" download>Download diagnostics</a>
          </section>
        </div>
      </div>
    </main>
  );
}

function MetadataRepair({ items, candidates, onFind, onSelect, onClear, onUpdate }: { items: MetadataTarget[]; candidates: Record<string, MetadataCandidate[]>; onFind: (item: MetadataTarget) => Promise<void>; onSelect: (item: MetadataTarget, candidate: MetadataCandidate) => Promise<void>; onClear: (item: MetadataTarget) => Promise<void>; onUpdate: (item: MetadataTarget) => void }) {
  return <section aria-labelledby="metadata-repair-title"><h3 id="metadata-repair-title">Title identification</h3>{items.length === 0 ? <p>Nothing needs review.</p> : <ul className="metadata-repair">{items.map((item) => <li key={item.id}><span>{item.title}{item.provider_id ? ' · matched' : ' · unmatched'}</span><button type="button" onClick={() => void onFind(item)}>{item.provider_id ? 'Change match' : 'Find matches'}</button>{item.provider_id && <button type="button" onClick={() => void onClear(item)}>Unmatch</button>}{candidates[item.id]?.map((candidate) => <button key={candidate.id} type="button" onClick={() => void onSelect(item, candidate)}>{candidate.title}{candidate.year ? ` (${candidate.year})` : ''} · TMDB</button>)}<MetadataEditor item={item} onSaved={onUpdate} /></li>)}</ul>}</section>;
}

function conflictReason(reason: string) {
  return ({ provider_identity: 'These titles claim the same provider identity.', replacement_evidence: 'Replacement evidence conflicts with the existing title identity.', ambiguous_duplicate: 'These files may be duplicates, but Flixr cannot safely decide.' } as Record<string, string>)[reason] ?? 'These titles have conflicting identities.';
}

function IdentityRepair({ repairs, error, actionError, onReload, onMerge, onUnmerge }: { repairs?: IdentityRepairs; error: string; actionError: string; onReload: () => void; onMerge: (conflict: IdentityConflict, survivorID: string) => Promise<void>; onUnmerge: (merge: IdentityMerge) => Promise<void> }) {
  const [survivors, setSurvivors] = useState<Record<number, string>>({});
  const [pending, setPending] = useState('');
  const openConflicts = repairs?.conflicts.filter((conflict) => conflict.state === 'open') ?? [];
  const merge = async (conflict: IdentityConflict) => {
    const survivorID = survivors[conflict.id];
    if (!survivorID) return;
    const survivor = conflict.left.id === survivorID ? conflict.left : conflict.right;
    const source = conflict.left.id === survivorID ? conflict.right : conflict.left;
    if (!window.confirm(`Merge ${source.title} into ${survivor.title}? Their viewing history remains recoverable until you choose Unmerge.`)) return;
    setPending(`merge-${conflict.id}`);
    try { await onMerge(conflict, survivorID); } finally { setPending(''); }
  };
  const unmerge = async (item: IdentityMerge) => {
    if (!window.confirm(`Unmerge ${item.survivor.title} and ${item.source.title}? Original identities are restored where newer viewing activity has not superseded them.`)) return;
    setPending(`unmerge-${item.id}`);
    try { await onUnmerge(item); } finally { setPending(''); }
  };
  return <section aria-labelledby="identity-repair-title" aria-busy={pending ? true : undefined}>
    <h3 id="identity-repair-title">Identity repair</h3>
    <p>Resolve conflicting title identities explicitly. Paths, fingerprints, and credentials are never shown here.</p>
    {!repairs && !error && <p role="status">Loading identity repairs…</p>}
    {!repairs && error && <div role="alert"><p>{error}</p><button type="button" onClick={onReload}>Retry identity repairs</button></div>}
    {actionError && <div className="identity-repair-error" role="alert"><p>{actionError}</p><button type="button" onClick={onReload}>Refresh repair list</button></div>}
    {openConflicts.map((conflict) => <article key={conflict.id} className="identity-repair">
      <h4>Conflict: {conflict.kind}</h4><p>{conflictReason(conflict.reason)}</p>
      <fieldset><legend>Choose the title to keep</legend>{[conflict.left, conflict.right].map((item) => <label key={item.id}><input type="radio" name={`identity-${conflict.id}`} checked={survivors[conflict.id] === item.id} onChange={() => setSurvivors((current) => ({ ...current, [conflict.id]: item.id }))} /> Keep {item.title}; merge {item.id === conflict.left.id ? conflict.right.title : conflict.left.title} into it</label>)}</fieldset>
      <button type="button" disabled={!survivors[conflict.id] || pending === `merge-${conflict.id}`} onClick={() => void merge(conflict)}>{pending === `merge-${conflict.id}` ? 'Merging titles…' : 'Merge selected titles'}</button>
    </article>)}
    {repairs && openConflicts.length === 0 && <p>No identity conflicts need repair.</p>}
    {repairs?.merges.length ? <section aria-labelledby="identity-merges-title"><h4 id="identity-merges-title">Identity repair history</h4>{repairs.merges.map((merge) => <article key={merge.id} className="identity-repair">
      <p><strong>{merge.state === 'unmerged' ? 'Unmerged' : 'Active merge'}:</strong> {merge.survivor.title} keeps identity; {merge.source.title} is the source.</p>
      <p>Affected titles: {merge.survivor.title} and {merge.source.title}.</p>
      {merge.state === 'unmerged' && <p role="status">Unmerge retained the state described in the reconciliation decisions.</p>}
      {merge.decisions.length > 0 && <ul aria-label={`Reconciliation decisions for ${merge.survivor.title} and ${merge.source.title}`}>{merge.decisions.map((decision) => <li key={decision}>{decision}</li>)}</ul>}
      {merge.state === 'active' && <button type="button" disabled={pending === `unmerge-${merge.id}`} onClick={() => void unmerge(merge)}>{pending === `unmerge-${merge.id}` ? 'Unmerging titles…' : `Unmerge ${merge.survivor.title} and ${merge.source.title}`}</button>}
    </article>)}</section> : null}
  </section>;
}

const metadataInputs: Array<[MetadataField['field'], string, 'owner' | 'local']> = [['title', 'Title', 'owner'], ['synopsis', 'Synopsis', 'owner'], ['year', 'Release year', 'owner'], ['poster', 'Poster URL', 'owner'], ['backdrop', 'Backdrop URL', 'owner'], ['tags', 'Tags', 'local'], ['content_rating', 'Content rating', 'local']];

function MetadataEditor({ item, onSaved }: { item: MetadataTarget; onSaved: (item: MetadataTarget) => void }) {
  const [fields, setFields] = useState<MetadataField[]>([]);
  const [preview, setPreview] = useState<MetadataField[]>([]);
  const [open, setOpen] = useState(false);
  const [notice, setNotice] = useState('');
  const load = async () => { try { setFields((await api.metadataFields(item.kind, item.id)).fields ?? []); setOpen(true); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Metadata fields are unavailable.'); } };
  const value = (name: MetadataField['field']) => fields.find((field) => field.field === name) ?? { field: name, value: name === 'title' ? item.title : name === 'synopsis' ? item.synopsis ?? '' : name === 'year' ? String(item.year ?? '') : name === 'poster' ? item.poster ?? '' : name === 'backdrop' ? item.backdrop ?? '' : '', source: name === 'tags' || name === 'content_rating' ? 'local' : 'owner', locked: false } as MetadataField;
  const change = (name: MetadataField['field'], patch: Partial<MetadataField>) => setFields((current) => { const old = current.find((field) => field.field === name) ?? value(name); return [...current.filter((field) => field.field !== name), { ...old, ...patch }]; });
  const editable = () => metadataInputs.map(([field, , source]) => ({ ...value(field), source, field }));
  const save = async () => { try { const updated = await api.editMetadata(item.kind, item.id, editable()); onSaved(updated); setNotice('Metadata saved.'); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to save metadata.'); } };
  const previewChanges = async () => { try { setPreview((await api.previewMetadata(item.kind, item.id, editable())).fields ?? []); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Preview is unavailable.'); } };
  const previewRefresh = async () => { try { setPreview((await api.refreshMetadataPreview(item.kind, item.id)).fields ?? []); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Provider refresh is unavailable.'); } };
  const refresh = async () => { try { const updated = await api.refreshMetadata(item.kind, item.id); onSaved(updated); setNotice('Provider metadata refreshed.'); } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Provider refresh is unavailable.'); } };
  return <details open={open} onToggle={(event) => { const next = (event.currentTarget as HTMLDetailsElement).open; setOpen(next); if (next && !open) void load(); }}><summary>Edit metadata</summary>{notice && <p role="status">{notice}</p>}{open && <div>{metadataInputs.map(([field, label]) => <label key={field}>{label}{field === 'synopsis' ? <textarea value={value(field).value} onChange={(event) => change(field, { value: event.target.value })} /> : <input value={value(field).value} onChange={(event) => change(field, { value: event.target.value })} /> }<span><input type="checkbox" checked={value(field).locked} onChange={(event) => change(field, { locked: event.target.checked })} /> Lock this field</span></label>)}<div className="actions"><button type="button" onClick={() => void previewChanges()}>Preview changes</button><button type="button" onClick={() => void save()}>Save metadata</button><button type="button" disabled={!item.provider_id} onClick={() => void previewRefresh()}>Preview provider refresh</button><button type="button" disabled={!item.provider_id} onClick={() => void refresh()}>Refresh unlocked fields</button></div>{preview.length > 0 && <ul aria-label="Metadata refresh preview">{preview.map((field) => <li key={field.field}>{field.field}: {field.value || 'empty'} · {field.source}{field.locked ? ' · locked' : ''}</li>)}</ul>}</div>}</details>;
}

function OwnerHeader({ onBrowse, onLogout }: OwnerProps) {
  return (
    <header>
      <Wordmark />
      <span>Owner operations</span>
      <button onClick={onBrowse}>Household home</button>
      <button onClick={() => void api.logout().finally(onLogout)}>Log out</button>
    </header>
  );
}

function ReadinessControls({ readiness, onRecheck }: { readiness: Readiness; onRecheck: () => Promise<void> }) {
  return (
    <>
      <ReadinessPanel readiness={readiness} />
      <LoadingButton onAction={onRecheck} pendingLabel="Checking…" successLabel="Recheck readiness">Recheck readiness</LoadingButton>
    </>
  );
}

function RootsForm({ films, tv, onFilmsChange, onTVChange, onSubmit, locked }: { films: string; tv: string; onFilmsChange: (value: string) => void; onTVChange: (value: string) => void; onSubmit: (event: FormEvent) => void; locked: Set<string> }) {
  return (
    <PendingForm onSubmit={onSubmit}>
      <h2>Library roots</h2>
      {(locked.has('library.films_root') || locked.has('library.tv_root')) && <p>Environment-managed fields are read-only.</p>}<label>Films root<input disabled={locked.has('library.films_root')} value={films} onChange={(event) => onFilmsChange(event.target.value)} placeholder="/media/films" /></label>
      <label>TV root<input disabled={locked.has('library.tv_root')} value={tv} onChange={(event) => onTVChange(event.target.value)} placeholder="/media/tv" /></label>
      <button className="primary" disabled={locked.has('library.films_root') && locked.has('library.tv_root')}>Save roots</button>
    </PendingForm>
  );
}

function ScanPanel({ scan, onStart }: { scan?: Scan; onStart: () => Promise<void> }) {
  return (
    <section>
      <h2>Manual scan</h2>
      <p>{scan ? `${scan.status}: ${scan.scanned} scanned, ${scan.unmatched} unmatched, ${scan.failed} failed.` : 'No scan has started.'}</p>
      {scan?.message && <p role="alert">{scan.message}</p>}
      <LoadingButton onAction={onStart} disabled={scan?.status === 'running'} pendingLabel="Starting…" successLabel="Start scan">{scan?.status === 'running' ? 'Scan in progress' : 'Start scan'}</LoadingButton>{scan?.status === 'running' && <ProgressBar value={null} label="Library scan" pendingLabel="Scanning your library…" />}
    </section>
  );
}

function TMDBForm({ configured, token, onTokenChange, onSave, onRemove, locked }: { configured: boolean; token: string; onTokenChange: (value: string) => void; onSave: (event: FormEvent) => void; onRemove: () => Promise<void>; locked: boolean }) {
  return (
    <PendingForm onSubmit={onSave}>
      <h2>TMDB metadata</h2>
      <p>{configured ? 'Credential configured. Enter a replacement token to change it.' : 'No credential configured.'}</p>
      {locked && <p>Managed by the server environment.</p>}<label>TMDB access token<input disabled={locked} type="password" value={token} onChange={(event) => onTokenChange(event.target.value)} autoComplete="new-password" /></label>
      <div className="actions">
        <button className="primary" disabled={locked}>Save TMDB credential</button>
        {configured && <button disabled={locked} type="button" onClick={() => void onRemove()}>Remove credential</button>}
      </div>
    </PendingForm>
  );
}

function PlaybackPanel({ settings, status, onChange, onSubmit, locked }: { settings: PlaybackSettings; status?: PlaybackStatus; onChange: (settings: PlaybackSettings) => void; onSubmit: (event: FormEvent) => void; locked: Set<string> }) {
  return <PendingForm onSubmit={onSubmit}>
    <h2>Playback resources</h2>
    <p>{status?.generations?.length ? `${status.generations.length} compatibility generation${status.generations.length === 1 ? '' : 's'} active.` : 'No compatibility generations are active.'}</p>
    {locked.size > 0 && <p>Environment-managed fields are read-only.</p>}<label>Segment directory<input disabled={locked.has('playback.segment_dir')} value={settings.segment_dir} onChange={(event) => onChange({ ...settings, segment_dir: event.target.value })} /></label>
    <label>Per-generation bytes<input disabled={locked.has('playback.generation_bytes')} type="number" min="1" value={settings.generation_bytes} onChange={(event) => onChange({ ...settings, generation_bytes: Number(event.target.value) })} /></label>
    <label>Global bytes<input disabled={locked.has('playback.global_bytes')} type="number" min="1" value={settings.global_bytes} onChange={(event) => onChange({ ...settings, global_bytes: Number(event.target.value) })} /></label>
    <label>Concurrent generations<input disabled={locked.has('playback.max_generations')} type="number" min="1" value={settings.max_generations} onChange={(event) => onChange({ ...settings, max_generations: Number(event.target.value) })} /></label>
    <button className="primary">Save playback limits</button>
  </PendingForm>;
}

function ConnectedScreens({ screens }: { screens: ScreenPresence[] }) {
  return <section><h2>Connected screens</h2>{screens.length ? <ul>{screens.map((screen) => <li key={screen.id}>{screen.name} · {screen.state}</li>)}</ul> : <p>No Flixr screens are connected.</p>}</section>;
}

function ProfileForm({ name, pin, onNameChange, onPinChange, onSubmit }: { name: string; pin: string; onNameChange: (value: string) => void; onPinChange: (value: string) => void; onSubmit: (event: FormEvent) => void }) {
  return (
    <PendingForm onSubmit={onSubmit}>
      <h2>Household profiles</h2>
      <label>Name<input required value={name} onChange={(event) => onNameChange(event.target.value)} /></label>
      <label>PIN (optional)<input type="password" value={pin} onChange={(event) => onPinChange(event.target.value)} /></label>
      <button className="primary">Create profile</button>
    </PendingForm>
  );
}

export function ProfileManager({ version = 0 }: { version?: number }) {
  const [profiles, setProfiles] = useState<{ id: string; name: string; protected: boolean }[]>([]);
  const [notice, setNotice] = useState('');
  const load = () => api.profiles().then((value) => setProfiles(value.profiles)).catch(() => setNotice('Profiles are unavailable.'));
  useEffect(() => { void load(); }, [version]);

  return (
    <section>
      <h2>Manage profiles</h2>
      {notice && <p role="status">{notice}</p>}
      {profiles.map((profile) => <ProfileRow key={profile.id} profile={profile} onSaved={load} onNotice={setNotice} />)}
    </section>
  );
}

function ProfileRow({ profile, onSaved, onNotice }: { profile: { id: string; name: string; protected: boolean }; onSaved: () => void; onNotice: (value: string) => void }) {
  const [name, setName] = useState(profile.name);
  const [pin, setPin] = useState('');
  const [deleting, setDeleting] = useState(false);
  const save = async (event: FormEvent) => {
    event.preventDefault();
    try {
      await api.updateProfile(profile.id, { name, ...(pin ? { pin } : {}) });
      setPin('');
      onNotice('Profile updated.');
      onSaved();
    } catch (error) {
      onNotice(error instanceof ApiError ? error.message : 'Unable to update profile.');
    }
  };

  return (
    <PendingForm className="owner-profile-row" onSubmit={save}>
      <strong><Avatar name={profile.name} />{profile.name} {profile.protected ? '· PIN protected' : '· no PIN'}</strong>
      <label>Profile name<input value={name} onChange={(event) => setName(event.target.value)} /></label>
      <label>New PIN<input type="password" value={pin} onChange={(event) => setPin(event.target.value)} /></label>
      <div className="actions">
        <button>Save</button>
        <button type="button" disabled={deleting} onClick={() => {
          if (!window.confirm(`Delete ${profile.name}? This permanently removes this profile's viewing history and My List.`)) return;
          setDeleting(true);
          void api.deleteProfile(profile.id).then(onSaved).catch((error) => onNotice(error instanceof ApiError ? error.message : 'Unable to delete profile.')).finally(() => setDeleting(false));
        }}>{deleting ? 'Deleting…' : 'Delete profile'}</button>
        {profile.protected && <button type="button" onClick={() => void api.updateProfile(profile.id, { unprotect: true }).then(onSaved).catch((error) => onNotice(error instanceof ApiError ? error.message : 'Unable to remove PIN.'))}>Remove PIN</button>}
      </div>
    </PendingForm>
  );
}

function SessionManager() {
  const [sessions, setSessions] = useState<ActiveSession[]>([]);
  const [notice, setNotice] = useState('');
  const load = () => api.activeSessions().then((value) => setSessions(value.sessions ?? [])).catch(() => setNotice('Sessions are unavailable.'));
  useEffect(() => { void load(); }, []);
  return <section><h2>Active sessions</h2>{notice && <p role="status">{notice}</p>}{sessions.length ? <ul>{sessions.map((session) => <li key={session.id}>{session.subject} · expires {new Date(session.expires_at * 1000).toLocaleString()} <button onClick={() => void api.revokeSession(session.id).then(load).catch(() => setNotice('Unable to revoke session.'))}>Revoke</button></li>)}</ul> : <p>No active sessions.</p>}</section>;
}

function PendingForm({ onSubmit, children, className }: { onSubmit: (event: FormEvent) => unknown; children: ReactNode; className?: string }) {
  const submitted = useRef<FormEvent | null>(null);
  const { run, pending } = useAsyncAction({ action: () => onSubmit(submitted.current!), resetAfter: 450 });
  return <form className={className} aria-busy={pending} onSubmit={(event) => { event.preventDefault(); submitted.current = event; run(); }}><fieldset disabled={pending}>{children}</fieldset>{pending && <p role="status" className="save-status"><span className="button-spinner" aria-hidden="true" />Saving changes…</p>}</form>;
}
