import { FeaturedHero } from './FeaturedHero';
import { motion } from 'motion/react';
import { Artwork, CatalogSkeleton } from '../../modules/ui/Feedback';
import { LoadingButton } from '../../vendor/interior/loading-button';
import { useTabs } from '../../vendor/interior/tabs';
import { ShowMore } from '../../vendor/interior/show-more';
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../../api/client';
import type { CatalogItem, CatalogPage, SeriesDetail, ViewerItem, ViewerModel, ViewerPreference, ViewerSection } from '../../core/api';
import { focusMediaItem, MediaCollection } from '../../modules/mediaCollection/MediaCollection';
import { Wordmark } from '../../modules/productChrome/Wordmark';
import { ScreenReadiness } from '../../modules/screenCoordinator/ScreenControls';

type Mode = 'home' | 'movies' | 'tv' | 'search';
type Media = 'all' | 'film' | 'series';
type Detail = (ViewerItem | SeriesDetail) & { listed: boolean };
type BrowseProps = { onReady?: () => void; onExit: () => void; mode?: Mode; query?: string; detailID?: string; onNavigate?: (path: string, replace?: boolean) => void; restoreFocusID?: string; onFocusRestored?: () => void };
type OpenDetail = (item: ViewerItem, opener: HTMLButtonElement) => void;

