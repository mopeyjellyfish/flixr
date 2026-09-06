import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../../api/client';
import type { CatalogItem, CatalogPage, SeriesDetail, ViewerItem, ViewerModel, ViewerPreference, ViewerSection } from '../../core/api';
import { focusMediaItem, MediaCollection } from '../../modules/mediaCollection/MediaCollection';
import { Wordmark } from '../../modules/productChrome/Wordmark';
import { ScreenReadiness } from '../../modules/screenCoordinator/ScreenControls';

type Mode = 'home' | 'movies' | 'tv' | 'search';
type Media = 'all' | 'film' | 'series';
type Detail = (ViewerItem | SeriesDetail) & { listed: boolean };
type BrowseProps = { onExit: () => void; mode?: Mode; query?: string; detailID?: string; onNavigate?: (path: string, replace?: boolean) => void; restoreFocusID?: string; onFocusRestored?: () => void };
type OpenDetail = (item: ViewerItem, opener: HTMLButtonElement) => void;

export function Browse({ onExit, mode = 'home', query: routeQuery = '', detailID, restoreFocusID, onFocusRestored, onNavigate }: BrowseProps) {
  const [query, setQuery] = useState(routeQuery);
  const [model, setModel] = useState<ViewerModel>();
  const [searchItems, setSearchItems] = useState<ViewerItem[]>([]);
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'failure'>('loading');
  const [detail, setDetail] = useState<Detail>();
  const [retry, setRetry] = useState(0);
  const opener = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const modelRef = useRef<ViewerModel | undefined>(undefined);
  const requestVersion = useRef(0);
  const detailVersion = useRef(0);
  const openingID = useRef<string | undefined>(undefined);
  const [preferenceError, setPreferenceError] = useState<string>();
  const [listError, setListError] = useState<string>();
  const media: Media = mode === 'movies' ? 'film' : mode === 'tv' ? 'series' : 'all';
  const loadDetail = useCallback((item: ViewerItem, version = ++detailVersion.current) => {
    setDetail(item);
    if (item.kind !== 'series') return;
    api.series(item.id).then((loaded) => { if (version === detailVersion.current) setDetail({ ...item, ...loaded, listed: item.listed }); }).catch(() => undefined);
  }, []);

  useEffect(() => setQuery(routeQuery), [routeQuery]);
  useEffect(() => { modelRef.current = model; }, [model]);
  useEffect(() => {
    if (mode !== 'search') return;
    let active = true;
    api.viewer('all').then((viewer) => { if (active) { modelRef.current = viewer; setModel(viewer); } }).catch(() => undefined);
    return () => { active = false; };
  }, [mode]);
  useEffect(() => {
    if (!model) return;
    const listed = listedItemIDs(model);
    setSearchItems((items) => items.map((item) => item.listed === listed.has(item.id) ? item : { ...item, listed: listed.has(item.id) }));
    setDetail((current) => current && (current.kind === 'film' || current.kind === 'series') && current.listed !== listed.has(current.id) ? { ...current, listed: listed.has(current.id) } : current);
  }, [model]);
  useEffect(() => {
    if (mode === 'search' && !query.trim()) { setSearchItems([]); setState('empty'); return; }
    const version = ++requestVersion.current;
    const timer = window.setTimeout(() => {
      setState('loading');
      const load = mode === 'search' ? api.search(query) : api.viewer(media);
      load.then((response) => {
        if (version !== requestVersion.current) return;
        if (mode === 'search') {
          const listed = listedItemIDs(modelRef.current);
          const items = (response as CatalogPage).items.map((item) => ({ ...item, listed: listed.has(item.id) }));
          setSearchItems(items);
          setState(items.length ? 'ready' : 'empty');
        } else {
          const viewer = response as ViewerModel;
          setModel(viewer);
          setState(viewerState(viewer));
        }
      }).catch(() => { if (version === requestVersion.current) setState('failure'); });
    }, mode === 'search' ? 250 : 0);
    return () => { window.clearTimeout(timer); if (requestVersion.current === version) requestVersion.current += 1; };
  }, [media, mode, query, retry]);
  useEffect(() => {
    if (mode !== 'search' || query === routeQuery) return;
    const timer = window.setTimeout(() => onNavigate?.(`/search?q=${encodeURIComponent(query)}`, true), 250);
    return () => window.clearTimeout(timer);
  }, [mode, onNavigate, query, routeQuery]);

  useEffect(() => {
    if (!detailID) { if (dialog.current?.open) dialog.current.close(); setDetail(undefined); return; }
    if (openingID.current === detailID) { openingID.current = undefined; return; }
    const version = ++detailVersion.current;
    api.item(detailID).then((item) => {
      if (version !== detailVersion.current) return;
      const listed = listedItemIDs(modelRef.current).has(item.id);
      loadDetail({ ...item, listed }, version);
    }).catch(() => { if (version === detailVersion.current) setDetail(undefined); });
    return () => { if (detailVersion.current === version) detailVersion.current += 1; };
  }, [detailID, loadDetail]);
  useEffect(() => { if (detail) window.setTimeout(() => { if (dialog.current && !dialog.current.open) dialog.current.showModal(); }, 0); }, [detail]);
  useEffect(() => {
    if (state !== 'ready' || !restoreFocusID) return;
    const timer = window.setTimeout(() => { if (!focusMediaItem(restoreFocusID)) document.querySelector<HTMLButtonElement>('main button')?.focus(); onFocusRestored?.(); }, 0);
    return () => window.clearTimeout(timer);
  }, [onFocusRestored, restoreFocusID, state]);

  const changeMode = (value: Mode) => onNavigate?.(value === 'search' ? `/search${query ? `?q=${encodeURIComponent(query)}` : ''}` : `/${value === 'home' ? 'home' : value}`);
  const open: OpenDetail = (item, button) => { opener.current = button; if (onNavigate) openingID.current = item.id; setListError(undefined); loadDetail(item); onNavigate?.(`/detail/${encodeURIComponent(item.id)}`); };
  const close = useCallback(() => { dialog.current?.close(); setDetail(undefined); onNavigate?.(mode === 'search' ? `/search?q=${encodeURIComponent(query)}` : `/${mode}`); window.setTimeout(() => opener.current?.focus(), 0); }, [mode, onNavigate, query]);
  useEffect(() => {
    const node = dialog.current;
    if (!detail || !node) return;
    const cancel = (event: Event) => { event.preventDefault(); close(); };
    node.addEventListener('cancel', cancel);
    return () => node.removeEventListener('cancel', cancel);
  }, [close, detail, mode, query]);
  const savePreference = async (preference: ViewerPreference) => { const version = ++requestVersion.current; try { setPreferenceError(undefined); await api.saveViewerPreference(media, preference); const viewer = await api.viewer(media); if (version !== requestVersion.current) return; setModel(viewer); setState(viewerState(viewer)); } catch { if (version === requestVersion.current) setPreferenceError('Flixr could not save this view. Try again.'); } };
  const changeListed = async () => {
    if (!detail || (detail.kind !== 'film' && detail.kind !== 'series')) return;
    const listed = !detail.listed;
    try {
      setListError(undefined);
      await api.setListed(detail.kind, detail.id, listed);
      setDetail({ ...detail, listed });
      setModel((current) => current && updateListed(current, detail.id, listed));
      setSearchItems((current) => current.map((item) => item.id === detail.id ? { ...item, listed } : item));
    } catch { setListError('Flixr could not update My List. Try again.'); }
  };
  const items = mode === 'search' ? searchItems : viewerItems(model);
  const focal = items.find((item) => item.backdrop) ?? items[0];
  return <main className="viewer"><header><Wordmark /><nav aria-label="Main navigation"><NavButton label="Home" active={mode === 'home'} onClick={() => changeMode('home')} /><NavButton label="Movies" active={mode === 'movies'} onClick={() => changeMode('movies')} /><NavButton label="TV" active={mode === 'tv'} onClick={() => changeMode('tv')} /></nav><button onClick={() => changeMode('search')}>Search</button><button onClick={onExit}>Switch profile</button></header>
    <Hero focal={focal} mode={mode} query={query} onOpen={open} onQueryChange={setQuery} />
    {mode !== 'search' && model && <ViewerControls preference={model.preference} error={preferenceError} onChange={savePreference} />}
    <CatalogState state={state} mode={mode} query={query} onRetry={() => setRetry((value) => value + 1)} />
    {state === 'ready' && (mode === 'search' ? <MediaCollection layout="rail" label="Search results" items={searchItems} onOpen={open} /> : model?.preference.view === 'grid' ? <MediaCollection layout="grid" label="Titles" items={viewerItems(model)} onOpen={open} /> : <HomeRails sections={model?.sections ?? []} onOpen={open} />)}
    {detail && <DetailDialog detail={detail} error={listError} dialog={dialog} onClose={close} onPlay={(id) => onNavigate?.(`/play/${encodeURIComponent(id)}`)} onToggleList={() => void changeListed()} onRestoreFocus={() => window.setTimeout(() => opener.current?.focus(), 0)} />}
  </main>;
}

