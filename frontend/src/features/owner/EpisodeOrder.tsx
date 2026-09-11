import { useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type EpisodeOrderDetail, type EpisodeOrderEntry, type EpisodeOrderGroup, type EpisodeOrderKind, type EpisodeOrderPosition, type EpisodeOrderSeries } from '../../core/api';

const kinds: EpisodeOrderKind[] = ['aired', 'dvd', 'absolute'];

function message(error: unknown, fallback: string) { return error instanceof ApiError ? error.message : fallback; }
function cloneEntries(entries: EpisodeOrderEntry[]) { return entries.map((entry) => ({ ...entry, mapping: entry.mapping ? { ...entry.mapping } : undefined })); }
function position(entry: EpisodeOrderEntry): EpisodeOrderPosition {
  return entry.mapping ?? { position: entry.episode, end_position: entry.episode_end, season: entry.season, episode: entry.episode, episode_end: entry.episode_end, special: entry.season === 0 };
}
function entryLabel(entry: EpisodeOrderEntry) { return entry.season === 0 ? `Special · ${entry.title}` : `S${entry.season} · E${entry.episode}${entry.episode_end > entry.episode ? `–${entry.episode_end}` : ''} · ${entry.title}`; }

export function EpisodeOrder() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [series, setSeries] = useState<EpisodeOrderSeries[]>([]);
  const [nextOffset, setNextOffset] = useState<number>();
  const [searchedQuery, setSearchedQuery] = useState('');
  const [selected, setSelected] = useState<EpisodeOrderDetail>();
  const [draft, setDraft] = useState<EpisodeOrderDetail>();
  const [groups, setGroups] = useState<EpisodeOrderGroup[]>([]);
  const [groupID, setGroupID] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState('');
  const requestVersion = useRef(0);
  const clear = () => { requestVersion.current += 1; setSelected(undefined); setDraft(undefined); setSeries([]); setNextOffset(undefined); setSearchedQuery(''); setGroups([]); setGroupID(''); setNotice(''); setBusy(''); };
  const search = async () => {
    const version = ++requestVersion.current;
    setBusy('search'); setNotice('');
    const term = query.trim();
    try { const result = await api.episodeOrderSeries(term); if (version === requestVersion.current) { setSeries(result.series ?? []); setNextOffset(result.next_offset); setSearchedQuery(term); } }
    catch (error) { if (version === requestVersion.current) setNotice(message(error, 'Series discovery is unavailable.')); }
    finally { if (version === requestVersion.current) setBusy(''); }
  };
  const loadMore = async () => {
    if (nextOffset === undefined || searchedQuery !== query.trim()) return;
    const version = ++requestVersion.current;
    setBusy('more'); setNotice('');
    try {
      const result = await api.episodeOrderSeries(searchedQuery, nextOffset);
      if (version === requestVersion.current) {
        setSeries((current) => [...current, ...(result.series ?? []).filter((item) => !current.some((known) => known.id === item.id))]);
        setNextOffset(result.next_offset);
      }
    } catch (error) { if (version === requestVersion.current) setNotice(message(error, 'More series could not be loaded.')); }
    finally { if (version === requestVersion.current) setBusy(''); }
  };
  const load = async (item: EpisodeOrderSeries) => {
    const version = ++requestVersion.current;
    setBusy('load'); setNotice(''); setSelected(undefined); setDraft(undefined); setGroups([]); setGroupID('');
    try { const detail = await api.episodeOrder(item.id); if (version === requestVersion.current) { setSelected(detail); setDraft({ ...detail, entries: cloneEntries(detail.entries) }); } }
    catch (error) { if (version === requestVersion.current) setNotice(message(error, 'This episode order is unavailable.')); }
    finally { if (version === requestVersion.current) setBusy(''); }
  };
  const changeEntry = (id: string, patch: Partial<EpisodeOrderPosition>) => setDraft((current) => current ? { ...current, entries: current.entries.map((entry) => entry.catalog_id === id ? { ...entry, mapping: { ...position(entry), ...patch } } : entry) } : current);
  const loadGroups = async () => {
    if (!selected) return;
    const version = ++requestVersion.current;
    setBusy('groups'); setNotice('');
    try { const result = await api.episodeOrderGroups(selected.series_id); if (version === requestVersion.current) setGroups(result.groups ?? []); }
    catch (error) { if (version === requestVersion.current) setNotice(message(error, 'Provider groups are unavailable.')); }
    finally { if (version === requestVersion.current) setBusy(''); }
  };
  const preview = async () => {
    if (!selected || !groupID) return;
    const version = ++requestVersion.current;
    setBusy('preview'); setNotice('');
    try { const detail = await api.previewEpisodeOrder(selected.series_id, groupID); if (version === requestVersion.current) setDraft({ ...detail, entries: cloneEntries(detail.entries) }); }
    catch (error) { if (version === requestVersion.current) setNotice(message(error, 'Provider mapping preview is unavailable.')); }
    finally { if (version === requestVersion.current) setBusy(''); }
  };
  const save = async () => {
    if (!draft) return;
    const version = ++requestVersion.current;
    setBusy('save'); setNotice('');
    try {
      const saved = await api.saveEpisodeOrder(draft.series_id, draft.order, draft.revision, cloneEntries(draft.entries));
      if (version === requestVersion.current) { setSelected(saved); setDraft({ ...saved, entries: cloneEntries(saved.entries) }); setNotice('Episode order saved.'); }
    } catch (error) { if (version === requestVersion.current) setNotice(message(error, 'Unable to save episode order.')); }
    finally { if (version === requestVersion.current) setBusy(''); }
  };
  return <section className="owner-panel" aria-labelledby="episode-order-title">
    <div className="panel-heading"><div><h3 id="episode-order-title">Episode order</h3><p>Choose the numbers shown for each TV file without changing its filename numbers or viewing history.</p></div></div>
    {!open ? <button type="button" className="quiet-button" onClick={() => setOpen(true)}>Edit episode order</button> : <div className="episode-order-editor">
        <label>Find a series<input disabled={busy === 'save'} value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); void search(); } }} /></label>
        <button type="button" onClick={() => void search()} disabled={busy === 'search' || busy === 'save'}>Find series</button>
        {series.length > 0 && <div className="episode-order-results" role="list" aria-label="Matching series">{series.map((item) => <div key={item.id} role="listitem"><button type="button" disabled={busy === 'save'} onClick={() => void load(item)}>{item.title}</button></div>)}</div>}
        {nextOffset !== undefined && searchedQuery === query.trim() && <button type="button" className="quiet-button" disabled={busy === 'more' || busy === 'save'} onClick={() => void loadMore()}>Load more matching series</button>}
        {busy === 'load' && <p role="status">Loading episode order…</p>}
        {draft && <div className="episode-order-draft">
          <div className="card-head"><div><strong>{draft.title}</strong><p className="muted">Each row is one local file. Save only when its display numbers are ready.</p></div><button type="button" className="quiet-button" disabled={busy === 'save'} onClick={() => selected && void load({ id: selected.series_id, title: selected.title })}>Reload saved order</button></div>
          <label>Order<select disabled={busy === 'save'} value={draft.order} onChange={(event) => setDraft((current) => current ? { ...current, order: event.target.value as EpisodeOrderKind } : current)}>{kinds.map((kind) => <option key={kind} value={kind}>{kind === 'dvd' ? 'DVD' : kind === 'absolute' ? 'Absolute' : 'Aired'}</option>)}</select></label>
          <div className="actions"><button type="button" className="quiet-button" onClick={() => void loadGroups()} disabled={busy === 'groups' || busy === 'save'}>Load provider groups</button>{groups.length > 0 && <label>Provider group<select disabled={busy === 'save'} value={groupID} onChange={(event) => setGroupID(event.target.value)}><option value="">Choose a group</option>{groups.map((group) => <option key={group.id} value={group.id}>{group.name} · {group.order}</option>)}</select></label>}<button type="button" className="quiet-button" disabled={!groupID || busy === 'preview' || busy === 'save'} onClick={() => void preview()}>Preview provider mapping</button></div>
          {draft.needs_repair && <p className="inline-alert" role="alert">This mapping needs repair. You can edit and save it while offline.</p>}
          <div className="episode-order-rows" aria-label="Episode order files">{draft.entries.map((entry) => { const mapped = position(entry); return <article key={entry.catalog_id} className="episode-order-row"><strong>{entryLabel(entry)}</strong><div className="episode-order-fields"><label>Position for {entry.title}<input disabled={busy === 'save'} type="number" min={1} value={mapped.position} onChange={(event) => changeEntry(entry.catalog_id, { position: Number(event.target.value) })} /></label><label>End position for {entry.title}<input disabled={busy === 'save'} type="number" min={1} value={mapped.end_position} onChange={(event) => changeEntry(entry.catalog_id, { end_position: Number(event.target.value) })} /></label><label>Display season for {entry.title}<input disabled={busy === 'save'} type="number" min={0} value={mapped.season} onChange={(event) => changeEntry(entry.catalog_id, { season: Number(event.target.value) })} /></label><label>Display episode for {entry.title}<input disabled={busy === 'save'} type="number" min={1} value={mapped.episode} onChange={(event) => changeEntry(entry.catalog_id, { episode: Number(event.target.value) })} /></label><label>Display end for {entry.title}<input disabled={busy === 'save'} type="number" min={1} value={mapped.episode_end} onChange={(event) => changeEntry(entry.catalog_id, { episode_end: Number(event.target.value) })} /></label><label className="choice"><input disabled={busy === 'save'} type="checkbox" checked={mapped.special} onChange={(event) => changeEntry(entry.catalog_id, { special: event.target.checked })} /> Special</label></div></article>; })}</div>
          <div className="actions"><button type="button" className="primary" disabled={busy === 'save'} onClick={() => void save()}>Save episode order</button><button type="button" className="quiet-button" disabled={busy === 'save'} onClick={() => setDraft((current) => current ? { ...current, order: 'aired', entries: [] } : current)}>Restore parsed aired order</button><button type="button" onClick={() => { setOpen(false); clear(); }}>Cancel order edit</button></div>
        </div>}
        {notice && <p className={notice.includes('saved') ? 'field-hint' : 'inline-alert'} role={notice.includes('saved') ? 'status' : 'alert'}>{notice}</p>}
      </div>}
  </section>;
}
