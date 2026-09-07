import { useEffect, useMemo, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type EffectiveSetting } from '../../core/api';

export function SettingsPanel({ onNotice }: { onNotice: (message: string) => void }) {
  const [settings, setSettings] = useState<EffectiveSetting[]>([]);
  const [query, setQuery] = useState('');
  const [advanced, setAdvanced] = useState(false);
  const [importText, setImportText] = useState('');
  const [preview, setPreview] = useState<Record<string, { from: string; to: string }>>();
  const load = () => api.settingsInventory().then((result) => setSettings(result.settings ?? [])).catch(() => onNotice('Settings inventory is unavailable.'));
  useEffect(() => { void load(); }, []);
  const visible = useMemo(() => settings.filter((setting) => (advanced || !setting.advanced) && `${setting.category} ${setting.key} ${setting.environment}`.toLowerCase().includes(query.toLowerCase())), [advanced, query, settings]);
  const resetLibraries = async () => { try { await api.roots('', ''); await load(); onNotice('Library roots reset to their defaults.'); } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Unable to reset library roots.'); } };
  const previewImport = async () => { try { const value: unknown = JSON.parse(importText); const result = await api.settingsImportPreview(value as { version: number; settings: Record<string, string> }); setPreview(result.changes); } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Paste a valid exported settings file.'); } };
  const applyImport = async () => { try { const result = await api.settingsImport(JSON.parse(importText) as { version: number; settings: Record<string, string> }); await load(); onNotice(result.restart_required ? 'Imported settings will apply after restart.' : 'Settings imported.'); setPreview(undefined); } catch (error) { onNotice(error instanceof ApiError ? error.message : 'Unable to import these settings.'); } };
  const exportSettings = async () => { try { setImportText(JSON.stringify(await api.settingsExport(), null, 2)); onNotice('Portable settings export is ready to copy. Secrets are excluded.'); } catch { onNotice('Settings export is unavailable.'); } };
  return <section id="configuration" className="owner-section" aria-labelledby="configuration-title">
    <h2 id="configuration-title">Configuration</h2><p>Environment values take precedence over saved values and are read-only here. Secrets are never included in exports.</p>
    <label>Search settings<input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Library, playback, environment…" /></label>
    <label><input type="checkbox" checked={advanced} onChange={(event) => setAdvanced(event.target.checked)} /> Show advanced settings</label>
    <div className="settings-list">{visible.map((setting) => <article key={setting.key} className="setting-row"><strong>{setting.key}</strong><span>{setting.value}</span><small>{setting.scope} · {setting.source}{setting.environment ? ` · ${setting.environment}` : ''} · {setting.restart}</small>{!setting.mutable && setting.source === 'environment' && <small>Managed by the server environment.</small>}</article>)}</div>
    <div className="actions"><button type="button" onClick={() => void resetLibraries()}>Reset library defaults</button><button type="button" onClick={() => void exportSettings()}>Export portable settings</button></div>
    <details><summary>Import preview</summary><p>Paste a portable export to review changes. Import one library or playback scope at a time; network and access settings always require review.</p><label>Export JSON<textarea value={importText} onChange={(event) => setImportText(event.target.value)} /></label><button type="button" onClick={() => void previewImport()}>Preview changes</button>{preview && <><ul>{Object.entries(preview).map(([key, change]) => <li key={key}>{key}: {change.from || 'default'} → {change.to || 'default'}</li>)}</ul><button type="button" onClick={() => void applyImport()}>Apply previewed changes</button></>}</details>
  </section>;
}
