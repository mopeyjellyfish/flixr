import { useEffect, useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type BackupJob, type BackupPolicy } from '../../core/api';
import { Disclosure } from '../../modules/ui/Disclosure';
import { BusyButton } from '../../modules/ui/Feedback';
import { Timestamp } from './Libraries';

const defaults: BackupPolicy = { enabled: false, destination: '', schedule_kind: 'interval', interval_seconds: 86400, local_time: '03:00', timezone: 'UTC', retain_count: 7, retain_age_seconds: 2592000, budget_bytes: 10737418240, last_status: 'never' };

export function BackupPanel() {
  const [policy, setPolicy] = useState<BackupPolicy>(defaults);
  const [jobs, setJobs] = useState<BackupJob[]>([]);
  const [message, setMessage] = useState('');
  const [saving, setSaving] = useState(false);
  const [locked, setLocked] = useState<Set<string>>(new Set());
  const load = () => api.backupStatus().then((result) => { if (!result?.policy) return; setPolicy(result.policy); setJobs(result.jobs ?? []); }).catch((error) => setMessage(error instanceof ApiError ? error.message : 'Backup status is unavailable.'));
  useEffect(() => {
    void load();
    void api.settingsInventory().then((result) => setLocked(new Set((result.settings ?? []).filter((setting) => setting.source === 'environment').map((setting) => setting.key)))).catch(() => undefined);
  }, []);
  useEffect(() => {
    if (!jobs.some((job) => job.status === 'queued' || job.status === 'running')) return;
    const timer = window.setInterval(() => void load(), 1500);
    return () => window.clearInterval(timer);
  }, [jobs]);
  const save = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try { const result = await api.saveBackupPolicy(policy); setPolicy(result.policy); setMessage('Backup policy saved.'); }
    catch (error) { setMessage(error instanceof ApiError ? error.message : 'Unable to save backup policy.'); }
    finally { setSaving(false); }
  };
  const run = async () => {
    try { const result = await api.runBackup(); setJobs((current) => [result.job, ...current]); setMessage('Backup queued.'); }
    catch (error) { setMessage(error instanceof ApiError ? error.message : 'Unable to queue backup.'); }
  };
  const cancel = async (id: string) => {
    try { const result = await api.cancelBackup(id); setJobs((current) => current.map((job) => job.id === id ? result.job : job)); }
    catch (error) { setMessage(error instanceof ApiError ? error.message : 'Unable to cancel backup.'); }
  };
  const update = <K extends keyof BackupPolicy>(key: K, value: BackupPolicy[K]) => setPolicy((current) => ({ ...current, [key]: value }));
  const scheduleLocked = locked.has('backup.schedule');
  const active = jobs.some((job) => job.status === 'queued' || job.status === 'running');

  return <>
    {message && <p role="status" className="field-hint">{message}</p>}
    <div className="owner-card backup-status">
      <div className="card-title"><span className={`status-dot ${policy.last_status === 'succeeded' ? 'is-healthy' : policy.last_status === 'failed' ? 'is-warning' : ''}`} aria-hidden="true" /><p><strong>Last result:</strong> {policy.last_status === 'never' ? 'No verified backup yet.' : policy.last_status}{policy.last_verified_at ? <> · <Timestamp label="" value={policy.last_verified_at} /></> : ''}</p></div>
      {policy.last_message && <p className="muted">{policy.last_message}</p>}
      {policy.next_run_at && <p className="muted"><strong>Next backup:</strong> <Timestamp label="" value={policy.next_run_at} /></p>}
      <div className="actions"><button type="button" className="primary" disabled={active || !policy.destination} onClick={() => void run()}>Back up now</button></div>
      {!policy.destination && <p className="field-hint">Choose a backup folder below before running a backup.</p>}
    </div>
    <form onSubmit={save} className="stack" aria-label="Backup policy">
      <label>Backup folder<input aria-label="Backup folder" disabled={locked.has('backup.destination')} value={policy.destination} placeholder="/backups" onChange={(event) => update('destination', event.target.value)} /></label>
      <label className="choice"><input type="checkbox" disabled={scheduleLocked} checked={policy.enabled} onChange={(event) => update('enabled', event.target.checked)} /> Run backups automatically</label>
      {policy.enabled && <div className="field-row">
        <label>Schedule<select disabled={scheduleLocked} value={policy.schedule_kind} onChange={(event) => update('schedule_kind', event.target.value as BackupPolicy['schedule_kind'])}><option value="interval">Every interval</option><option value="daily">Daily</option></select></label>
        {policy.schedule_kind === 'interval'
          ? <label>Hours between backups<input disabled={scheduleLocked} type="number" min="1" max="8760" inputMode="numeric" value={policy.interval_seconds / 3600} onChange={(event) => update('interval_seconds', Number(event.target.value) * 3600)} /></label>
          : <><label>Local time<input disabled={scheduleLocked} type="time" value={policy.local_time} onChange={(event) => update('local_time', event.target.value)} /></label><label>Timezone<input disabled={scheduleLocked} value={policy.timezone} onChange={(event) => update('timezone', event.target.value)} /></label></>}
      </div>}
      <Disclosure summary="Retention" detail={`Keep ${policy.retain_count} newest · ${Math.round(policy.retain_age_seconds / 86400)} days · ${Math.round(policy.budget_bytes / 1073741824)} GiB budget`}>
        <div className="field-row">
          <label>Keep newest<input disabled={locked.has('backup.retain_count')} type="number" min="1" max="100" inputMode="numeric" value={policy.retain_count} onChange={(event) => update('retain_count', Number(event.target.value))} /></label>
          <label>Keep for days<input disabled={locked.has('backup.retain_age')} type="number" min="1" inputMode="numeric" value={policy.retain_age_seconds / 86400} onChange={(event) => update('retain_age_seconds', Number(event.target.value) * 86400)} /></label>
          <label>Storage budget (GiB)<input disabled={locked.has('backup.budget_bytes')} type="number" min="1" inputMode="numeric" value={Math.round(policy.budget_bytes / 1073741824)} onChange={(event) => update('budget_bytes', Number(event.target.value) * 1073741824)} /></label>
        </div>
      </Disclosure>
      <div className="actions"><BusyButton className="primary" busy={saving}>Save backup policy</BusyButton></div>
    </form>
    <div className="owner-panel">
      <div className="panel-heading"><div><h3>Recent backup activity</h3></div></div>
      {jobs.length === 0 ? <p className="muted">No backup jobs yet.</p> : <ul className="stack card-list">{jobs.slice(0, 8).map((job) => <li key={job.id} className="owner-card job-card">
        <div className="card-head"><div className="card-title"><strong>{job.trigger === 'manual' ? 'Manual' : 'Scheduled'} backup</strong><span className={`pill ${job.status === 'succeeded' ? 'is-locked' : job.status === 'failed' ? 'is-warning' : ''}`}>{job.status}</span>{job.finished_at && <span className="muted"><Timestamp label="" value={job.finished_at} /></span>}</div>{(job.status === 'queued' || job.status === 'running') && <div className="card-tools"><button type="button" className="quiet-button" onClick={() => void cancel(job.id)}>Cancel</button></div>}</div>
        {job.message && <p className="muted">{job.message}</p>}
      </li>)}</ul>}
    </div>
    <Disclosure summary="How to restore">
      <p className="field-hint">Stop Flixr, then run the command below. Flixr verifies the archive before changing data and keeps a safety backup when replacing an installation.</p>
      <pre className="command"><code>flixr restore-backup --data-dir /path/to/flixr-data --archive /path/to/backup.flixr-backup</code></pre>
    </Disclosure>
  </>;
}