export function Browse({ onReady, onExit, mode = 'home', query: routeQuery = '', detailID, restoreFocusID, onFocusRestored, onNavigate }: BrowseProps) {
  const [query, setQuery] = useState(routeQuery);
  const [model, setModel] = useState<ViewerModel>();
  const [searchItems, setSearchItems] = useState<ViewerItem[]>([]);
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'failure'>('loading');
  const [detail, setDetail] = useState<Detail>();
  const [detailError, setDetailError] = useState('');
  const [detailLoading, setDetailLoading] = useState(false);
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
    setDetail(item); setDetailError(''); setDetailLoading(item.kind === 'series');
    if (item.kind !== 'series') return;
    api.series(item.id).then((loaded) => { if (version === detailVersion.current) { setDetail({ ...item, ...loaded, listed: item.listed }); setDetailLoading(false); } }).catch(() => { if (version === detailVersion.current) { setDetailError('Episodes could not be loaded. Try again.'); setDetailLoading(false); } });
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
    if (!detailID) { detailVersion.current += 1; setDetailLoading(false); setDetailError(''); if (dialog.current?.open) dialog.current.close(); setDetail(undefined); return; }
    if (openingID.current === detailID) { openingID.current = undefined; return; }
    const version = ++detailVersion.current;
    setDetailLoading(true); setDetailError('');
    api.item(detailID).then((item) => {
      if (version !== detailVersion.current) return;
      const listed = listedItemIDs(modelRef.current).has(item.id);
      loadDetail({ ...item, listed }, version);
    }).catch(() => { if (version === detailVersion.current) { setDetail(undefined); setDetailLoading(false); setDetailError('This title could not be loaded.'); } });
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
  const close = useCallback(() => { detailVersion.current += 1; dialog.current?.close(); setDetail(undefined); setDetailLoading(false); setDetailError(''); onNavigate?.(mode === 'search' ? `/search?q=${encodeURIComponent(query)}` : `/${mode}`); window.setTimeout(() => opener.current?.focus(), 0); }, [mode, onNavigate, query]);
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
    const version = detailVersion.current;
    try {
      setListError(undefined);
      await api.setListed(detail.kind, detail.id, listed);
      if (version === detailVersion.current) setDetail((current) => current?.id === detail.id ? { ...current, listed } : current);
      setModel((current) => current && updateListed(current, detail.id, listed));
      setSearchItems((current) => current.map((item) => item.id === detail.id ? { ...item, listed } : item));
    } catch (error) { setListError('Flixr could not update My List. Try again.'); throw error; }
  };
  const changeWatched = async (kind: 'film' | 'episode' | 'season' | 'series', id: string, watched: boolean, season?: number) => {
    try { setListError(undefined); await api.setWatched(kind, id, watched, season); setRetry((value) => value + 1); } catch { setListError('Flixr could not update watched state. Try again.'); }
  };
  useEffect(() => { if (state !== 'loading') onReady?.(); }, [state, onReady]);
  const items = mode === 'search' ? searchItems : viewerItems(model);
  const focal = items.find((item) => item.backdrop) ?? items[0];
  return <main className="viewer"><header><Wordmark /><nav aria-label="Main navigation"><NavButton label="Home" active={mode === 'home'} onClick={() => changeMode('home')} /><NavButton label="Movies" active={mode === 'movies'} onClick={() => changeMode('movies')} /><NavButton label="TV" active={mode === 'tv'} onClick={() => changeMode('tv')} /><button className="nav-tab" onClick={() => onNavigate?.('/history')}>History</button></nav><button onClick={() => changeMode('search')}>Search</button><button onClick={onExit}>Switch profile</button></header>
    <div className="browse-feature">{mode === 'search' ? <Hero focal={focal} mode={mode} query={query} onOpen={open} onQueryChange={setQuery} onPlay={(id) => onNavigate?.(`/play/${encodeURIComponent(id)}`)} /> : <FeaturedHero key={media} items={model?.sections?.find((section) => section.name === 'New')?.items ?? items} suspended={Boolean(detail) || state === 'loading'} onOpen={open} onPlay={(id) => onNavigate?.(`/play/${encodeURIComponent(id)}`)} />}</div>
    {mode !== 'search' && model && <ViewerControls preference={model.preference} error={preferenceError} onChange={savePreference} />}
    <CatalogState state={state} mode={mode} query={query} onRetry={() => setRetry((value) => value + 1)} />
    {state === 'ready' && (mode === 'search' ? <MediaCollection layout="rail" label="Search results" items={searchItems} onOpen={open} /> : model?.preference.view === 'grid' ? <MediaCollection layout="grid" label="Titles" items={viewerItems(model)} onOpen={open} /> : <HomeRails sections={model?.sections ?? []} onOpen={open} />)}
    {!detail && detailLoading && <p className="detail-notice" role="status">Opening title…</p>}{!detail && detailError && <div className="detail-notice" role="alert">{detailError}<button onClick={close}>Back to library</button></div>}
    {detail && <DetailDialog key={`detail-${detail.id}`} loading={detailLoading} detailError={detailError} onRetry={() => loadDetail(detail)} detail={detail} error={listError} dialog={dialog} onClose={close} onPlay={(id) => onNavigate?.(`/play/${encodeURIComponent(id)}`)} onToggleList={changeListed} onWatched={changeWatched} onRestoreFocus={() => window.setTimeout(() => opener.current?.focus(), 0)} />}
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
function NavButton({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) { return <button aria-current={active ? 'page' : undefined} className={`nav-tab ${active ? 'selected' : ''}`} onClick={onClick}>{label}{active && <motion.span className="nav-indicator" layoutId="browse-navigation" transition={{ type: 'spring', stiffness: 450, damping: 38 }} />}</button>; }
function Hero({ focal, mode, query, onOpen, onQueryChange, onPlay }: { focal?: ViewerItem; mode: Mode; query: string; onOpen: OpenDetail; onQueryChange: (value: string) => void; onPlay: (id: string) => void }) {
  return <section className={`hero ${mode === 'search' ? 'hero-search' : ''} ${focal?.demo ? 'tonal-artwork' : ''}`} style={tonalStyle(focal?.title)}>
    {mode !== 'search' && (focal?.backdrop || focal?.poster) && <Artwork className={`hero-art ${!focal.backdrop ? 'poster-hero' : ''}`} src={focal.backdrop || focal.poster!} priority />}
    <p className="eyebrow">{mode === 'home' ? 'HOME' : mode.toUpperCase()}</p>
    {focal && mode !== 'search' ? <>
      <h1>{focal.title}</h1>
      <p className="hero-meta">{[kindLabel(focal), focal.year, focal.duration_ms ? `${Math.round(focal.duration_ms / 60000)} min` : undefined].filter(Boolean).join(' · ')}</p>
      {focal.synopsis && <p className="hero-synopsis">{focal.synopsis}</p>}
      <div className="hero-actions">
        {focal.playable === true && (focal.kind === 'film' || focal.kind === 'episode') && <button className="primary" onClick={() => onPlay(focal.id)}>Play {focal.title}</button>}
        <button data-catalog-id={focal.id} onClick={(event) => onOpen(focal, event.currentTarget)} aria-label={`View details for ${focal.title}`}><span aria-hidden="true">ⓘ</span> More info</button>
      </div>
    </> : <h1>{mode === 'search' ? 'Find something local.' : 'Your local cinema.'}</h1>}
    {mode === 'search' && <label>Search titles<input autoFocus value={query} onChange={(event) => onQueryChange(event.target.value)} /></label>}
  </section>;
}
function tonalStyle(title?: string) { if (!title) return undefined; let hash = 0; for (const character of title) hash = (hash * 31 + character.charCodeAt(0)) >>> 0; return { backgroundImage: `radial-gradient(circle at ${25 + hash % 60}% ${20 + (hash >>> 8) % 50}%, hsl(${215 + hash % 35} 88% 62% / .52), transparent 42%), linear-gradient(135deg, #101623, #05070c 70%)` }; }
function ViewerControls({ preference, error, onChange }: { preference: ViewerPreference; error?: string; onChange: (preference: ViewerPreference) => void }) { return <div className="viewer-controls"><label>View<select aria-label="View" value={preference.view} onChange={(event) => onChange({ ...preference, view: event.target.value as ViewerPreference['view'] })}><option value="rows">Rows</option><option value="grid">Grid</option></select></label>{preference.view === 'grid' && <label>Sort<select aria-label="Sort" value={preference.sort} onChange={(event) => onChange({ ...preference, sort: event.target.value as ViewerPreference['sort'] })}><option value="title">Title</option><option value="year">Year</option><option value="added">Recently added</option><option value="watched">Recently watched</option></select></label>}{error && <p role="alert">{error}</p>}</div>; }
function CatalogState({ state, mode, query, onRetry }: { state: 'loading' | 'ready' | 'empty' | 'failure'; mode: Mode; query: string; onRetry: () => void }) { if (state === 'loading') return <CatalogSkeleton />; if (state === 'failure') return <section className="state" role="alert"><h2>Catalog unavailable</h2><button onClick={onRetry}>Try again</button></section>; if (state === 'empty') return <section className="state"><h2>{mode === 'search' ? 'No matching titles' : 'Your library is waiting'}</h2><p>{mode === 'search' ? (query ? 'Try another title.' : 'Enter a title to search your local catalog.') : 'No titles are available yet.'}</p></section>; return null; }
function SeriesEpisodes({ detail, onPlay, onWatched }: { detail: SeriesDetail; onPlay: (id: string) => void; onWatched: (season: number, watched: boolean) => void }) {
  const seasons = [...detail.seasons].sort((a, b) => a.number - b.number);
  const tabs = useTabs({ items: seasons.map((season) => ({ value: season.id, label: `Season ${season.number}` })) });
  const selected = seasons.find((season) => season.id === tabs.value) ?? seasons[0];
  return <section className="episodes" aria-label="Episodes"><div className="episode-heading"><h3>Episodes</h3><span>{selected?.episodes.length ?? 0} episodes</span>{selected && <><button onClick={() => onWatched(selected.number, true)}>Mark season watched</button><button onClick={() => onWatched(selected.number, false)}>Start season over</button></>}</div><div className="season-tabs" {...tabs.tabListProps} aria-label="Seasons">{seasons.map((season, index) => <button key={season.id} {...tabs.getTabProps({ value: season.id, label: `Season ${season.number}` }, index)}>Season {season.number}</button>)}</div>{selected && <div {...tabs.getPanelProps(selected.id)}>{[...selected.episodes].sort((a, b) => a.episode - b.episode).map((episode) => <div className="episode-row" key={episode.id}><span className="episode-number">{String(episode.episode).padStart(2, '0')}</span><div><strong>{episode.title}</strong><p>{episode.playable === false ? 'Preview · No media file' : `Season ${episode.season} · Episode ${episode.episode}`}</p></div>{episode.playable !== false && <button aria-label={`Play S${episode.season} E${episode.episode} ${episode.title}`} onClick={() => onPlay(episode.id)}>▶</button>}</div>)}</div>}</section>;
}
function kindLabel(item: CatalogItem) { return item.kind === 'series' ? 'SERIES' : item.kind === 'episode' ? `EPISODE · S${item.season ?? '?'} E${item.episode ?? '?'}` : 'FILM'; }
function DetailDialog({ detail, error, loading, detailError, onRetry, dialog, onClose, onPlay, onToggleList, onWatched, onRestoreFocus }: { detail: Detail; error?: string; loading: boolean; detailError: string; onRetry: () => void; dialog: React.RefObject<HTMLDialogElement | null>; onClose: () => void; onPlay: (id: string) => void; onToggleList: () => Promise<void>; onWatched: (kind: 'film' | 'episode' | 'season' | 'series', id: string, watched: boolean, season?: number) => void; onRestoreFocus: () => void }) {
  const playable = detail.playable !== false;
  return <dialog className="media-dialog" ref={dialog} aria-labelledby="detail-title" onClose={onRestoreFocus} onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}><article className="detail"><div className={`detail-hero ${!detail.backdrop ? 'detail-poster-hero' : ''}`}>
    {(detail.backdrop || detail.poster) && <Artwork src={detail.backdrop || detail.poster!} priority />}
    <button className="detail-close" aria-label="Close details" autoFocus onClick={onClose}>×</button><div className="detail-heading"><p className="eyebrow">{kindLabel(detail)}</p><h2 id="detail-title">{detail.title}</h2><div className="detail-actions">{(detail.kind === 'film' || detail.kind === 'episode') && playable && <button className="primary" onClick={() => onPlay(detail.id)}>▶ Play {detail.title}</button>}<button onClick={() => onWatched(detail.kind === 'series' ? 'series' : detail.kind === 'episode' ? 'episode' : 'film', detail.id, true)}>Mark watched</button><button onClick={() => onWatched(detail.kind === 'series' ? 'series' : detail.kind === 'episode' ? 'episode' : 'film', detail.id, false)}>Mark unwatched</button>{(detail.kind === 'film' || detail.kind === 'series') && <LoadingButton resetAfter={450} className="list-action" onAction={onToggleList} pendingLabel="Saving…" successLabel={detail.listed ? 'Added to My List' : 'Removed from My List'}>{detail.listed ? 'Remove from My List' : 'Add to My List'}</LoadingButton>}</div></div></div>
    <div className="detail-body"><div className="detail-facts">{detail.year && <span>{detail.year}</span>}{detail.genres?.map((genre) => <span key={genre}>{genre}</span>)}{detail.demo && <span className="preview-badge">Metadata preview</span>}</div>{detail.synopsis && <ShowMore lines={4} className="detail-synopsis">{detail.synopsis}</ShowMore>}{(detail.kind === 'film' || detail.kind === 'episode') && <RatingControl key={detail.id} catalogID={detail.id} />}{detail.demo && <p className="detail-note">Demo title · no media file</p>}{error && <p role="alert">{error}</p>}{(detail.kind === 'film' || detail.kind === 'episode') && <MediaMetadata item={detail} />}{playable && (detail.kind === 'film' || detail.kind === 'episode') && <ScreenReadiness catalogID={detail.id} />}{loading && <p role="status">Loading episodes…</p>}{detailError && <div role="alert">{detailError}<button onClick={onRetry}>Try again</button></div>}{detail.kind === 'series' && 'seasons' in detail && <SeriesEpisodes detail={detail} onPlay={onPlay} onWatched={(season, watched) => onWatched('season', detail.id, watched, season)} />}</div></article></dialog>;
}
function MediaMetadata({ item }: { item: CatalogItem }) { const metadata = [item.container, item.video_codec, item.audio_codec].filter(Boolean).join(' · '); return metadata ? <p>Media: {metadata}</p> : null; }
function HomeRails({ sections, onOpen }: { sections: ViewerSection[]; onOpen: OpenDetail }) { return <>{sections.map((section) => <MediaCollection key={section.name} layout="rail" label={section.name} items={section.items} onOpen={onOpen} />)}</>; }

