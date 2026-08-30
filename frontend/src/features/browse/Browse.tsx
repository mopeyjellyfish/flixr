import { useEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { api } from '../../api/client';
import type { CatalogItem, CatalogPage, SeriesDetail, ViewerItem, ViewerModel, ViewerPreference, ViewerSection } from '../../core/api';
import { moveRailFocus } from '../../modules/focusManager/rail';

type Mode = 'home' | 'movies' | 'tv' | 'search';
type Media = 'all' | 'film' | 'series';
type Detail = (ViewerItem | SeriesDetail) & { listed: boolean };
type BrowseProps = { onExit: () => void; mode?: Mode; detailID?: string; onNavigate?: (path: string) => void; restoreFocusID?: string; onFocusRestored?: () => void };
type OpenDetail = (item: ViewerItem, opener: HTMLButtonElement) => void;

export function Browse({ onExit, mode: initialMode = 'home', detailID, restoreFocusID, onFocusRestored, onNavigate }: BrowseProps) {
  const [mode, setMode] = useState<Mode>(initialMode);
  const [query, setQuery] = useState(new URLSearchParams(window.location.search).get('q') ?? '');
  const [model, setModel] = useState<ViewerModel>();
  const [searchItems, setSearchItems] = useState<ViewerItem[]>([]);
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'failure'>('loading');
  const [detail, setDetail] = useState<Detail>();
  const [retry, setRetry] = useState(0);
  const opener = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const media: Media = mode === 'movies' ? 'film' : mode === 'tv' ? 'series' : 'all';

  useEffect(() => setMode(initialMode), [initialMode]);
  useEffect(() => {
    if (mode === 'search' && !query.trim()) { setSearchItems([]); setState('empty'); return; }
    let active = true;
    const timer = window.setTimeout(() => {
      setState('loading');
      const load = mode === 'search' ? Promise.all([api.search(query), api.viewer('all')]) : api.viewer(media);
      load.then((response) => {
        if (!active) return;
        if (mode === 'search') {
          const [page, viewer] = response as [CatalogPage, ViewerModel];
          const normalized = normalizeViewer(viewer);
          const listed = listedItemIDs(normalized);
          const items = (page.items ?? []).map((item) => ({ ...item, listed: listed.has(item.id) }));
          setSearchItems(items); setState(items.length ? 'ready' : 'empty');
        } else applyViewer(normalizeViewer(response as ViewerModel));
      }).catch(() => active && setState('failure'));
    }, mode === 'search' ? 250 : 0);
    return () => { active = false; window.clearTimeout(timer); };
  }, [media, mode, query, retry]);

  useEffect(() => {
    if (!detailID) { if (dialog.current?.open) dialog.current.close(); setDetail(undefined); return; }
    Promise.all([api.item(detailID), api.viewer('all')]).then(([item, viewer]) => {
      const normalized = normalizeViewer(viewer);
      const listed = listedItemIDs(normalized);
      loadDetail({ ...item, listed: listed.has(item.id) });
    }).catch(() => setDetail(undefined));
  }, [detailID]);
  useEffect(() => { if (detail) window.setTimeout(() => dialog.current?.showModal(), 0); }, [detail]);
  useEffect(() => {
    if (state !== 'ready' || !restoreFocusID) return;
    const timer = window.setTimeout(() => { (Array.from(document.querySelectorAll<HTMLButtonElement>('[data-catalog-id]')).find((button) => button.dataset.catalogId === restoreFocusID) ?? document.querySelector<HTMLButtonElement>('main button'))?.focus(); onFocusRestored?.(); }, 0);
    return () => window.clearTimeout(timer);
  }, [onFocusRestored, restoreFocusID, state]);

  useEffect(() => {
    const node = dialog.current;
    if (!detail || !node) return;
    const cancel = (event: Event) => { event.preventDefault(); close(); };
    node.addEventListener('cancel', cancel);
    return () => node.removeEventListener('cancel', cancel);
  }, [detail, mode, query]);
  const applyViewer = (viewer: ViewerModel) => {
    setModel(viewer);
    setState(viewer.preference.view === 'rows' ? (viewer.sections?.some((section) => section.items.length) ? 'ready' : 'empty') : (viewer.items?.length ? 'ready' : 'empty'));
  };
  const loadDetail = (item: ViewerItem) => { setDetail(item); (item.kind === 'series' ? api.series(item.id) : item.kind === 'film' ? api.film(item.id) : api.item(item.id)).then((loaded) => setDetail({ ...item, ...loaded, listed: item.listed })).catch(() => undefined); };
  const changeMode = (value: Mode) => { setMode(value); onNavigate?.(value === 'search' ? `/search${query ? `?q=${encodeURIComponent(query)}` : ''}` : `/${value === 'home' ? 'home' : value}`); };
  const open: OpenDetail = (item, button) => { opener.current = button; loadDetail(item); onNavigate?.(`/detail/${encodeURIComponent(item.id)}`); };
  const close = () => { dialog.current?.close(); setDetail(undefined); onNavigate?.(mode === 'search' ? `/search?q=${encodeURIComponent(query)}` : `/${mode}`); window.setTimeout(() => opener.current?.focus(), 0); };
  const savePreference = async (preference: ViewerPreference) => { try { await api.saveViewerPreference(media, preference); applyViewer(normalizeViewer(await api.viewer(media))); } catch { setState('failure'); } };
  const changeListed = async () => {
    if (!detail || (detail.kind !== 'film' && detail.kind !== 'series')) return;
    const listed = !detail.listed;
    try {
      await api.setListed(detail.kind, detail.id, listed);
      setDetail({ ...detail, listed });
      setModel((current) => current && updateListed(current, detail.id, listed));
      setSearchItems((current) => current.map((item) => item.id === detail.id ? { ...item, listed } : item));
    } catch { setState('failure'); }
  };
  const items = mode === 'search' ? searchItems : viewerItems(model);
  const focal = items.find((item) => item.backdrop) ?? items[0];
  return <main className="viewer"><header><Wordmark /><nav aria-label="Main navigation"><NavButton label="Home" active={mode === 'home'} onClick={() => changeMode('home')} /><NavButton label="Movies" active={mode === 'movies'} onClick={() => changeMode('movies')} /><NavButton label="TV" active={mode === 'tv'} onClick={() => changeMode('tv')} /></nav><button onClick={() => changeMode('search')}>Search</button><button onClick={onExit}>Switch profile</button></header>
    <Hero focal={focal} mode={mode} query={query} onOpen={open} onQueryChange={(value) => { setQuery(value); onNavigate?.(`/search?q=${encodeURIComponent(value)}`); }} />
    {mode !== 'search' && model && <ViewerControls preference={model.preference} onChange={savePreference} />}
    <CatalogState state={state} mode={mode} query={query} onRetry={() => setRetry((value) => value + 1)} />
    {state === 'ready' && (mode === 'search' ? <Rail label="Search results" items={searchItems} onOpen={open} /> : model?.preference.view === 'grid' ? <PosterGrid items={viewerItems(model)} onOpen={open} /> : <HomeRails sections={model?.sections ?? []} onOpen={open} />)}
    {detail && <DetailDialog detail={detail} dialog={dialog} onClose={close} onPlay={(id) => onNavigate?.(`/play/${encodeURIComponent(id)}`)} onToggleList={() => void changeListed()} onRestoreFocus={() => window.setTimeout(() => opener.current?.focus(), 0)} />}
  </main>;
}

function normalizeViewer(response: ViewerModel): ViewerModel { if (response.preference) return response; const items = ((response as unknown as CatalogPage).items ?? []).map((item) => ({ ...item, listed: false })); return { preference: { view: 'rows', sort: 'title' }, sections: [{ name: 'Films', items: items.filter((item) => item.kind === 'film') }, { name: 'Series', items: items.filter((item) => item.kind === 'series' || item.kind === 'episode') }] }; }
function viewerItems(model?: ViewerModel) { return model?.items ?? model?.sections?.flatMap((section) => section.items) ?? []; }
function listedItemIDs(model: ViewerModel) { return new Set(viewerItems(model).filter((item) => item.listed).map((item) => item.id)); }
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
function Wordmark() { return <b className="wordmark"><span>Flix</span><i>R</i></b>; }
function NavButton({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) { return <button aria-current={active ? 'page' : undefined} className={active ? 'selected' : ''} onClick={onClick}>{label}</button>; }
function Hero({ focal, mode, query, onOpen, onQueryChange }: { focal?: ViewerItem; mode: Mode; query: string; onOpen: OpenDetail; onQueryChange: (value: string) => void }) { return <section className={`hero ${focal?.demo ? 'tonal-artwork' : ''}`} style={focal?.backdrop ? { backgroundImage: `url(${focal.backdrop})` } : tonalStyle(focal?.title)}><p className="eyebrow">{mode === 'home' ? 'HOME' : mode.toUpperCase()}</p>{focal && mode !== 'search' ? <><h1>{focal.title}</h1>{focal.year && <p>{focal.year}</p>}{focal.synopsis && <p>{focal.synopsis}</p>}<button data-catalog-id={focal.id} className="primary" onClick={(event) => onOpen(focal, event.currentTarget)}>View details for {focal.title}</button></> : <h1>{mode === 'search' ? 'Find something local.' : 'Your local cinema.'}</h1>}{mode === 'search' && <label>Search titles<input autoFocus value={query} onChange={(event) => onQueryChange(event.target.value)} /></label>}</section>; }
function tonalStyle(title?: string) { if (!title) return undefined; let hash = 0; for (const character of title) hash = (hash * 31 + character.charCodeAt(0)) >>> 0; return { backgroundImage: `radial-gradient(circle at ${25 + hash % 60}% ${20 + (hash >>> 8) % 50}%, hsl(${215 + hash % 35} 88% 62% / .52), transparent 42%), linear-gradient(135deg, #101623, #05070c 70%)` }; }
function ViewerControls({ preference, onChange }: { preference: ViewerPreference; onChange: (preference: ViewerPreference) => void }) { return <div className="viewer-controls"><label>View<select aria-label="View" value={preference.view} onChange={(event) => onChange({ ...preference, view: event.target.value as ViewerPreference['view'] })}><option value="rows">Rows</option><option value="grid">Grid</option></select></label>{preference.view === 'grid' && <label>Sort<select aria-label="Sort" value={preference.sort} onChange={(event) => onChange({ ...preference, sort: event.target.value as ViewerPreference['sort'] })}><option value="title">Title</option><option value="year">Year</option><option value="added">Recently added</option><option value="watched">Recently watched</option></select></label>}</div>; }
function CatalogState({ state, mode, query, onRetry }: { state: 'loading' | 'ready' | 'empty' | 'failure'; mode: Mode; query: string; onRetry: () => void }) { if (state === 'loading') return <p role="status">Loading catalog…</p>; if (state === 'failure') return <section className="state" role="alert"><h2>Catalog unavailable</h2><button onClick={onRetry}>Try again</button></section>; if (state === 'empty') return <section className="state"><h2>{mode === 'search' ? 'No matching titles' : 'Your library is waiting'}</h2><p>{mode === 'search' ? (query ? 'Try another title.' : 'Enter a title to search your local catalog.') : 'No titles are available yet.'}</p></section>; return null; }
function SeriesEpisodes({ detail, onPlay }: { detail: SeriesDetail; onPlay: (id: string) => void }) { return <section aria-label="Episodes">{[...detail.seasons].sort((a, b) => a.number - b.number).map((season) => <section key={season.id}><h3>Season {season.number}</h3>{[...season.episodes].sort((a, b) => a.episode - b.episode).map((episode) => episode.playable === false ? <p key={episode.id}>{episode.title} · Demo title · no media file</p> : <button key={episode.id} type="button" onClick={() => onPlay(episode.id)}>Play S{episode.season} E{episode.episode} {episode.title}</button>)}</section>)}</section>; }
function kindLabel(item: CatalogItem) { return item.kind === 'series' ? 'SERIES' : item.kind === 'episode' ? `EPISODE · S${item.season ?? '?'} E${item.episode ?? '?'}` : 'FILM'; }
function Rail({ label, items, onOpen }: { label: string; items: ViewerItem[]; onOpen: OpenDetail }) { const parentRef = useRef<HTMLDivElement>(null); const virtualizer = useVirtualizer({ horizontal: true, count: items.length, getScrollElement: () => parentRef.current, estimateSize: () => 236, overscan: 3, initialRect: { width: 1000, height: 320 } }); const visible = virtualizer.getVirtualItems(); const cards = visible.length ? visible.map((virtual) => ({ index: virtual.index, start: virtual.start })) : items.slice(0, 8).map((_, index) => ({ index, start: index * 236 })); return <section aria-label={label}><div className="section-heading"><h2>{label}</h2><span>{items.length} title{items.length === 1 ? '' : 's'}</span></div>{items.length ? <div className="rail" ref={parentRef} data-rail><div className="rail-inner" style={{ width: `${virtualizer.getTotalSize()}px` }}>{cards.map((virtual) => <PosterButton key={items[virtual.index].id} item={items[virtual.index]} style={{ transform: `translateX(${virtual.start}px)` }} onKeyDown={moveRailFocus} onOpen={onOpen} />)}</div></div> : <p className="empty-row">No titles yet.</p>}</section>; }
function PosterGrid({ items, onOpen }: { items: ViewerItem[]; onOpen: OpenDetail }) { return <section className="poster-grid" aria-label="Titles">{items.map((item) => <PosterButton key={item.id} item={item} onOpen={onOpen} />)}</section>; }
function PosterButton({ item, onOpen, ...props }: { item: ViewerItem; onOpen: OpenDetail; style?: React.CSSProperties; onKeyDown?: React.KeyboardEventHandler<HTMLButtonElement> }) { return <button {...props} data-testid={`card-${item.id}`} data-catalog-id={item.id} data-card className={`card ${item.demo ? 'tonal-artwork' : ''}`} style={{ ...props.style, ...(item.poster ? { backgroundImage: `linear-gradient(#05070c22, #05070ccc), url(${item.poster})` } : tonalStyle(item.title)) }} onClick={(event) => onOpen(item, event.currentTarget)}><span>{kindLabel(item)}</span><strong>{item.title}</strong>{item.year && <small>{item.year}</small>}{item.demo && <small>Demo title · no media file</small>}</button>; }
function DetailDialog({ detail, dialog, onClose, onPlay, onToggleList, onRestoreFocus }: { detail: Detail; dialog: React.RefObject<HTMLDialogElement | null>; onClose: () => void; onPlay: (id: string) => void; onToggleList: () => void; onRestoreFocus: () => void }) { const playable = detail.playable !== false; return <dialog ref={dialog} aria-labelledby="detail-title" onClose={onRestoreFocus}><article className="detail"><button autoFocus onClick={onClose}>Close details</button><p className="eyebrow">{kindLabel(detail)}</p><h2 id="detail-title">{detail.title}</h2>{detail.year && <p>{detail.year}</p>}{detail.synopsis && <p>{detail.synopsis}</p>}{detail.demo && <p>Demo title · no media file</p>}{(detail.kind === 'film' || detail.kind === 'episode') && <MediaMetadata item={detail} />}{(detail.kind === 'film' || detail.kind === 'series') && <button onClick={onToggleList}>{detail.listed ? 'Remove from My List' : 'Add to My List'}</button>}{detail.kind === 'series' && 'seasons' in detail && <SeriesEpisodes detail={detail} onPlay={onPlay} />}{(detail.kind === 'film' || detail.kind === 'episode') && playable && <button className="primary" onClick={() => onPlay(detail.id)}>Play {detail.title}</button>}</article></dialog>; }
function MediaMetadata({ item }: { item: CatalogItem }) { const metadata = [item.container, item.video_codec, item.audio_codec].filter(Boolean).join(' · '); return metadata ? <p>Media: {metadata}</p> : null; }
function HomeRails({ sections, onOpen }: { sections: ViewerSection[]; onOpen: OpenDetail }) { return <>{sections.map((section) => <Rail key={section.name} label={section.name} items={section.items} onOpen={onOpen} />)}</>; }
