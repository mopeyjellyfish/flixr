import { useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type IdentityConflict, type IdentityMerge, type IdentityRepairs, type MetadataCandidate, type MetadataField, type MetadataTarget, type TMDBSettings } from '../../core/api';
import { Disclosure } from '../../modules/ui/Disclosure';
import { HoldButton } from '../../modules/ui/HoldButton';
import type { ConfirmOptions } from '../../modules/ui/ConfirmDialog';
import { PendingForm } from './Libraries';

type Confirm = (options: ConfirmOptions) => Promise<boolean>;

const providerStateLabel: Record<TMDBSettings['state'], string> = { unavailable: 'Unavailable', configured: 'Ready', running: 'Enriching', failed: 'Failed', disabled: 'Off', rate_limited: 'Rate limited' };

export function ProviderStatus({ settings, enabled, locked, onToggle }: { settings?: TMDBSettings; enabled: boolean; locked: boolean; onToggle: (enabled: boolean) => void }) {
  const state = settings?.state ?? (settings?.configured ? 'configured' : 'unavailable');
  const message = settings?.message ?? (settings?.configured ? 'Credential configured. Enter a replacement token to change it.' : 'Metadata access is unavailable in this build.');
  const healthy = state === 'configured' || state === 'running';
  return <div className="owner-card provider-card">
    <label className="switch-row">
      <span className="switch-text"><strong>Use online metadata</strong><span className="muted">Artwork and descriptions from TMDB. Downloaded artwork stays on your server.</span></span>
      <input type="checkbox" role="switch" checked={enabled} disabled={locked} onChange={(event) => onToggle(event.target.checked)} />
    </label>
    {locked && <p className="field-hint">Remote metadata policy is managed by the server environment.</p>}
    <p className="provider-state"><span className={`status-dot ${healthy ? 'is-healthy' : state === 'failed' || state === 'rate_limited' ? 'is-warning' : ''}`} aria-hidden="true" /><strong>Provider status: {state} · {settings?.source ?? 'none'}</strong></p>
    <p className="muted provider-message">{providerStateLabel[state as TMDBSettings['state']] ? `${providerStateLabel[state as TMDBSettings['state']]}. ` : ''}{message}</p>
  </div>;
}

export function TMDBForm({ settings, token, onTokenChange, onSave, onRemove, locked }: { settings?: TMDBSettings; token: string; onTokenChange: (value: string) => void; onSave: (event: FormEvent) => void; onRemove: () => Promise<void>; locked: boolean }) {
  const overrideConfigured = settings?.source === 'owner' || settings?.source === 'environment';
  return <Disclosure summary="Advanced: personal TMDB credential" detail={overrideConfigured ? 'Personal credential in use' : 'Using application access'}>
    <PendingForm onSubmit={onSave}>
      <p className="field-hint"><a href="https://www.themoviedb.org/settings/api">Get your TMDB API Read Access Token</a> only if you want to replace automatic application access. Flixr verifies it before saving it.</p>
      {locked && <p className="field-hint">Managed by the server environment.</p>}
      <label>TMDB API Read Access Token<input disabled={locked} type="password" value={token} onChange={(event) => onTokenChange(event.target.value)} autoComplete="new-password" /></label>
      <div className="actions">
        {overrideConfigured && <HoldButton className="danger" disabled={locked} onConfirm={() => void onRemove()} confirmedLabel="Removing…">Remove credential</HoldButton>}
        <button className="primary" disabled={locked}>Save TMDB credential</button>
      </div>
    </PendingForm>
  </Disclosure>;
}

type RepairProps = { items: MetadataTarget[]; candidates: Record<string, MetadataCandidate[]>; onFind: (item: MetadataTarget) => Promise<void>; onSelect: (item: MetadataTarget, candidate: MetadataCandidate) => Promise<void>; onClear: (item: MetadataTarget) => Promise<void>; onUpdate: (item: MetadataTarget) => void };