function RatingControl({ catalogID }: { catalogID: string }) {
  const [value, setValue] = useState(0);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(true);
  const version = useRef(0);
  const pending = useRef(true);
  const invalidate = useCallback(() => { version.current++; }, []);
  useEffect(() => {
    const request = ++version.current;
    pending.current = true;
    setBusy(true);
    setValue(0);
    setError('');
    api.rating(catalogID).then(({ rating }) => {
      if (request === version.current) setValue(rating?.value ?? 0);
    }).catch(() => {
      if (request === version.current) setError('Rating unavailable.');
    }).finally(() => {
      if (request === version.current) { pending.current = false; setBusy(false); }
    });
    return invalidate;
  }, [catalogID, invalidate]);
  const save = async (next: number) => {
    if (pending.current) return;
    pending.current = true;
    const request = ++version.current;
    setBusy(true);
    setError('');
    try {
      if (next === 0) await api.deleteRating(catalogID);
      else await api.setRating(catalogID, next);
      if (request === version.current) setValue(next);
    } catch {
      if (request === version.current) setError('Flixr could not save your rating.');
    } finally {
      if (request === version.current) { pending.current = false; setBusy(false); }
    }
  };
  return <label className="rating-control">Your rating<select aria-label="Your rating" value={value} disabled={busy} onChange={(event) => void save(Number(event.target.value))}><option value={0}>Not rated</option>{[1, 2, 3, 4, 5].map(stars => <option key={stars} value={stars}>{stars} star{stars === 1 ? '' : 's'}</option>)}</select>{error && <span>{error}</span>}</label>;
}
