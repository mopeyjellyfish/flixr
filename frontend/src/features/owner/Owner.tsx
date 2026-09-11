import { LoadingButton } from '../../vendor/interior/loading-button';
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { useScrollSpy } from '../../vendor/interior/scroll-spy';
import { HouseholdPanel } from './Household';
import { api } from '../../api/client';
import { ApiError, type IdentityConflict, type IdentityMerge, type IdentityRepairs, type Library, type LibraryLocation, type MetadataCandidate, type MetadataTarget, type PlaybackSettings, type PlaybackStatus, type Readiness, type Scan, type ScanJob, type ScanPolicy, type TMDBSettings } from '../../core/api';
import type { ScreenPresence } from '../../core/screens';
import { Wordmark } from '../../modules/productChrome/Wordmark';
import { useConfirm } from '../../modules/ui/ConfirmDialog';
import { SettingsPanel } from './SettingsPanel';
import { BackupPanel } from './BackupPanel';
import { LibrariesSection, type LibraryActions, type ScanActions } from './Libraries';
import { IdentityRepair, MetadataRepair, ProviderStatus, TMDBForm } from './Metadata';
import { MediaVersions } from './MediaVersions';
import { ConnectedScreens, PlaybackPanel } from './Playback';
import { EpisodeOrder } from './EpisodeOrder';

type OwnerProps = {
  onLogout: () => void;
  onBrowse: () => void;
};