function viewerItems(model?: ViewerModel) { return model?.items ?? model?.sections?.flatMap((section) => section.items) ?? []; }
function listedItemIDs(model?: ViewerModel) { return new Set(viewerItems(model).filter((item) => item.listed).map((item) => item.id)); }
function viewerState(viewer: ViewerModel): 'ready' | 'empty' { return viewer.preference.view === 'rows' ? (viewer.sections?.some((section) => section.items.length) ? 'ready' : 'empty') : (viewer.items?.length ? 'ready' : 'empty'); }
function updateListed(model: ViewerModel, id: string, listed: boolean): ViewerModel {
  const source = viewerItems(model).find((item) => item.id === id);
  const update = (item: ViewerItem) => item.id === id ? { ...item, listed } : item;
  return {
    ...model,
    items: model.items?.map(update),
    sections: model.sections?.map((section) => {
      const items = section.items.map(update);
      if (section.name !== 'My List') return { ...section, items };
      if (!listed) return { ...section, items: items.filter((item) => item.id !== id) };
      return { ...section, items: items.some((item) => item.id === id) || !source ? items : [...items, { ...source, listed: true }] };
    }),
  };
}
function NavButton({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) { return <button aria-current={active ? 'page' : undefined} className={active ? 'selected' : ''} onClick={onClick}>{label}</button>; }
function Hero({ focal, mode, query, onOpen, onQueryChange }: { focal?: ViewerItem; mode: Mode; query: string; onOpen: OpenDetail; onQueryChange: (value: string) => void }) { return <section className={`hero ${focal?.demo ? 'tonal-artwork' : ''}`} style={focal?.backdrop ? { backgroundImage: `url(${focal.backdrop})` } : tonalStyle(focal?.title)}><p className="eyebrow">{mode === 'home' ? 'HOME' : mode.toUpperCase()}</p>{focal && mode !== 'search' ? <><h1>{focal.title}</h1>{focal.year && <p>{focal.year}</p>}{focal.synopsis && <p>{focal.synopsis}</p>}<button data-catalog-id={focal.id} className="primary" onClick={(event) => onOpen(focal, event.currentTarget)}>View details for {focal.title}</button></> : <h1>{mode === 'search' ? 'Find something local.' : 'Your local cinema.'}</h1>}{mode === 'search' && <label>Search titles<input autoFocus value={query} onChange={(event) => onQueryChange(event.target.value)} /></label>}</section>; }
function tonalStyle(title?: string) { if (!title) return undefined; let hash = 0; for (const character of title) hash = (hash * 31 + character.charCodeAt(0)) >>> 0; return { backgroundImage: `radial-gradient(circle at ${25 + hash % 60}% ${20 + (hash >>> 8) % 50}%, hsl(${215 + hash % 35} 88% 62% / .52), transparent 42%), linear-gradient(135deg, #101623, #05070c 70%)` }; }
function ViewerControls({ preference, error, onChange }: { preference: ViewerPreference; error?: string; onChange: (preference: ViewerPreference) => void }) { return <div className="viewer-controls"><label>View<select aria-label="View" value={preference.view} onChange={(event) => onChange({ ...preference, view: event.target.value as ViewerPreference['view'] })}><option value="rows">Rows</option><option value="grid">Grid</option></select></label>{preference.view === 'grid' && <label>Sort<select aria-label="Sort" value={preference.sort} onChange={(event) => onChange({ ...preference, sort: event.target.value as ViewerPreference['sort'] })}><option value="title">Title</option><option value="year">Year</option><option value="added">Recently added</option><option value="watched">Recently watched</option></select></label>}{error && <p role="alert">{error}</p>}</div>; }
function CatalogState({ state, mode, query, onRetry }: { state: 'loading' | 'ready' | 'empty' | 'failure'; mode: Mode; query: string; onRetry: () => void }) { if (state === 'loading') return <p role="status">Loading catalog…</p>; if (state === 'failure') return <section className="state" role="alert"><h2>Catalog unavailable</h2><button onClick={onRetry}>Try again</button></section>; if (state === 'empty') return <section className="state"><h2>{mode === 'search' ? 'No matching titles' : 'Your library is waiting'}</h2><p>{mode === 'search' ? (query ? 'Try another title.' : 'Enter a title to search your local catalog.') : 'No titles are available yet.'}</p></section>; return null; }
function SeriesEpisodes({ detail, onPlay }: { detail: SeriesDetail; onPlay: (id: string) => void }) { return <section aria-label="Episodes">{[...detail.seasons].sort((a, b) => a.number - b.number).map((season) => <section key={season.id}><h3>Season {season.number}</h3>{[...season.episodes].sort((a, b) => a.episode - b.episode).map((episode) => episode.playable === false ? <p key={episode.id}>{episode.title} · Demo title · no media file</p> : <button key={episode.id} type="button" onClick={() => onPlay(episode.id)}>Play S{episode.season} E{episode.episode} {episode.title}</button>)}</section>)}</section>; }
function kindLabel(item: CatalogItem) { return item.kind === 'series' ? 'SERIES' : item.kind === 'episode' ? `EPISODE · S${item.season ?? '?'} E${item.episode ?? '?'}` : 'FILM'; }
function DetailDialog({ detail, error, dialog, onClose, onPlay, onToggleList, onRestoreFocus }: { detail: Detail; error?: string; dialog: React.RefObject<HTMLDialogElement | null>; onClose: () => void; onPlay: (id: string) => void; onToggleList: () => void; onRestoreFocus: () => void }) { const playable = detail.playable !== false; return <dialog ref={dialog} aria-labelledby="detail-title" onClose={onRestoreFocus}><article className="detail"><button autoFocus onClick={onClose}>Close details</button><p className="eyebrow">{kindLabel(detail)}</p><h2 id="detail-title">{detail.title}</h2>{detail.year && <p>{detail.year}</p>}{detail.synopsis && <p>{detail.synopsis}</p>}{detail.demo && <p>Demo title · no media file</p>}{(detail.kind === 'film' || detail.kind === 'episode') && <MediaMetadata item={detail} />}{playable && (detail.kind === 'film' || detail.kind === 'episode') && <ScreenReadiness catalogID={detail.id} />}{(detail.kind === 'film' || detail.kind === 'series') && <><button onClick={onToggleList}>{detail.listed ? 'Remove from My List' : 'Add to My List'}</button>{error && <p role="alert">{error}</p>}</>}{detail.kind === 'series' && 'seasons' in detail && <SeriesEpisodes detail={detail} onPlay={onPlay} />}{(detail.kind === 'film' || detail.kind === 'episode') && playable && <button className="primary" onClick={() => onPlay(detail.id)}>Play {detail.title}</button>}</article></dialog>; }
function MediaMetadata({ item }: { item: CatalogItem }) { const metadata = [item.container, item.video_codec, item.audio_codec].filter(Boolean).join(' · '); return metadata ? <p>Media: {metadata}</p> : null; }
function HomeRails({ sections, onOpen }: { sections: ViewerSection[]; onOpen: OpenDetail }) { return <>{sections.map((section) => <MediaCollection key={section.name} layout="rail" label={section.name} items={section.items} onOpen={onOpen} />)}</>; }
