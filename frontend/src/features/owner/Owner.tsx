import { LoadingButton, useAsyncAction } from '../../vendor/interior/loading-button';
import { ProgressBar } from '../../vendor/interior/progress-bar';
import { Avatar } from '../../modules/ui/Feedback';
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { api } from '../../api/client';
import { ApiError, type ActiveSession, type IdentityConflict, type IdentityMerge, type IdentityRepairs, type Library, type LibraryLocation, type MetadataCandidate, type MetadataField, type MetadataTarget, type PlaybackSettings, type PlaybackStatus, type Readiness, type Scan, type ScanJob, type ScanPolicy, type TMDBSettings } from '../../core/api';
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
  const [locations, setLocations] = useState<LibraryLocation[]>([]);
  const [libraries, setLibraries] = useState<Library[]>([]);
  const [scanPolicies, setScanPolicies] = useState<Record<string, ScanPolicy>>({});
  const [scanJobs, setScanJobs] = useState<ScanJob[]>([]);
  const [tmdbSettings, setTMDBSettings] = useState<TMDBSettings>();
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
    api.scanStatus().then((result) => { setScan(result.scan.status ? result.scan : undefined); setLocations(result.locations ?? []); }).catch(() => undefined);
    api.libraries().then(async (result) => { const loaded=result.libraries??[];setLibraries(loaded);const policies=await Promise.all(loaded.map((library)=>api.scanPolicy(library.id).then((value)=>value.policy)));setScanPolicies(Object.fromEntries(policies.map((policy)=>[policy.library_id,policy]))); }).catch((error) => { if (error instanceof ApiError && error.code === 'owner_required') setOwnerRequired(true); else setNotice('Libraries are unavailable.'); });
    api.scanJobs().then((result)=>setScanJobs(result.jobs??[])).catch(()=>undefined);
    api.tmdbSettings().then(setTMDBSettings).catch(() => setNotice('TMDB settings are unavailable.'));
    api.unmatchedMetadata().then((result) => setUnmatched(result.items ?? [])).catch(() => undefined);
    loadIdentityRepairs();
    api.playbackSettings().then(setPlaybackSettings).catch(() => setNotice('Playback settings are unavailable.'));
    api.playbackStatus().then(setPlaybackStatus).catch(() => setNotice('Playback status is unavailable.'));
    api.ownerScreens().then((result) => setScreens(result.screens ?? [])).catch(() => setNotice('Connected screens are unavailable.'));
    api.settingsInventory().then((result) => setLocked(new Set((result.settings ?? []).filter((setting) => setting.source === 'environment').map((setting) => setting.key)))).catch(() => undefined);
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
    const timer = window.setInterval(() => api.scanStatus().then((result) => {
      setScan(result.scan);
      setLocations(result.locations ?? []);
      if (result.scan.status !== 'running') void api.tmdbSettings().then(setTMDBSettings).catch(() => undefined);
    }).catch(() => undefined), 1500);
    return () => clearInterval(timer);
  }, [scan?.status]);
  useEffect(()=>{
    if (!scanJobs.some((job)=>job.status==='queued'||job.status==='running')) return;
    const timer=window.setInterval(()=>api.scanJobs().then((result)=>setScanJobs(result.jobs??[])).catch(()=>undefined),1500);
    return ()=>clearInterval(timer);
  },[scanJobs]);

  const refreshLibraries = async () => {
    const loaded = (await api.libraries()).libraries ?? [];
    setLibraries(loaded);
    const policies = await Promise.all(loaded.map((library) => api.scanPolicy(library.id).then((value) => value.policy)));
    setScanPolicies(Object.fromEntries(policies.map((policy) => [policy.library_id, policy])));
  };
  const createLibrary = async (name: string, kind: Library['kind']) => {
    try { await api.createLibrary(name, kind); await refreshLibraries(); setNotice(`Library ${name.trim()} created.`); }
    catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to create library.'); }
  };
  const renameLibrary = async (library: Library) => {
    const name = window.prompt('Library name', library.name)?.trim();
    if (!name || name === library.name) return;
    try { await api.renameLibrary(library.id, name); await refreshLibraries(); setNotice('Library renamed.'); }
    catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to rename library.'); }
  };
  const deleteLibrary = async (library: Library) => {
    if (!window.confirm(`Delete the empty library “${library.name}”?`)) return;
    try { await api.deleteLibrary(library.id); await refreshLibraries(); setNotice('Empty library deleted.'); }
    catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to delete library.'); }
  };
  const addLocation = async (library: Library, path: string) => {
    try { await api.addLibraryLocation(library.id, path); await refreshLibraries(); setNotice(`Folder added to ${library.name}.`); }
    catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to add folder.'); }
  };
  const changeLocation = async (location: LibraryLocation, path: string) => {
    try {
      const preview = await api.previewLibraryLocationChange(location.id, path);
      const action = path ? 'move this folder' : 'remove this folder';
      if (!window.confirm(`This will ${action} and make ${preview.affected_titles} affected titles unavailable until another location or a fresh scan verifies them. Media files and viewing history are kept. Continue?`)) {
        await api.cancelLibraryLocationChange(preview.id);
        await refreshLibraries();
        return;
      }
      const result = await api.confirmLibraryLocationChange(preview.id);
      setLibraries(result.libraries ?? []);
      setNotice(path ? 'Library folder moved. Start a scan to verify the new location.' : 'Library folder removed. Media files were untouched and title history was kept.');
    } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to change library folder.'); }
  };
  const confirmPendingLocation = async (location: LibraryLocation) => {
    if (!location.pending_change_id) return;
    const action = location.pending_path ? `move this folder to ${location.pending_path}` : 'remove this folder';
    if (!window.confirm(`${location.pending_change_origin === 'environment' ? 'The server environment requested to ' : 'Apply the pending request to '}${action}? Media files and viewing history are kept.`)) return;
    try {
      const result = await api.confirmLibraryLocationChange(location.pending_change_id);
      setLibraries(result.libraries ?? []);
      setNotice(location.pending_path ? 'Environment-managed folder moved. Start a scan to verify it.' : 'Environment-managed folder removed.');
    } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to apply the pending folder change.'); }
  };
  const cancelPendingLocation = async (location: LibraryLocation) => {
    if (!location.pending_change_id) return;
    try {
      const result = await api.cancelLibraryLocationChange(location.pending_change_id);
      setLibraries(result.libraries ?? []);
      setNotice('Pending folder change discarded.');
    } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Unable to discard the pending folder change.'); }
  };
  const saveTMDB = async (event: FormEvent) => {
    event.preventDefault();
    try {
      const settings = await api.saveTMDBToken(tmdbToken);
      setTMDBSettings(settings);
      setTMDBToken('');
      try {
        const result = await api.scan();
        setScan(result.scan);
        if(result.jobs)setScanJobs((current)=>[...result.jobs!,...current.filter((job)=>!result.jobs!.some((queued)=>queued.id===job.id))]);
        setTMDBSettings({ ...settings, state: 'running', message: 'Metadata enrichment is running with the library scan.' });
        setNotice('TMDB credential verified and saved. Metadata enrichment is running.');
      } catch (error) {
        setNotice(error instanceof ApiError ? `TMDB credential verified and saved. ${error.message} Start a scan from Libraries when ready.` : 'TMDB credential verified and saved. Start a scan from Libraries when ready.');
      }
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to save TMDB credential.');
    }
  };
  const removeTMDB = async () => {
    try {
      const settings = await api.removeTMDBToken();
      setTMDBSettings(settings);
      setTMDBToken('');
      setNotice('TMDB credential removed.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to remove TMDB credential.');
    }
  };
	const setMetadataEnabled = async (enabled: boolean) => {
		try {
			setTMDBSettings(await api.setMetadataEnabled(enabled));
			setNotice(enabled ? 'Remote metadata enabled.' : 'Remote metadata disabled. Cached artwork and descriptions remain available.');
		} catch (error) {
			setNotice(error instanceof ApiError ? error.message : 'Unable to change remote metadata.');
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
      const result=await api.scan();setScan(result.scan);if(result.jobs)setScanJobs((current)=>[...result.jobs!,...current.filter((job)=>!result.jobs!.some((queued)=>queued.id===job.id))]);
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to start scan.');
    }
  };
  const saveScanPolicy=async(policy:ScanPolicy)=>{try{const result=await api.saveScanPolicy(policy.library_id,policy);setScanPolicies((current)=>({...current,[policy.library_id]:result.policy}));setNotice('Scan settings saved.');}catch(error){setNotice(error instanceof ApiError?error.message:'Unable to save scan settings.');}};
  const queueLibraryScan=async(libraryID:string)=>{try{const result=await api.queueScan(libraryID);setScanJobs((current)=>[result.job,...current.filter((job)=>job.id!==result.job.id)]);setNotice('Library scan queued.');}catch(error){setNotice(error instanceof ApiError?error.message:'Unable to queue library scan.');}};
  const cancelScanJob=async(id:string)=>{try{const result=await api.cancelScanJob(id);setScanJobs((current)=>current.map((job)=>job.id===id?result.job:job));}catch(error){setNotice(error instanceof ApiError?error.message:'Unable to cancel scan.');}};
  const retryScanJob=async(job:ScanJob,files:ScanJob['files']=(job.files??[]).filter((file)=>file.retryable))=>{try{const result=await api.retryScanJob(job.id,files??[]);setScanJobs((current)=>[result.job,...current]);setNotice(files?.length===1?'File queued for retry.':'Failed files queued for retry.');}catch(error){setNotice(error instanceof ApiError?error.message:'Unable to retry files.');}};
  const confirmRemovals = async (location: LibraryLocation) => {
    if (!location.pending_scan_id || !window.confirm(`Confirm removal of ${location.missing} missing files from the ${location.root_kind === 'film' ? 'Films' : 'TV'} library? Their titles and viewing history remain recoverable.`)) return;
    try {
      const result = await api.confirmScanRemovals(location.pending_scan_id, location.id);
      setLocations(result.locations ?? []);
      setScan(result.scan);
      setNotice('Library cleanup confirmed. Missing titles remain in your history and lists as unavailable.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to confirm library cleanup. Refresh scan status and try again.');
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
            <LibraryManager libraries={libraries} locked={locked} onCreate={createLibrary} onRename={renameLibrary} onDelete={deleteLibrary} onAddLocation={addLocation} onChangeLocation={changeLocation} onConfirmPending={confirmPendingLocation} onCancelPending={cancelPendingLocation} /><ScanPanel scan={scan} locations={locations} onStart={startScan} onConfirm={confirmRemovals} />
            <ScanSchedules libraries={libraries} policies={scanPolicies} jobs={scanJobs} locked={locked.has('background.scan_schedule')} onSave={saveScanPolicy} onRun={queueLibraryScan} onCancel={cancelScanJob} onRetry={retryScanJob} />
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
			<label><input type="checkbox" checked={tmdbSettings?.enabled ?? true} disabled={locked.has('metadata.enabled')} onChange={(event) => void setMetadataEnabled(event.target.checked)} /> Use online metadata</label>
			{locked.has('metadata.enabled') && <p>Remote metadata policy is managed by the server environment.</p>}
            <TMDBForm settings={tmdbSettings} token={tmdbToken} onTokenChange={setTMDBToken} onSave={saveTMDB} onRemove={removeTMDB} locked={locked.has('metadata.tmdb_token')} />
            <MetadataRepair items={unmatched} candidates={candidates} onFind={findMatches} onSelect={selectMatch} onClear={clearMatch} onUpdate={(updated) => setUnmatched((current) => current.map((item) => item.id === updated.id ? updated : item))} />
            <IdentityRepair repairs={identityRepairs} error={identityError} actionError={identityActionError} onReload={() => loadIdentityRepairs()} onMerge={mergeIdentity} onUnmerge={unmergeIdentity} />
          </section>
          <SettingsPanel onNotice={setNotice} />
          <section id="support" className="owner-section" aria-labelledby="support-title">
            <h2 id="support-title">Support diagnostics</h2><p>Download a local ZIP with versions, runtime health, and recent failure IDs. It never includes media names, paths, passwords, or tokens.</p>
            <a className="button-link primary" href="/api/v1/owner/diagnostics" download>Download diagnostics</a>
			<h3>Credits</h3><a href="https://www.themoviedb.org" aria-label="Visit TMDB"><img src="/tmdb-logo.svg" alt="TMDB" width="180" /></a><p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p>
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

function LibraryManager({ libraries, locked, onCreate, onRename, onDelete, onAddLocation, onChangeLocation, onConfirmPending, onCancelPending }: { libraries: Library[]; locked: Set<string>; onCreate: (name: string, kind: Library['kind']) => Promise<void>; onRename: (library: Library) => Promise<void>; onDelete: (library: Library) => Promise<void>; onAddLocation: (library: Library, path: string) => Promise<void>; onChangeLocation: (location: LibraryLocation, path: string) => Promise<void>; onConfirmPending: (location: LibraryLocation) => Promise<void>; onCancelPending: (location: LibraryLocation) => Promise<void> }) {
  const [name, setName] = useState('');
  const [kind, setKind] = useState<Library['kind']>('film');
  const [paths, setPaths] = useState<Record<string, string>>({});
  const locationLocked = (location: LibraryLocation) => (location.id === 'films-root' && locked.has('library.films_root')) || (location.id === 'tv-root' && locked.has('library.tv_root'));
  return <section className="library-manager"><h2>Named libraries</h2><p>Group folders from local disks and mounted shares. Overlapping folders are rejected.</p>
    {libraries.map((library) => <section key={library.id}><div className="actions"><h3>{library.name}</h3><span>{library.kind === 'film' ? 'Films' : 'TV'}</span><button type="button" onClick={() => void onRename(library)}>Rename</button>{library.locations.length === 0 && <button type="button" onClick={() => void onDelete(library)}>Delete library</button>}</div>
      {library.locations.map((location) => <div key={location.id} className="library-location"><code>{location.path}</code><span>{location.state === 'available' ? `${location.items} files` : location.state.replace('_', ' ')}</span>{location.message && <span>{location.message}</span>}{location.pending_change_id && <><span>{location.pending_change_origin === 'environment' ? 'Environment requested' : 'Pending change'} {location.pending_path ? <code>{location.pending_path}</code> : 'removal'}.</span><button type="button" onClick={() => void onConfirmPending(location)}>Review and apply</button><button type="button" onClick={() => void onCancelPending(location)}>Discard</button></>}{locationLocked(location) ? <span>Managed by environment</span> : <><button type="button" onClick={() => { const next = window.prompt('New folder path', location.path ?? '')?.trim(); if (next) void onChangeLocation(location, next); }}>Move folder</button><button type="button" onClick={() => void onChangeLocation(location, '')}>Remove folder</button></>}</div>)}
      <PendingForm onSubmit={async (event) => { event.preventDefault(); const path = paths[library.id]?.trim(); if (!path) return; await onAddLocation(library, path); setPaths((current) => ({ ...current, [library.id]: '' })); }}><label>Add folder to {library.name}<input value={paths[library.id] ?? ''} onChange={(event) => setPaths((current) => ({ ...current, [library.id]: event.target.value }))} placeholder="/media/library" /></label><button>Add folder</button></PendingForm>
    </section>)}
    <PendingForm onSubmit={async (event) => { event.preventDefault(); if (!name.trim()) return; await onCreate(name, kind); setName(''); }}><h3>Create library</h3><label>Name<input value={name} onChange={(event) => setName(event.target.value)} /></label><label>Media type<select value={kind} onChange={(event) => setKind(event.target.value as Library['kind'])}><option value="film">Films</option><option value="episode">TV episodes</option></select></label><button>Create library</button></PendingForm>
  </section>;
}

function ScanPanel({ scan, locations, onStart, onConfirm }: { scan?: Scan; locations: LibraryLocation[]; onStart: () => Promise<void>; onConfirm: (location: LibraryLocation) => Promise<void> }) {
  return (
    <section>
      <h2>Manual scan</h2>
      <p>{scan ? `${scan.status}: ${scan.scanned} scanned, ${scan.unmatched} unmatched, ${scan.failed} failed.` : 'No scan has started.'}</p>
      {scan?.message && <p role="alert">{scan.message}</p>}
      {locations.map((location) => <div key={location.id}>
        <p><strong>{location.root_kind === 'film' ? 'Films' : 'TV'}:</strong> {location.state === 'available' && location.scan_complete ? `${location.items} files checked.` : location.state === 'unavailable' ? 'Location unavailable; the prior catalog was kept.' : location.state === 'review_required' ? `${location.missing} missing files need owner review.` : 'Last scan was incomplete.'}</p>
        {location.state === 'review_required' && <button type="button" onClick={() => void onConfirm(location)}>Confirm removal</button>}
      </div>)}
      <LoadingButton onAction={onStart} disabled={scan?.status === 'running'} pendingLabel="Starting…" successLabel="Start scan">{scan?.status === 'running' ? 'Scan in progress' : 'Start scan'}</LoadingButton>{scan?.status === 'running' && <ProgressBar value={null} label="Library scan" pendingLabel="Scanning your library…" />}
    </section>
  );
}

function ScanSchedules({ libraries, policies, jobs, locked, onSave, onRun, onCancel, onRetry }: { libraries: Library[]; policies: Record<string, ScanPolicy>; jobs: ScanJob[]; locked: boolean; onSave: (policy: ScanPolicy) => Promise<void>; onRun: (libraryID: string) => Promise<void>; onCancel: (id: string) => Promise<void>; onRetry: (job: ScanJob, files?: ScanJob['files']) => Promise<void> }) {
  return <section className="owner-panel" aria-labelledby="scan-schedules-title">
    <div className="section-heading"><div><p className="eyebrow">Background work</p><h2 id="scan-schedules-title">Scheduled scans</h2></div></div>
    <p className="muted">Poll each library on its own schedule. Missed times coalesce into one run, and unchanged files are skipped without probing.</p>
    {locked && <p className="muted">Schedule timing is managed by FLIXR_SCAN_SCHEDULE. Excluded paths remain editable.</p>}
    <div className="stack">{libraries.map((library)=><ScanPolicyEditor key={library.id} library={library} policy={policies[library.id]} locked={locked} onSave={onSave} onRun={onRun}/>)}</div>
    <h3>Scan activity</h3>
    {jobs.length===0?<p className="muted">No scheduled or manual scan jobs yet.</p>:<div className="stack">{jobs.map((job)=>{
      const active=job.status==='queued'||job.status==='running';const retryableFiles=(job.files??[]).filter((file)=>file.retryable);const retryable=retryableFiles.length>0||job.status==='failed'||job.status==='interrupted';
      return <article className="setting-row" key={job.id}><div><strong>{libraries.find((library)=>library.id===job.library_id)?.name??'Library'} · {job.status}</strong><p className="muted">{job.trigger} · {job.scanned} scanned · {job.skipped} unchanged · {job.failed} failed{job.total!==undefined?` · ${job.total} total`:''}{job.started_at?` · ${formatElapsed(job.started_at,job.finished_at)} elapsed`:''}</p><p className="muted"><Timestamp label="Queued" value={job.queued_at} />{job.started_at&&<> · <Timestamp label="Started" value={job.started_at} /></>}{job.finished_at&&<> · <Timestamp label="Finished" value={job.finished_at} /></>}</p>{job.message&&<p role="alert">{job.message}</p>}{job.files?.map((file)=><div className="button-row" key={`${file.location_id}:${file.relative_path}`}><p className="muted">{file.relative_path}: {file.message??file.outcome}</p>{file.retryable&&<button type="button" className="secondary" aria-label={`Retry ${file.relative_path} from ${file.location_id}`} onClick={()=>void onRetry(job,[file])}>Retry file</button>}</div>)}{job.status==='running'&&<ProgressBar value={null} label="Library scan" pendingLabel="Scanning…" />}</div><div className="button-row">{active&&<button type="button" className="secondary" onClick={()=>void onCancel(job.id)}>Cancel</button>}{retryable&&<button type="button" className="secondary" onClick={()=>void onRetry(job,retryableFiles)}>Retry failed files</button>}</div></article>;
    })}</div>}
  </section>;
}

function formatElapsed(startedAt:number,finishedAt?:number){const seconds=Math.max(0,(finishedAt??Math.floor(Date.now()/1000))-startedAt);return seconds<60?`${seconds}s`:`${Math.floor(seconds/60)}m ${seconds%60}s`;}

function Timestamp({label,value}:{label:string;value:number}){const date=new Date(value*1000);return <>{label&&`${label} `}<time dateTime={date.toISOString()}>{date.toLocaleString()}</time></>;}

function ScanPolicyEditor({ library, policy, locked, onSave, onRun }: { library: Library; policy?: ScanPolicy; locked: boolean; onSave: (policy: ScanPolicy) => Promise<void>; onRun: (libraryID: string) => Promise<void> }) {
  const [draft,setDraft]=useState<ScanPolicy>();
  useEffect(()=>{if(policy)setDraft(policy)},[policy]);
  if(!draft)return <div className="setting-row"><strong>{library.name}</strong><span className="muted">Loading schedule…</span></div>;
  const next=draft.next_run_at?new Date(draft.next_run_at*1000).toLocaleString():'Not scheduled';
  return <form className="setting-row" onSubmit={(event)=>{event.preventDefault();void onSave(draft)}}><div><strong>{library.name}</strong><label><input type="checkbox" checked={draft.enabled} disabled={locked} onChange={(event)=>setDraft({...draft,enabled:event.target.checked})}/> Enable scheduled scans</label><div className="button-row"><label>Frequency<select value={draft.schedule_kind} disabled={locked} onChange={(event)=>setDraft({...draft,schedule_kind:event.target.value as ScanPolicy['schedule_kind']})}><option value="interval">Interval</option><option value="daily">Daily</option></select></label>{draft.schedule_kind==='interval'?<label>Every (hours)<input type="number" min="1" max="8760" value={Math.max(1,Math.round(draft.interval_seconds/3600))} disabled={locked} onChange={(event)=>setDraft({...draft,interval_seconds:Number(event.target.value)*3600})}/></label>:<><label>Local time<input type="time" value={draft.local_time} disabled={locked} onChange={(event)=>setDraft({...draft,local_time:event.target.value})}/></label><label>Timezone<input value={draft.timezone} disabled={locked} onChange={(event)=>setDraft({...draft,timezone:event.target.value})}/></label></>}</div><label>Excluded paths<textarea value={draft.exclusions.join('\n')} placeholder={'Extras/**\n**/Samples/**'} onChange={(event)=>setDraft({...draft,exclusions:event.target.value.split('\n').map((value)=>value.trim()).filter(Boolean)})}/></label><p className="muted">Next run: {next}. Daily scans use the chosen timezone; daylight-saving gaps run at the first valid local minute.</p><p className="muted">Last successful scan: {draft.last_success_at?<Timestamp label="" value={draft.last_success_at}/>: 'Never'}.</p></div><div className="button-row"><button type="button" className="secondary" aria-label={`Run now for ${library.name}`} onClick={()=>void onRun(library.id)}>Run now</button><button type="submit" aria-label={`${locked?'Save exclusions':'Save schedule'} for ${library.name}`}>{locked?'Save exclusions':'Save schedule'}</button></div></form>;
}

function TMDBForm({ settings, token, onTokenChange, onSave, onRemove, locked }: { settings?: TMDBSettings; token: string; onTokenChange: (value: string) => void; onSave: (event: FormEvent) => void; onRemove: () => Promise<void>; locked: boolean }) {
	const overrideConfigured = settings?.source === 'owner' || settings?.source === 'environment';
	const state = settings?.state ?? (settings?.configured ? 'configured' : 'unavailable');
	const message = settings?.message ?? (settings?.configured ? 'Credential configured. Enter a replacement token to change it.' : 'Metadata access is unavailable in this build.');
  return (
		<section><p><strong>Provider status: {state} · {settings?.source ?? 'none'}</strong></p><p>{message}</p><details><summary>Advanced: personal TMDB credential</summary><PendingForm onSubmit={onSave}>
      <h2>TMDB metadata</h2>
			<p><a href="https://www.themoviedb.org/settings/api">Get your TMDB API Read Access Token</a> only if you want to replace automatic application access. Flixr verifies it before saving it.</p>
      {locked && <p>Managed by the server environment.</p>}<label>TMDB API Read Access Token<input disabled={locked} type="password" value={token} onChange={(event) => onTokenChange(event.target.value)} autoComplete="new-password" /></label>
      <div className="actions">
        <button className="primary" disabled={locked}>Save TMDB credential</button>
				{overrideConfigured && <button disabled={locked} type="button" onClick={() => void onRemove()}>Remove credential</button>}
      </div>
		</PendingForm></details></section>
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