const ownerSections = [
  { id: 'libraries', label: 'Libraries' }, { id: 'household', label: 'Household' }, { id: 'playback', label: 'Playback & screens' },
  { id: 'metadata', label: 'Metadata' }, { id: 'backups', label: 'Backups' }, { id: 'system', label: 'System' },
];

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
  const [notice, setNotice] = useState('');
  const [ownerRequired, setOwnerRequired] = useState(false);
  const [locked, setLocked] = useState<Set<string>>(new Set());
  const [backupStatus, setBackupStatus] = useState<string>();
  const { confirm, dialog } = useConfirm();
  const requireOwner = useCallback(() => setOwnerRequired(true), []);

  const load = () => {
    api.setupStatus().then((status) => setReadiness(status.readiness)).catch((error) => setNotice(error instanceof ApiError ? error.message : 'Readiness is unavailable.'));
    api.scanStatus().then((result) => { setScan(result.scan.status ? result.scan : undefined); setLocations(result.locations ?? []); }).catch(() => undefined);
    api.libraries().then(async (result) => { const loaded = result.libraries ?? []; setLibraries(loaded); const policies = await Promise.all(loaded.map((library) => api.scanPolicy(library.id).then((value) => value.policy))); setScanPolicies(Object.fromEntries(policies.map((policy) => [policy.library_id, policy]))); }).catch((error) => { if (error instanceof ApiError && error.code === 'owner_required') setOwnerRequired(true); else setNotice('Libraries are unavailable.'); });
    api.scanJobs().then((result) => setScanJobs(result.jobs ?? [])).catch(() => undefined);
    api.tmdbSettings().then(setTMDBSettings).catch(() => setNotice('TMDB settings are unavailable.'));
    api.unmatchedMetadata().then((result) => setUnmatched(result.items ?? [])).catch(() => undefined);
    loadIdentityRepairs();
    api.playbackSettings().then(setPlaybackSettings).catch(() => setNotice('Playback settings are unavailable.'));
    api.playbackStatus().then(setPlaybackStatus).catch(() => setNotice('Playback status is unavailable.'));
    api.ownerScreens().then((result) => setScreens(result.screens ?? [])).catch(() => setNotice('Connected screens are unavailable.'));
    api.settingsInventory().then((result) => setLocked(new Set((result.settings ?? []).filter((setting) => setting.source === 'environment').map((setting) => setting.key)))).catch(() => undefined);
    api.backupStatus().then((result) => setBackupStatus(result?.policy?.last_status)).catch(() => undefined);
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
  useEffect(() => {
    if (!scanJobs.some((job) => job.status === 'queued' || job.status === 'running')) return;
    const timer = window.setInterval(() => api.scanJobs().then((result) => setScanJobs(result.jobs ?? [])).catch(() => undefined), 1500);
    return () => clearInterval(timer);
  }, [scanJobs]);

  const refreshLibraries = async () => {
    const loaded = (await api.libraries()).libraries ?? [];
    setLibraries(loaded);
    const policies = await Promise.all(loaded.map((library) => api.scanPolicy(library.id).then((value) => value.policy)));
    setScanPolicies(Object.fromEntries(policies.map((policy) => [policy.library_id, policy])));
  };
  const failed = (error: unknown, fallback: string) => setNotice(error instanceof ApiError ? error.message : fallback);
  const libraryActions: LibraryActions = {
    onCreate: async (name, kind) => { try { await api.createLibrary(name, kind); await refreshLibraries(); setNotice(`Library ${name.trim()} created.`); } catch (error) { failed(error, 'Unable to create library.'); } },
    onRename: async (library, name) => { try { await api.renameLibrary(library.id, name); await refreshLibraries(); setNotice('Library renamed.'); } catch (error) { failed(error, 'Unable to rename library.'); } },
    onDelete: async (library) => { try { await api.deleteLibrary(library.id); await refreshLibraries(); setNotice('Empty library deleted.'); } catch (error) { failed(error, 'Unable to delete library.'); } },
    onAddLocation: async (library, path) => { try { await api.addLibraryLocation(library.id, path); await refreshLibraries(); setNotice(`Folder added to ${library.name}.`); } catch (error) { failed(error, 'Unable to add folder.'); } },
    onChangeLocation: async (location, path) => {
      try {
        const preview = await api.previewLibraryLocationChange(location.id, path);
        const confirmed = await confirm({
          title: path ? 'Move this folder?' : 'Remove this folder?',
          message: `${preview.affected_titles} affected titles become unavailable until another location or a fresh scan verifies them. Media files and viewing history are kept.`,
          confirmLabel: path ? 'Move folder' : 'Remove folder',
          danger: !path,
        });
        if (!confirmed) { await api.cancelLibraryLocationChange(preview.id); await refreshLibraries(); return; }
        const result = await api.confirmLibraryLocationChange(preview.id);
        setLibraries(result.libraries ?? []);
        setNotice(path ? 'Library folder moved. Start a scan to verify the new location.' : 'Library folder removed. Media files were untouched and title history was kept.');
      } catch (error) { failed(error, 'Unable to change library folder.'); }
    },
    onConfirmPending: async (location) => {
      if (!location.pending_change_id) return;
      const action = location.pending_path ? `move this folder to ${location.pending_path}` : 'remove this folder';
      const confirmed = await confirm({ title: location.pending_path ? 'Apply the folder move?' : 'Remove this folder?', message: `${location.pending_change_origin === 'environment' ? 'The server environment requested to ' : 'Apply the pending request to '}${action}. Media files and viewing history are kept.`, confirmLabel: 'Apply change' });
      if (!confirmed) return;
      try {
        const result = await api.confirmLibraryLocationChange(location.pending_change_id);
        setLibraries(result.libraries ?? []);
        setNotice(location.pending_path ? 'Environment-managed folder moved. Start a scan to verify it.' : 'Environment-managed folder removed.');
      } catch (error) { failed(error, 'Unable to apply the pending folder change.'); }
    },
    onCancelPending: async (location) => {
      if (!location.pending_change_id) return;
      try { const result = await api.cancelLibraryLocationChange(location.pending_change_id); setLibraries(result.libraries ?? []); setNotice('Pending folder change discarded.'); }
      catch (error) { failed(error, 'Unable to discard the pending folder change.'); }
    },
    onSavePolicy: async (policy) => { try { const result = await api.saveScanPolicy(policy.library_id, policy); setScanPolicies((current) => ({ ...current, [policy.library_id]: result.policy })); setNotice('Scan settings saved.'); } catch (error) { failed(error, 'Unable to save scan settings.'); } },
    onRunScan: async (libraryID) => { try { const result = await api.queueScan(libraryID); setScanJobs((current) => [result.job, ...current.filter((job) => job.id !== result.job.id)]); setNotice('Library scan queued.'); } catch (error) { failed(error, 'Unable to queue library scan.'); } },
  };
  const mergeQueuedJobs = (jobs?: ScanJob[]) => { if (jobs) setScanJobs((current) => [...jobs, ...current.filter((job) => !jobs.some((queued) => queued.id === job.id))]); };
  const scanActions: ScanActions = {
    onStart: async () => { try { const result = await api.scan(); setScan(result.scan); mergeQueuedJobs(result.jobs); } catch (error) { failed(error, 'Unable to start scan.'); } },
    onConfirmRemovals: async (location) => {
      if (!location.pending_scan_id) return;
      const confirmed = await confirm({ title: `Remove ${location.missing} missing files?`, message: `They will leave the ${location.root_kind === 'film' ? 'Films' : 'TV'} library. Their titles and viewing history remain recoverable.`, confirmLabel: 'Confirm removal', danger: true });
      if (!confirmed) return;
      try { const result = await api.confirmScanRemovals(location.pending_scan_id, location.id); setLocations(result.locations ?? []); setScan(result.scan); setNotice('Library cleanup confirmed. Missing titles remain in your history and lists as unavailable.'); }
      catch (error) { failed(error, 'Unable to confirm library cleanup. Refresh scan status and try again.'); }
    },
    onCancelJob: async (id) => { try { const result = await api.cancelScanJob(id); setScanJobs((current) => current.map((job) => job.id === id ? result.job : job)); } catch (error) { failed(error, 'Unable to cancel scan.'); } },
    onRetry: async (job, files = (job.files ?? []).filter((file) => file.retryable)) => { try { const result = await api.retryScanJob(job.id, files ?? []); setScanJobs((current) => [result.job, ...current]); setNotice(files?.length === 1 ? 'File queued for retry.' : 'Failed files queued for retry.'); } catch (error) { failed(error, 'Unable to retry files.'); } },
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
        mergeQueuedJobs(result.jobs);
        setTMDBSettings({ ...settings, state: 'running', message: 'Metadata enrichment is running with the library scan.' });
        setNotice('TMDB credential verified and saved. Metadata enrichment is running.');
      } catch (error) {
        setNotice(error instanceof ApiError ? `TMDB credential verified and saved. ${error.message} Start a scan from Libraries when ready.` : 'TMDB credential verified and saved. Start a scan from Libraries when ready.');
      }
    } catch (error) { failed(error, 'Unable to save TMDB credential.'); }
  };
  const removeTMDB = async () => { try { setTMDBSettings(await api.removeTMDBToken()); setTMDBToken(''); setNotice('TMDB credential removed.'); } catch (error) { failed(error, 'Unable to remove TMDB credential.'); } };
  const setMetadataEnabled = async (enabled: boolean) => {
    try { setTMDBSettings(await api.setMetadataEnabled(enabled)); setNotice(enabled ? 'Remote metadata enabled.' : 'Remote metadata disabled. Cached artwork and descriptions remain available.'); }
    catch (error) { failed(error, 'Unable to change remote metadata.'); }
  };
  const findMatches = async (item: MetadataTarget) => { try { const result = await api.metadataCandidates(item.kind, item.id); setCandidates((current) => ({ ...current, [item.id]: result.candidates })); } catch (error) { failed(error, 'Matches are unavailable.'); } };
  const selectMatch = async (item: MetadataTarget, candidate: MetadataCandidate) => { try { const matched = await api.matchMetadata(item.kind, item.id, candidate.id); setUnmatched((current) => current.map((value) => value.id === item.id ? matched : value)); setCandidates((current) => { const next = { ...current }; delete next[item.id]; return next; }); setNotice(`Matched ${item.title}.`); } catch (error) { failed(error, 'Unable to save this match.'); } };
  const clearMatch = async (item: MetadataTarget) => { try { await api.unmatchMetadata(item.kind, item.id); setUnmatched((current) => current.map((value) => value.id === item.id ? { ...value, provider_id: '', owner_matched: false } : value)); setNotice(`Unmatched ${item.title}.`); } catch (error) { failed(error, 'Unable to unmatch this title.'); } };
  const identityFailed = (error: unknown, stale: string, fallback: string) => {
    if (error instanceof ApiError && error.code === 'owner_required') setOwnerRequired(true);
    else if (error instanceof ApiError && error.code === 'identity_conflict') { setIdentityActionError(stale); loadIdentityRepairs(false); }
    else setIdentityActionError(error instanceof ApiError ? error.message : fallback);
  };
  const mergeIdentity = async (conflict: IdentityConflict, survivorID: string) => {
    const source = conflict.left.id === survivorID ? conflict.right : conflict.left;
    setIdentityActionError('');
    try {
      const merge = await api.mergeIdentity(conflict.kind, survivorID, source.id);
      setIdentityRepairs((current) => current && { conflicts: current.conflicts.filter((item) => item.id !== conflict.id), merges: [...current.merges.filter((item) => item.id !== merge.id), merge] });
      setNotice(`Identity repair merged ${merge.survivor.title} and ${merge.source.title}. Affected titles: ${merge.survivor.title} and ${merge.source.title}.`);
    } catch (error) { identityFailed(error, 'The repair list changed before this merge. Refresh it to review the current titles and decisions.', 'Unable to merge these titles.'); }
  };
  const unmergeIdentity = async (merge: IdentityMerge) => {
    setIdentityActionError('');
    try {
      const result = await api.unmergeIdentity(merge.id);
      setIdentityRepairs((current) => current && { ...current, merges: current.merges.map((item) => item.id === result.id ? result : item) });
      setNotice(`Unmerged ${result.survivor.title} and ${result.source.title}. Review the retained-state decision below.`);
    } catch (error) { identityFailed(error, 'The repair list changed before this unmerge. Refresh it to review the current titles and decisions.', 'Unable to unmerge these titles.'); }
  };
  const savePlayback = async (event: FormEvent) => {
    event.preventDefault();
    if (!playbackSettings) return;
    try {
      const result = await api.savePlaybackSettings(playbackSettings);
      setNotice(result.restart_required ? 'Playback settings saved. Restart Flixr to use the new segment directory.' : 'Playback settings saved.');
      setPlaybackStatus(await api.playbackStatus());
    } catch (error) { failed(error, 'Unable to save playback settings.'); }
  };
  const spy = useScrollSpy({ sections: ownerSections, offset: 88 });

  if (ownerRequired) return <main className="owner"><OwnerHeader onBrowse={onBrowse} onLogout={onLogout} /><section className="auth-panel"><h1>Owner sign in required</h1><p>Your owner session has expired or this device has not signed in.</p><a className="button-link primary" href="/login">Sign in to administer Flixr</a></section></main>;

  const toolsReady = readiness ? readiness.ffprobe && readiness.ffmpeg : undefined;
  const reviewCount = unmatched.filter((item) => !item.provider_id).length + (identityRepairs?.conflicts.filter((conflict) => conflict.state === 'open').length ?? 0) + locations.filter((location) => location.state === 'review_required').length;
  const activeScans = scanJobs.filter((job) => job.status === 'queued' || job.status === 'running').length;
  const overview: Array<{ id: string; label: string; value: string; state?: 'healthy' | 'warning' }> = [
    { id: 'libraries', label: 'Local tools', value: toolsReady === undefined ? 'Checking…' : toolsReady ? 'Ready' : 'Need attention', state: toolsReady === undefined ? undefined : toolsReady ? 'healthy' : 'warning' },
    { id: 'libraries', label: 'Scanning', value: scan?.status === 'running' || activeScans > 0 ? `${Math.max(activeScans, 1)} running` : scan ? scan.status : 'Idle', state: scan?.status === 'running' || activeScans > 0 ? 'healthy' : scan?.status === 'failed' ? 'warning' : undefined },
    { id: 'playback', label: 'Screens', value: screens.length === 1 ? '1 connected' : `${screens.length} connected`, state: screens.length ? 'healthy' : undefined },
    { id: 'backups', label: 'Backups', value: backupStatus === undefined ? 'Checking…' : backupStatus === 'never' ? 'None yet' : backupStatus === 'succeeded' ? 'Verified' : backupStatus, state: backupStatus === 'succeeded' ? 'healthy' : backupStatus === 'failed' ? 'warning' : undefined },
    { id: 'metadata', label: 'Needs review', value: reviewCount === 0 ? 'All clear' : `${reviewCount} ${reviewCount === 1 ? 'item' : 'items'}`, state: reviewCount === 0 ? 'healthy' : 'warning' },
  ];

  return (
    <main className="owner">
      <OwnerHeader onBrowse={onBrowse} onLogout={onLogout} />
      <div className="owner-intro"><p className="eyebrow">YOUR SERVER</p><h1>Server settings</h1><p>Manage your libraries, household, and playback from this device.</p></div>
      <ul className="owner-overview" aria-label="Server status">{overview.map((tile) => <li key={tile.label}><a {...spy.getLinkProps(tile.id)} aria-current={undefined} className="overview-tile"><span className="overview-label">{tile.label}</span><span className="overview-value"><span className={`status-dot ${tile.state === 'healthy' ? 'is-healthy' : tile.state === 'warning' ? 'is-warning' : ''}`} aria-hidden="true" />{tile.value}</span></a></li>)}</ul>
      <OwnerNotice message={notice} onDismiss={() => setNotice('')} />
      <div className="owner-layout">
        <nav className="owner-nav" aria-label="Server settings">{ownerSections.map((section) => <a key={section.id} {...spy.getLinkProps(section.id)}>{section.label}</a>)}</nav>
        <div className="owner-sections">
          <section id="libraries" className="owner-section" aria-labelledby="libraries-title">
            <h2 id="libraries-title">Libraries</h2><p>Folders are read from this server. Your original media stays untouched.</p>
            <LibrariesSection readiness={readiness} onRecheck={recheck} libraries={libraries} locked={locked} policies={scanPolicies} scheduleLocked={locked.has('background.scan_schedule')} scan={scan} locations={locations} jobs={scanJobs} actions={libraryActions} scanActions={scanActions} />
          </section>
          <section id="household" className="owner-section" aria-labelledby="household-title">
            <h2 id="household-title">Household</h2><p>Who can watch, and what they can see. Each profile has its own list, viewing progress, and optional PIN.</p>
            <HouseholdPanel libraries={libraries} onNotice={setNotice} />
          </section>
          <section id="playback" className="owner-section" aria-labelledby="playback-title">
            <div className="section-head"><div><h2 id="playback-title">Playback & screens</h2><p>Compatible files play directly. FFmpeg handles files that need conversion.</p></div><LoadingButton className="quiet-button" onAction={refreshActivity} pendingLabel="Refreshing…" successLabel="Refresh activity">Refresh activity</LoadingButton></div>
            <ConnectedScreens screens={screens} />
            {playbackSettings && <PlaybackPanel settings={playbackSettings} status={playbackStatus} onChange={setPlaybackSettings} onSubmit={savePlayback} locked={locked} />}
          </section>
          <section id="metadata" className="owner-section" aria-labelledby="metadata-title">
            <h2 id="metadata-title">Metadata</h2><p>Optional online artwork and descriptions. Browsing and playback work without a provider.</p>
            <ProviderStatus settings={tmdbSettings} enabled={tmdbSettings?.enabled ?? true} locked={locked.has('metadata.enabled')} onToggle={(enabled) => void setMetadataEnabled(enabled)} />
            <TMDBForm settings={tmdbSettings} token={tmdbToken} onTokenChange={setTMDBToken} onSave={saveTMDB} onRemove={removeTMDB} locked={locked.has('metadata.tmdb_token')} />
            <EpisodeOrder />
            <MetadataRepair items={unmatched} candidates={candidates} onFind={findMatches} onSelect={selectMatch} onClear={clearMatch} onUpdate={(updated) => setUnmatched((current) => current.map((item) => item.id === updated.id ? updated : item))} />
            <MediaVersions confirm={confirm} onOwnerRequired={requireOwner} />
            <IdentityRepair repairs={identityRepairs} error={identityError} actionError={identityActionError} confirm={confirm} onReload={() => loadIdentityRepairs()} onMerge={mergeIdentity} onUnmerge={unmergeIdentity} />
          </section>
          <section id="backups" className="owner-section" aria-labelledby="backups-title">
            <h2 id="backups-title">Backups</h2><p>Verified copies of profiles, viewing history, settings, metadata decisions, and original downloaded artwork. Media files and temporary playback data are excluded.</p>
            <BackupPanel />
          </section>
          <section id="system" className="owner-section" aria-labelledby="system-title">
            <h2 id="system-title">System</h2><p>Effective configuration, diagnostics, and credits.</p>
            <SettingsPanel onNotice={setNotice} />
            <div className="owner-panel" aria-labelledby="support-title">
              <div className="panel-heading"><div><h3 id="support-title">Support diagnostics</h3><p>Download a local ZIP with versions, runtime health, and recent failure IDs. It never includes media names, paths, passwords, or tokens.</p></div><a className="button-link" href="/api/v1/owner/diagnostics" download>Download diagnostics</a></div>
            </div>
            <div className="owner-panel credits">
              <h3>Credits</h3><a href="https://www.themoviedb.org" aria-label="Visit TMDB"><img src="/tmdb-logo.svg" alt="TMDB" width="140" /></a><p className="muted">This product uses the TMDB API but is not endorsed or certified by TMDB.</p>
            </div>
          </section>
        </div>
      </div>
      {dialog}
    </main>
  );
}

function OwnerNotice({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  useEffect(() => {
    if (!message) return;
    const timer = window.setTimeout(onDismiss, 8000);
    return () => window.clearTimeout(timer);
  }, [message, onDismiss]);
  // The live region always exists so screen readers announce each update.
  return <div className={`owner-toast ${message ? 'is-visible' : ''}`} role="status" aria-live="polite">{message && <><span>{message}</span><button type="button" className="owner-toast-dismiss" onClick={onDismiss} aria-label="Dismiss notice">×</button></>}</div>;
}

function OwnerHeader({ onBrowse, onLogout }: OwnerProps) {
  return (
    <header className="owner-header">
      <Wordmark />
      <span className="eyebrow">OWNER</span>
      <button className="quiet-button" onClick={onBrowse}>Who’s watching</button>
      <button className="quiet-button" onClick={() => void api.logout().finally(onLogout)}>Log out</button>
    </header>
  );
}
