import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type EffectiveSetting } from '../../core/api';
import { Disclosure } from '../../modules/ui/Disclosure';

type ImportPreview = { data: { version: number; settings: Record<string, string> }; changes: Record<string, { from: string; to: string }>; restartRequired: boolean };

const sourceLabel: Record<EffectiveSetting['source'], string> = { default: 'Default', saved: 'Saved', environment: 'Environment' };

export function SettingsPanel({ onNotice }: { onNotice: (message: string) => void }) {
  const [settings, setSettings] = useState<EffectiveSetting[]>();
  const [query, setQuery] = useState('');
  const [advanced, setAdvanced] = useState(false);
  const [importText, setImportText] = useState('');
  const [preview, setPreview] = useState<ImportPreview>();
  const importTextRef = useRef('');
  const load = useCallback(() => api.settingsInventory().then((result) => setSettings(result.settings ?? [])).catch(() => onNotice('Settings inventory is unavailable.')), [onNotice]);
  useEffect(() => { void load(); }, [load]);
  const groups = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const visible = (settings ?? []).filter((setting) => (advanced || !setting.advanced) && (!needle || `${setting.category} ${setting.key} ${setting.environment}`.toLowerCase().includes(needle)));
    const byCategory = new Map<string, EffectiveSetting[]>();
    for (const setting of visible) byCategory.set(setting.category || 'Other', [...(byCategory.get(setting.category || 'Other') ?? []), setting]);
    return [...byCategory.entries()];
  }, [advanced, query, settings]);
  const advancedCount = (settings ?? []).filter((setting) => setting.advanced).length;
  const libraryLocked = (settings ?? []).some((setting) => (setting.key === 'library.films_root' || setting.key === 'library.tv_root') && !setting.mutable);
  const changeImportText = (value: string) => { importTextRef.current = value; setImportText(value); setPreview(undefined); };
  const resetLibraries = async () => { try { await api.roots('', ''); await load(); onNotice('Library roots reset to their defaults.'); } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Unable to reset library roots.'); } };
  const previewImport = async () => {
    const text = importText;
    try {
      const data = JSON.parse(text) as ImportPreview['data'];
      const result = await api.settingsImportPreview(data);
      if (importTextRef.current === text) setPreview({ data, changes: result.changes, restartRequired: result.restart_required === true });
    } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Paste a valid exported settings file.'); }
  };
  const applyImport = async () => {
    if (!preview) return;
    try { const result = await api.settingsImport(preview.data); await load(); onNotice(result.restart_required ? 'Imported settings will apply after restart.' : 'Settings imported.'); setPreview(undefined); }
    catch (error) { onNotice(error instanceof ApiError ? error.message : 'Unable to import these settings.'); }
  };
  const exportSettings = async () => { try { changeImportText(JSON.stringify(await api.settingsExport(), null, 2)); onNotice('Portable settings export is ready to copy. Secrets are excluded.'); } catch { onNotice('Settings export is unavailable.'); } };

  return <div id="configuration" className="owner-panel" aria-labelledby="configuration-title">
    <div className="panel-heading"><div><h3 id="configuration-title">Configuration</h3><p>Every effective setting and where it comes from. Environment values are read-only here; secrets are never shown or exported.</p></div></div>
    <div className="settings-toolbar">
      <label className="settings-search">Search settings<input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Library, playback, environment…" /></label>
      <label className="choice"><input type="checkbox" checked={advanced} onChange={(event) => setAdvanced(event.target.checked)} /> Show advanced settings{advancedCount > 0 && <span className="muted"> ({advancedCount})</span>}</label>
    </div>
    {groups.length === 0 && <p className="muted">{!settings ? 'Loading settings…' : settings.length === 0 ? 'No settings reported by this server.' : 'No settings match your search.'}</p>}
    <div className="settings-groups">{groups.map(([category, entries]) => <section key={category} className="settings-group" aria-label={category}>
      <h4>{category}</h4>
      <div className="settings-list">{entries.map((setting) => <article key={setting.key} className="setting-row">
        <div className="setting-main"><code className="setting-key">{setting.key}</code><span className="setting-value">{setting.secret ? '••••••••' : setting.value || <em className="muted">default</em>}</span></div>
        <div className="setting-meta"><span className={`pill ${setting.source === 'environment' ? 'is-locked' : ''}`}>{sourceLabel[setting.source]}</span>{setting.restart === 'restart' && <span className="pill">Restart required</span>}{setting.environment && <span className="muted">{setting.environment}</span>}</div>
        {setting.pending_source === 'environment' && <p className="field-hint pending-change">Environment requested {setting.pending_value || 'the default'}; review this pending change in Libraries.</p>}
        {!setting.mutable && (setting.source === 'environment' || setting.pending_source === 'environment') && <p className="field-hint">Managed by the server environment.</p>}
      </article>)}</div>
    </section>)}</div>
    <div className="actions"><button type="button" className="quiet-button" disabled={libraryLocked} onClick={() => void resetLibraries()}>Reset library defaults</button><button type="button" className="quiet-button" onClick={() => void exportSettings()}>Export portable settings</button></div>
    <Disclosure summary="Import preview" detail="Paste a portable export">
      <div className="stack">
        <p className="field-hint">Review changes before they apply. Import one library or playback scope at a time; network and access settings always require review.</p>
        <label>Export JSON<textarea rows={6} value={importText} onChange={(event) => changeImportText(event.target.value)} spellCheck={false} /></label>
        <div className="actions"><button type="button" onClick={() => void previewImport()}>Preview changes</button></div>
        {preview && <>
          <ul className="preview-list">{Object.entries(preview.changes).map(([key, change]) => <li key={key}><code>{key}</code> {change.from || 'default'} → {change.to || 'default'}</li>)}</ul>
          {preview.restartRequired && <p role="status" className="field-hint">Restart required after import.</p>}
          <div className="actions"><button type="button" className="primary" onClick={() => void applyImport()}>Apply previewed changes</button></div>
        </>}
      </div>
    </Disclosure>
  </div>;
}
