import { useEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { api } from '../../api/client';
import type { CatalogItem, CatalogPage, SeriesDetail } from '../../core/api';
import { moveRailFocus } from '../../modules/focusManager/rail';

type Mode = 'home' | 'search';
type Detail = CatalogItem | SeriesDetail;
type BrowseProps = {
  onExit: () => void;
  mode?: Mode;
  detailID?: string;
  onNavigate?: (path: string) => void;
};
type OpenDetail = (item: CatalogItem, opener: HTMLButtonElement) => void;

export function Browse({ onExit, mode: initialMode = 'home', detailID, onNavigate }: BrowseProps) {
  const [mode, setMode] = useState<Mode>(initialMode);
  const [query, setQuery] = useState(new URLSearchParams(window.location.search).get('q') ?? '');
  const [items, setItems] = useState<CatalogItem[]>([]);
  const [next, setNext] = useState<number | null | undefined>();
  const [total, setTotal] = useState<number>();
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'failure'>('loading');
  const [detail, setDetail] = useState<Detail>();
  const [retry, setRetry] = useState(0);
  const opener = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => setMode(initialMode), [initialMode]);
  const apply = (page: CatalogPage, append = false) => {
    const received = page.items ?? [];
    setItems((current) => append ? [...current, ...received] : received);
    setNext(page.next);
    setTotal(page.total);
    setState((append ? items.length + received.length : received.length) ? 'ready' : 'empty');
  };

  useEffect(() => {
    if (mode === 'search' && !query.trim()) {
      setItems([]);
      setNext(undefined);
      setTotal(undefined);
      setState('empty');
      return;
    }
    let active = true;
    const timer = window.setTimeout(() => {
      setState('loading');
      const load = mode === 'search' ? api.search(query) : api.home();
      load.then((page) => active && apply(page)).catch(() => active && setState('failure'));
    }, mode === 'search' ? 250 : 0);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [mode, query, retry]);

  useEffect(() => {
    if (!detailID) {
      if (dialog.current?.open) dialog.current.close();
      setDetail(undefined);
      return;
    }
    api.item(detailID).then(loadDetail).catch(() => setDetail(undefined));
  }, [detailID]);

  useEffect(() => {
    if (detail) window.setTimeout(() => dialog.current?.showModal(), 0);
  }, [detail]);

  const loadDetail = (item: CatalogItem) => {
    setDetail(item);
    const request = item.kind === 'series' ? api.series(item.id) : item.kind === 'film' ? api.film(item.id) : api.item(item.id);
    request.then(setDetail).catch(() => undefined);
  };
  const changeMode = (value: Mode) => {
    setMode(value);
    onNavigate?.(value === 'home' ? '/home' : `/search${query ? `?q=${encodeURIComponent(query)}` : ''}`);
  };
  const open: OpenDetail = (item, button) => {
    opener.current = button;
    loadDetail(item);
    onNavigate?.(`/detail/${encodeURIComponent(item.id)}`);
  };
  const close = () => {
    dialog.current?.close();
    setDetail(undefined);
    onNavigate?.(mode === 'home' ? '/home' : `/search?q=${encodeURIComponent(query)}`);
    window.setTimeout(() => opener.current?.focus(), 0);
  };

  useEffect(() => {
    const node = dialog.current;
    if (!detail || !node) return;
    const cancel = (event: Event) => {
      event.preventDefault();
      close();
    };
    node.addEventListener('cancel', cancel);
    return () => node.removeEventListener('cancel', cancel);
  }, [detail, mode, query]);

  const loadMore = async () => {
    if (next === undefined || next === null) return;
    try {
      const page = await (mode === 'search' ? api.search(query, next) : api.home(next));
      apply(page, true);
    } catch {
      setState('failure');
    }
  };
  const focal = items.find((item) => item.backdrop) ?? items[0];

  return (
    <main>
      <header>
        <b>FLIXR</b>
        <nav aria-label="Main navigation">
          <button aria-current={mode === 'home' ? 'page' : undefined} className={mode === 'home' ? 'selected' : ''} onClick={() => changeMode('home')}>Home</button>
          <button aria-current={mode === 'search' ? 'page' : undefined} className={mode === 'search' ? 'selected' : ''} onClick={() => changeMode('search')}>Search</button>
        </nav>
        <button onClick={onExit}>Switch profile</button>
      </header>
      <Hero focal={focal} mode={mode} query={query} onOpen={open} onQueryChange={(value) => {
        setQuery(value);
        onNavigate?.(`/search?q=${encodeURIComponent(value)}`);
      }} />
      <CatalogState state={state} mode={mode} query={query} onRetry={() => setRetry((value) => value + 1)} />
      {state === 'ready' && (mode === 'home' ? <HomeRails items={items} onOpen={open} /> : <Rail label="Search results" items={items} onOpen={open} />)}
      {state === 'ready' && <CatalogFooter items={items} total={total} next={next} onLoadMore={() => void loadMore()} />}
      {detail && <DetailDialog detail={detail} dialog={dialog} onClose={close} onRestoreFocus={() => window.setTimeout(() => opener.current?.focus(), 0)} />}
    </main>
  );
}

function Hero({ focal, mode, query, onOpen, onQueryChange }: { focal?: CatalogItem; mode: Mode; query: string; onOpen: OpenDetail; onQueryChange: (value: string) => void }) {
  return <section className="hero" style={focal?.backdrop ? { backgroundImage: `url(${focal.backdrop})` } : undefined}>
    <p className="eyebrow">{mode === 'home' ? 'HOME' : 'SEARCH'}</p>
    {focal && mode === 'home' ? <>
      <h1>{focal.title}</h1>
      {focal.year && <p>{focal.year}</p>}
      <p>{focal.synopsis || (focal.local_only ? 'Local metadata is available offline.' : 'Metadata is ready on this local server.')}</p>
      <p>{focal.local_only ? 'Local-only metadata' : 'Enriched metadata'}</p>
      <button className="primary" onClick={(event) => onOpen(focal, event.currentTarget)}>View details for {focal.title}</button>
    </> : <h1>{mode === 'home' ? 'Your local cinema.' : 'Find something local.'}</h1>}
    {mode === 'search' && <label>Search titles<input autoFocus value={query} onChange={(event) => onQueryChange(event.target.value)} /></label>}
    <p className="quiet-status">Local catalog · playback comes next</p>
  </section>;
}

function CatalogState({ state, mode, query, onRetry }: { state: 'loading' | 'ready' | 'empty' | 'failure'; mode: Mode; query: string; onRetry: () => void }) {
  if (state === 'loading') return <p role="status">Loading catalog…</p>;
  if (state === 'failure') return <section className="state" role="alert"><h2>Catalog unavailable</h2><p>Your local server did not answer. Check the connection and try again.</p><button onClick={onRetry}>Try again</button></section>;
  if (state === 'empty') return <section className="state"><h2>{mode === 'search' ? 'No matching titles' : 'Your library is waiting'}</h2><p>{mode === 'search' ? (query ? 'Try another title.' : 'Enter a title to search your local catalog.') : 'Ask the owner to configure roots and start a scan.'}</p></section>;
  return null;
}

function CatalogFooter({ items, total, next, onLoadMore }: { items: CatalogItem[]; total?: number; next?: number | null; onLoadMore: () => void }) {
  return <div><span>{total === undefined ? `${items.length} loaded` : `${items.length} of ${total} titles`}</span>{next !== null && next !== undefined ? <button className="load-more" onClick={onLoadMore}>Load more titles</button> : <p role="status">End of catalog</p>}</div>;
}

function DetailDialog({ detail, dialog, onClose, onRestoreFocus }: { detail: Detail; dialog: React.RefObject<HTMLDialogElement | null>; onClose: () => void; onRestoreFocus: () => void }) {
  return <dialog ref={dialog} aria-labelledby="detail-title" onClose={onRestoreFocus}><article className="detail">
    <button autoFocus onClick={onClose}>Close details</button>
    <p className="eyebrow">{kindLabel(detail)}</p>
    <h2 id="detail-title">{detail.title}</h2>
    {detail.year && <p>{detail.year}</p>}
    <p>{detail.synopsis || (detail.local_only ? 'Local metadata is available offline. This title may need owner matching review.' : 'Metadata is ready on this local server.')}</p>
    <p>{detail.local_only ? 'Local-only metadata' : 'Enriched metadata'}</p>
    {detail.kind === 'film' && <MediaMetadata item={detail} />}
    {detail.kind === 'series' && 'seasons' in detail && <SeriesEpisodes detail={detail} />}
    <button className="primary" disabled>Playback arrives in the next local update</button>
  </article></dialog>;
}

function MediaMetadata({ item }: { item: CatalogItem }) {
  return <p>Media: {[item.container, item.video_codec, item.audio_codec].filter(Boolean).join(' · ') || 'Local media metadata unavailable'}</p>;
}

function SeriesEpisodes({ detail }: { detail: SeriesDetail }) {
  return <section aria-label="Episodes">{[...detail.seasons].sort((a, b) => a.number - b.number).map((season) => <section key={season.id}><h3>Season {season.number}</h3>{[...season.episodes].sort((a, b) => a.episode - b.episode).map((episode) => <button key={episode.id} type="button">S{episode.season} E{episode.episode} {episode.title}{episode.local_only ? ' · Local metadata' : ''}</button>)}</section>)}</section>;
}

function kindLabel(item: CatalogItem) {
  return item.kind === 'episode' ? `EPISODE · S${item.season ?? '?'} E${item.episode ?? '?'}` : item.kind === 'series' ? 'SERIES' : 'FILM';
}

function Rail({ label, items, onOpen }: { label: string; items: CatalogItem[]; onOpen: OpenDetail }) {
  const parentRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({ horizontal: true, count: items.length, getScrollElement: () => parentRef.current, estimateSize: () => 236, overscan: 3, initialRect: { width: 1000, height: 320 } });
  const visible = virtualizer.getVirtualItems();
  const cards = visible.length ? visible.map((virtual) => ({ index: virtual.index, start: virtual.start })) : items.slice(0, 8).map((_, index) => ({ index, start: index * 236 }));
  return <section aria-label={label}><div className="section-heading"><h2>{label}</h2><span>{items.length} title{items.length === 1 ? '' : 's'}</span></div><div className="rail" ref={parentRef} data-rail><div className="rail-inner" style={{ width: `${virtualizer.getTotalSize()}px` }}>{cards.map((virtual) => {
    const item = items[virtual.index];
    return <button data-testid={`card-${item.id}`} data-card className="card" key={item.id} style={{ transform: `translateX(${virtual.start}px)`, backgroundImage: item.poster ? `linear-gradient(#05070c22, #05070ccc), url(${item.poster})` : undefined }} onKeyDown={moveRailFocus} onClick={(event) => onOpen(item, event.currentTarget)}><span>{kindLabel(item)}</span><strong>{item.title}</strong>{item.year && <small>{item.year}</small>}{item.local_only && <small>Local metadata</small>}</button>;
  })}</div></div></section>;
}

function HomeRails({ items, onOpen }: { items: CatalogItem[]; onOpen: OpenDetail }) {
  const films = items.filter((item) => item.kind === 'film');
  const series = items.filter((item) => item.kind === 'series' || item.kind === 'episode');
  return <>{films.length > 0 && <Rail label="Films" items={films} onOpen={onOpen} />}{series.length > 0 && <Rail label="Series" items={series} onOpen={onOpen} />}</>;
}