export function MetadataRepair({ items, candidates, onFind, onSelect, onClear, onUpdate }: RepairProps) {
  return <section className="owner-panel" aria-labelledby="metadata-repair-title">
    <div className="panel-heading"><div><h3 id="metadata-repair-title">Title identification</h3><p>Titles that could not be matched automatically, plus matches you have overridden.</p></div></div>
    {items.length === 0 ? <p className="muted">Nothing needs review.</p> : <ul className="stack card-list">{items.map((item) => <li key={item.id} className="owner-card title-card">
      <div className="card-head">
        <div className="card-title"><strong>{item.title}</strong>{item.year && <span className="muted">{item.year}</span>}<span className={`pill ${item.provider_id ? 'is-locked' : 'is-warning'}`}>{item.provider_id ? 'Matched' : 'Unmatched'}</span></div>
        <div className="card-tools"><button type="button" className="quiet-button" onClick={() => void onFind(item)}>{item.provider_id ? 'Change match' : 'Find matches'}</button>{item.provider_id && <button type="button" className="quiet-button" onClick={() => void onClear(item)}>Unmatch</button>}</div>
      </div>
      {candidates[item.id] && <div className="candidate-list" role="group" aria-label={`Matches for ${item.title}`}>{candidates[item.id].length === 0 ? <p className="muted">No matches found.</p> : candidates[item.id].map((candidate) => <button key={candidate.id} type="button" className="candidate" onClick={() => void onSelect(item, candidate)}><strong>{candidate.title}</strong><span className="muted">{candidate.year ?? 'Unknown year'} · TMDB</span></button>)}</div>}
      <MetadataEditor item={item} onSaved={onUpdate} />
    </li>)}</ul>}
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
  return <Disclosure summary="Edit metadata" open={open} onToggle={(next) => { setOpen(next); if (next && !open) void load(); }}>
    {notice && <p role="status" className="field-hint">{notice}</p>}
    {open && <div className="stack">
      <p className="field-hint">Locked fields keep your value when the provider refreshes.</p>
      {metadataInputs.map(([field, label]) => <div key={field} className="metadata-field">
        <label>{label}{field === 'synopsis' ? <textarea rows={3} value={value(field).value} onChange={(event) => change(field, { value: event.target.value })} /> : <input value={value(field).value} onChange={(event) => change(field, { value: event.target.value })} />}</label>
        <label className="choice"><input type="checkbox" checked={value(field).locked} onChange={(event) => change(field, { locked: event.target.checked })} /> Lock this field</label>
      </div>)}
      <div className="actions">
        <button type="button" className="quiet-button" disabled={!item.provider_id} onClick={() => void previewRefresh()}>Preview provider refresh</button>
        <button type="button" className="quiet-button" disabled={!item.provider_id} onClick={() => void refresh()}>Refresh unlocked fields</button>
        <button type="button" className="quiet-button" onClick={() => void previewChanges()}>Preview changes</button>
        <button type="button" className="primary" onClick={() => void save()}>Save metadata</button>
      </div>
      {preview.length > 0 && <ul className="preview-list" aria-label="Metadata refresh preview">{preview.map((field) => <li key={field.field}><strong>{field.field}</strong> {field.value || 'empty'} <span className="muted">· {field.source}{field.locked ? ' · locked' : ''}</span></li>)}</ul>}
    </div>}
  </Disclosure>;
}

function conflictReason(reason: string) {
  return ({ provider_identity: 'These titles claim the same provider identity.', replacement_evidence: 'Replacement evidence conflicts with the existing title identity.', ambiguous_duplicate: 'These files may be duplicates, but Flixr cannot safely decide.' } as Record<string, string>)[reason] ?? 'These titles have conflicting identities.';
}

type IdentityProps = { repairs?: IdentityRepairs; error: string; actionError: string; confirm: Confirm; onReload: () => void; onMerge: (conflict: IdentityConflict, survivorID: string) => Promise<void>; onUnmerge: (merge: IdentityMerge) => Promise<void> };

