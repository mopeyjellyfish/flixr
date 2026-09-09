import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type EffectiveSetting } from '../../core/api';

export function SettingsPanel({ onNotice }: { onNotice: (message: string) => void }) {
  const [settings, setSettings] = useState<EffectiveSetting[]>([]);
  const [query, setQuery] = useState('');
  const [advanced, setAdvanced] = useState(false);
  const [importText, setImportText] = useState('');
  const [preview, setPreview] = useState<{ data: { version: number; settings: Record<string, string> }; changes: Record<string, { from: string; to: string }>; restartRequired: boolean }>();
  const importTextRef = useRef('');
  const load = useCallback(() => api.settingsInventory().then((result) => setSettings(result.settings ?? [])).catch(() => onNotice('Settings inventory is unavailable.')), [onNotice]);
  useEffect(() => { void load(); }, [load]);
  const visible = useMemo(() => settings.filter((setting) => (advanced || !setting.advanced) && `${setting.category} ${setting.key} ${setting.environment}`.toLowerCase().includes(query.toLowerCase())), [advanced, query, settings]);
  const libraryLocked = settings.some((setting) => (setting.key === 'library.films_root' || setting.key === 'library.tv_root') && !setting.mutable);
  const changeImportText = (value: string) => { importTextRef.current = value; setImportText(value); setPreview(undefined); };
  const resetLibraries = async () => { try { await api.roots('', ''); await load(); onNotice('Library roots reset to their defaults.'); } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Unable to reset library roots.'); } };
  const previewImport = async () => { const text = importText; try { const data = JSON.parse(text) as { version: number; settings: Record<string, string> }; const result = await api.settingsImportPreview(data); if (importTextRef.current === text) setPreview({ data, changes: result.changes, restartRequired: result.restart_required === true }); } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Paste a valid exported settings file.'); } };
  const applyImport = async () => { if (!preview) return; try { const result = await api.settingsImport(preview.data); await load(); onNotice(result.restart_required ? 'Imported settings will apply after restart.' : 'Settings imported.'); setPreview(undefined); } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Unable to import these settings.'); } };
  const exportSettings = async () => { try { changeImportText(JSON.stringify(await api.settingsExport(), null, 2)); onNotice('Portable settings export is ready to copy. Secrets are excluded.'); } catch { onNotice('Settings export is unavailable.'); } };
  return <section id="configuration" className="owner-section" aria-labelledby="configuration-title">
    <h2 id="configuration-title">Configuration</h2><p>Environment values are read-only here. Changes to populated library folders remain pending until they are reviewed in Named libraries. Secrets are never included in exports.</p>
    <label>Search settings<input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Library, playback, environment…" /></label>
    <label><input type="checkbox" checked={advanced} onChange={(event) => setAdvanced(event.target.checked)} /> Show advanced settings</label>
    <div className="settings-list">{visible.map((setting) => <article key={setting.key} className="setting-row"><strong>{setting.key}</strong><span>{setting.value}</span><small>{setting.scope} · {setting.source}{setting.environment ? ` · ${setting.environment}` : ''} · {setting.restart}</small>{setting.pending_source === 'environment' && <small>Environment requested {setting.pending_value || 'the default'}; review this pending change in Named libraries.</small>}{!setting.mutable && (setting.source === 'environment' || setting.pending_source === 'environment') && <small>Managed by the server environment.</small>}</article>)}</div>
    <div className="actions"><button type="button" disabled={libraryLocked} onClick={() => void resetLibraries()}>Reset library defaults</button><button type="button" onClick={() => void exportSettings()}>Export portable settings</button></div>
    <details><summary>Import preview</summary><p>Paste a portable export to review changes. Import one library or playback scope at a time; network and access settings always require review.</p><label>Export JSON<textarea value={importText} onChange={(event) => changeImportText(event.target.value)} /></label><button type="button" onClick={() => void previewImport()}>Preview changes</button>{preview && <><ul>{Object.entries(preview.changes).map(([key, change]) => <li key={key}>{key}: {change.from || 'default'} → {change.to || 'default'}</li>)}</ul>{preview.restartRequired && <p role="status">Restart required after import.</p>}<button type="button" onClick={() => void applyImport()}>Apply previewed changes</button></>}</details>
  </section>;
}
