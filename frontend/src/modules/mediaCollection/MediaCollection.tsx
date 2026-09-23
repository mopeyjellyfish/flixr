import { Artwork } from '../ui/Feedback';
import { useLayoutEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { CSSProperties, KeyboardEventHandler, RefObject } from 'react';
import type { CatalogItem, ViewerItem } from '../../core/api';

type Open = (item: ViewerItem, opener: HTMLButtonElement) => void;
type Dismiss = (item: ViewerItem) => void;
type Layout = 'rail' | 'grid';

export function MediaCollection({ layout, label, items, onOpen, onDismiss, onLoadMore, pageState }: { layout: Layout; label: string; items: ViewerItem[]; onOpen: Open; onDismiss?: Dismiss; onLoadMore?: () => void; pageState?: 'loading' | 'failure' | 'complete' }) {
  return layout === 'grid' ? <Grid label={label} items={items} onOpen={onOpen} onLoadMore={onLoadMore} pageState={pageState} /> : <Rail label={label} items={items} onOpen={onOpen} onDismiss={onDismiss} onLoadMore={onLoadMore} pageState={pageState} />;
}

export function focusMediaItem(id: string) {
  const target = Array.from(document.querySelectorAll<HTMLButtonElement>('[data-catalog-id]')).find((button) => button.dataset.catalogId === id);
  target?.focus();
  return Boolean(target);
}

function useCollectionSize(ref: RefObject<HTMLElement | null>, fallback: number) {
  const [size, setSize] = useState({ width: fallback, unit: 16 });
  useLayoutEffect(() => {
    const node = ref.current;
    if (!node) return;
    const measure = () => { const styles = getComputedStyle(node); const padding = Number.parseFloat(styles.paddingLeft || '0') + Number.parseFloat(styles.paddingRight || '0'); setSize({ width: Math.max(0, node.clientWidth - padding) || fallback, unit: Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16 }); };
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [fallback, ref]);
  return size;
}

function cardWidth(containerWidth: number, unit: number) { return Math.max(9.0625 * unit, Math.min(13.75 * unit, containerWidth * 0.16)); }

function Grid({ label, items, onOpen, onLoadMore, pageState }: { label: string; items: ViewerItem[]; onOpen: Open; onLoadMore?: () => void; pageState?: 'loading' | 'failure' | 'complete' }) {
  const parentRef = useRef<HTMLElement>(null);
  const { width, unit } = useCollectionSize(parentRef, 1000);
  const gap = unit;
  const minimum = (width < 43.75 * unit ? 9.0625 : 11.25) * unit;
  const columns = Math.max(2, Math.floor((width + gap) / (minimum + gap)));
  const itemWidth = (width - gap * (columns - 1)) / columns;
  const rowHeight = itemWidth * 1.5 + gap;
  const rows = Math.ceil(items.length / columns);
  const virtualizer = useVirtualizer({ count: rows, getScrollElement: () => parentRef.current, estimateSize: () => rowHeight, overscan: 1, initialRect: { width, height: 900 } });
  useLayoutEffect(() => virtualizer.measure(), [rowHeight, virtualizer]);
  const visible = virtualizer.getVirtualItems();
  const rowItems = visible.length ? visible : Array.from({ length: Math.min(rows, 4) }, (_, index) => ({ index, start: index * rowHeight }));
  return <section className="media-grid" aria-label={label} ref={parentRef} data-collection data-layout="grid" data-columns={columns} data-last-id={items.at(-1)?.id}><div className="media-grid-inner" style={{ height: virtualizer.getTotalSize() }}>{rowItems.flatMap((row) => items.slice(row.index * columns, row.index * columns + columns).map((item, column) => <PosterCard key={`${item.kind}:${item.id}`} item={item} width={itemWidth} onOpen={onOpen} onKeyDown={moveFocus} style={{ top: row.start, left: column * (itemWidth + gap), width: itemWidth }} />))}</div><PageControl label={label} onLoadMore={onLoadMore} state={pageState} /></section>;
}

function Rail({ label, items, onOpen, onDismiss, onLoadMore, pageState }: { label: string; items: ViewerItem[]; onOpen: Open; onDismiss?: Dismiss; onLoadMore?: () => void; pageState?: 'loading' | 'failure' | 'complete' }) {
  const parentRef = useRef<HTMLDivElement>(null);
  const { width, unit } = useCollectionSize(parentRef, 1000);
  const gap = unit;
  const itemWidth = cardWidth(width, unit);
  const stride = itemWidth + gap;
  const virtualizer = useVirtualizer({ horizontal: true, count: items.length, getScrollElement: () => parentRef.current, estimateSize: () => stride, overscan: 3, initialRect: { width, height: itemWidth * 1.5 + 2 * unit } });
  useLayoutEffect(() => virtualizer.measure(), [stride, virtualizer]);
  const visible = virtualizer.getVirtualItems();
  const cards = visible.length ? visible.map((virtual) => ({ index: virtual.index, start: virtual.start })) : items.slice(0, Math.ceil(width / stride) + 3).map((_, index) => ({ index, start: index * stride }));
  return <section aria-label={label} data-rail-section><div className="section-heading"><h2>{label}</h2><div className="rail-controls"><span>{items.length} loaded title{items.length === 1 ? '' : 's'}</span><button aria-label={`Scroll ${label} left`} onClick={() => parentRef.current?.scrollBy({ left: -width * 0.85, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })}>‹</button><button aria-label={`Scroll ${label} right`} onClick={() => parentRef.current?.scrollBy({ left: width * 0.85, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })}>›</button></div></div>{items.length ? <div className="rail" ref={parentRef} data-collection data-layout="rail" data-columns="1" data-last-id={items.at(-1)?.id} style={{ height: itemWidth * 1.5 + 2 * unit }}><div className="rail-inner" style={{ width: virtualizer.getTotalSize() }}>{cards.map((virtual) => {
    const item = items[virtual.index];
    return onDismiss ? <div className="curatable-card" key={`${item.kind}:${item.id}`} style={{ left: virtual.start, width: itemWidth }}><PosterCard item={item} width={itemWidth} style={{ left: 0, width: itemWidth }} onKeyDown={moveFocus} onOpen={onOpen} /><button className="card-dismiss" aria-label={`Hide ${item.title} from Continue Watching`} onClick={() => onDismiss(item)}>×</button></div> : <PosterCard key={`${item.kind}:${item.id}`} item={item} width={itemWidth} style={{ left: virtual.start, width: itemWidth }} onKeyDown={moveFocus} onOpen={onOpen} />;
  })}</div></div> : <p className="empty-row">No titles yet.</p>}<PageControl label={label} onLoadMore={onLoadMore} state={pageState} /></section>;
}

function PageControl({ label, onLoadMore, state }: { label: string; onLoadMore?: () => void; state?: 'loading' | 'failure' | 'complete' }) {
  if (!onLoadMore && state !== 'complete') return null;
  return <div><button data-page-control aria-disabled={state === 'loading' || !onLoadMore} onClick={state === 'loading' ? undefined : onLoadMore} onKeyDown={(event) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowUp') return;
    const section = event.currentTarget.closest('section');
    const cards = section?.querySelectorAll<HTMLButtonElement>('button[data-card]');
    const target = cards?.[cards.length - 1];
    if (target) { event.preventDefault(); target.focus(); }
  }}>{state === 'complete' ? `All ${label} loaded` : state === 'loading' ? `Loading more ${label}…` : state === 'failure' ? `Retry ${label}` : `Load more ${label}`}</button>{state === 'failure' && <p role="alert">More titles could not be loaded.</p>}</div>;

}

function moveFocus(event: React.KeyboardEvent<HTMLButtonElement>) {
  const collection = event.currentTarget.closest<HTMLElement>('[data-collection]');
  if (!collection) return;
  const cards = Array.from(collection.querySelectorAll<HTMLButtonElement>('button[data-card]'));
  const cardIndex = cards.indexOf(event.currentTarget);
  if (collection.dataset.layout === 'rail' && (event.key === 'ArrowUp' || event.key === 'ArrowDown')) {
    const root = collection.closest('main');
    const sections = Array.from(root?.querySelectorAll<HTMLElement>('[data-rail-section]') ?? []);
    const current = collection.closest<HTMLElement>('[data-rail-section]');
    const step = event.key === 'ArrowDown' ? 1 : -1;
    let target: HTMLButtonElement | undefined;
    for (let index = sections.indexOf(current!) + step; index >= 0 && index < sections.length; index += step) {
      const cards = Array.from(sections[index].querySelectorAll<HTMLButtonElement>('button[data-card]'));
      target = cards[Math.min(cardIndex, cards.length - 1)] ?? sections[index].querySelector<HTMLButtonElement>('[data-page-control]') ?? undefined;
      if (target) break;
    }
    target ??= event.key === 'ArrowUp' ? root?.querySelector<HTMLButtonElement>('header nav [aria-current="page"], header nav button') ?? undefined : undefined;
    if (target) { event.preventDefault(); target.focus(); }
    return;
  }
  const columns = Number(collection.dataset.columns || 1);
  const delta = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : collection.dataset.layout === 'grid' && event.key === 'ArrowDown' ? columns : collection.dataset.layout === 'grid' && event.key === 'ArrowUp' ? -columns : 0;
  const next = cards[cardIndex + delta];
  if (delta && next) { event.preventDefault(); next.focus(); return; }
  if (delta > 0 && collection.dataset.lastId === event.currentTarget.dataset.catalogId) {
    const control = collection.closest('section')?.querySelector<HTMLButtonElement>('[data-page-control]');
    if (control) { event.preventDefault(); control.focus(); }
  }
}

function PosterCard({ item, width, onOpen, style, onKeyDown }: { item: ViewerItem; width: number; onOpen: Open; style?: CSSProperties; onKeyDown?: KeyboardEventHandler<HTMLButtonElement> }) {
  return <button data-testid={`card-${item.id}`} data-catalog-id={item.id} data-card className="card" style={style} onKeyDown={onKeyDown} onClick={(event) => onOpen(item, event.currentTarget)}>{item.poster ? <Artwork key={item.poster} src={item.poster} width={width} /> : <span className="card-artwork" style={tonalStyle(item.title)} aria-hidden="true" />}<span className="card-scrim" aria-hidden="true" /><span className="card-kind">{kindLabel(item)}</span><strong>{item.title}</strong>{item.edition_label && <small className="card-edition">{item.edition_label}</small>}{item.year && <small>{item.year}</small>}{item.demo && <small className="card-demo">Preview</small>}<span className="card-open" aria-hidden="true">↗</span></button>;
}

function tonalStyle(title?: string) { if (!title) return undefined; let hash = 0; for (const character of title) hash = (hash * 31 + character.charCodeAt(0)) >>> 0; return { backgroundImage: `radial-gradient(circle at ${25 + hash % 60}% ${20 + (hash >>> 8) % 50}%, hsl(${215 + hash % 35} 88% 62% / .52), transparent 42%), linear-gradient(135deg, #101623, #05070c 70%)` }; }
function kindLabel(item: CatalogItem) { return item.kind === 'series' ? 'SERIES' : item.kind === 'episode' ? `EPISODE · S${item.season ?? '?'} E${item.episode ?? '?'}` : 'FILM'; }