export function IdentityRepair({ repairs, error, actionError, confirm, onReload, onMerge, onUnmerge }: IdentityProps) {
  const [survivors, setSurvivors] = useState<Record<number, string>>({});
  const [pending, setPending] = useState('');
  const openConflicts = repairs?.conflicts.filter((conflict) => conflict.state === 'open') ?? [];
  const merge = async (conflict: IdentityConflict) => {
    const survivorID = survivors[conflict.id];
    if (!survivorID) return;
    const survivor = conflict.left.id === survivorID ? conflict.left : conflict.right;
    const source = conflict.left.id === survivorID ? conflict.right : conflict.left;
    if (!await confirm({ title: `Merge ${source.title} into ${survivor.title}?`, message: 'Their viewing history remains recoverable until you choose Unmerge.', confirmLabel: 'Merge titles' })) return;
    setPending(`merge-${conflict.id}`);
    try { await onMerge(conflict, survivorID); } finally { setPending(''); }
  };
  const unmerge = async (item: IdentityMerge) => {
    if (!await confirm({ title: `Unmerge ${item.survivor.title} and ${item.source.title}?`, message: 'Original identities are restored where newer viewing activity has not superseded them.', confirmLabel: 'Unmerge' })) return;
    setPending(`unmerge-${item.id}`);
    try { await onUnmerge(item); } finally { setPending(''); }
  };
  return <section className="owner-panel" aria-labelledby="identity-repair-title" aria-busy={pending ? true : undefined}>
    <div className="panel-heading"><div><h3 id="identity-repair-title">Identity repair</h3><p>Resolve conflicting title identities explicitly. Paths, fingerprints, and credentials are never shown here.</p></div></div>
    {!repairs && !error && <p role="status" className="muted">Loading identity repairs…</p>}
    {!repairs && error && <div className="inline-alert" role="alert"><p>{error}</p><button type="button" onClick={onReload}>Retry identity repairs</button></div>}
    {actionError && <div className="inline-alert" role="alert"><p>{actionError}</p><button type="button" onClick={onReload}>Refresh repair list</button></div>}
    {openConflicts.map((conflict) => <article key={conflict.id} className="owner-card identity-repair">
      <div className="card-title"><strong>Conflict: {conflict.kind}</strong><span className="pill is-warning">Needs a decision</span></div>
      <p className="muted">{conflictReason(conflict.reason)}</p>
      <fieldset className="choice-list"><legend>Choose the title to keep</legend>{[conflict.left, conflict.right].map((item) => <label key={item.id} className="choice"><input type="radio" name={`identity-${conflict.id}`} checked={survivors[conflict.id] === item.id} onChange={() => setSurvivors((current) => ({ ...current, [conflict.id]: item.id }))} /> Keep {item.title}; merge {item.id === conflict.left.id ? conflict.right.title : conflict.left.title} into it</label>)}</fieldset>
      <div className="actions"><button type="button" className="primary" disabled={!survivors[conflict.id] || pending === `merge-${conflict.id}`} onClick={() => void merge(conflict)}>{pending === `merge-${conflict.id}` ? 'Merging titles…' : 'Merge selected titles'}</button></div>
    </article>)}
    {repairs && openConflicts.length === 0 && <p className="muted">No identity conflicts need repair.</p>}
    {repairs?.merges.length ? <Disclosure summary="Identity repair history" detail={`${repairs.merges.length} ${repairs.merges.length === 1 ? 'merge' : 'merges'}`}>
      <div className="stack">{repairs.merges.map((merge) => <article key={merge.id} className="owner-card identity-repair">
        <p><strong>{merge.state === 'unmerged' ? 'Unmerged' : 'Active merge'}:</strong> {merge.survivor.title} keeps identity; {merge.source.title} is the source.</p>
        <p className="muted">Affected titles: {merge.survivor.title} and {merge.source.title}.</p>
        {merge.state === 'unmerged' && <p role="status" className="muted">Unmerge retained the state described in the reconciliation decisions.</p>}
        {merge.decisions.length > 0 && <ul className="decision-list" aria-label={`Reconciliation decisions for ${merge.survivor.title} and ${merge.source.title}`}>{merge.decisions.map((decision) => <li key={decision}>{decision}</li>)}</ul>}
        {merge.state === 'active' && <div className="actions"><button type="button" className="quiet-button" disabled={pending === `unmerge-${merge.id}`} onClick={() => void unmerge(merge)}>{pending === `unmerge-${merge.id}` ? 'Unmerging titles…' : `Unmerge ${merge.survivor.title} and ${merge.source.title}`}</button></div>}
      </article>)}</div>
    </Disclosure> : null}
  </section>;
}
