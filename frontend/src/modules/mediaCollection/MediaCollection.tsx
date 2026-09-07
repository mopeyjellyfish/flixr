import { Artwork } from '../ui/Feedback';
import { useLayoutEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { CSSProperties, KeyboardEventHandler, RefObject } from 'react';
import type { CatalogItem, ViewerItem } from '../../core/api';

type Open = (item: ViewerItem, opener: HTMLButtonElement) => void;
type Layout = 'rail' | 'grid';

export function MediaCollection({ layout, label, items, onOpen }: { layout: Layout; label: string; items: ViewerItem[]; onOpen: Open }) {
  return layout === 'grid' ? <Grid label={label} items={items} onOpen={onOpen} /> : <Rail label={label} items={items} onOpen={onOpen} />;
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

function Grid({ label, items, onOpen }: { label: string; items: ViewerItem[]; onOpen: Open }) {
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
  return <section className="media-grid" aria-label={label} ref={parentRef} data-collection data-layout="grid" data-columns={columns}><div className="media-grid-inner" style={{ height: virtualizer.getTotalSize() }}>{rowItems.flatMap((row) => items.slice(row.index * columns, row.index * columns + columns).map((item, column) => <PosterCard key={item.id} item={item} width={itemWidth} onOpen={onOpen} onKeyDown={moveFocus} style={{ top: row.start, left: column * (itemWidth + gap), width: itemWidth }} />))}</div></section>;
}

function Rail({ label, items, onOpen }: { label: string; items: ViewerItem[]; onOpen: Open }) {
  const parentRef = useRef<HTMLDivElement>(null);
  const { width, unit } = useCollectionSize(parentRef, 1000);
  const gap = unit;
  const itemWidth = cardWidth(width, unit);
  const stride = itemWidth + gap;
  const virtualizer = useVirtualizer({ horizontal: true, count: items.length, getScrollElement: () => parentRef.current, estimateSize: () => stride, overscan: 3, initialRect: { width, height: itemWidth * 1.5 + 2 * unit } });
  useLayoutEffect(() => virtualizer.measure(), [stride, virtualizer]);
  const visible = virtualizer.getVirtualItems();
  const cards = visible.length ? visible.map((virtual) => ({ index: virtual.index, start: virtual.start })) : items.slice(0, Math.ceil(width / stride) + 3).map((_, index) => ({ index, start: index * stride }));
  return <section aria-label={label}><div className="section-heading"><h2>{label}</h2><div className="rail-controls"><span>{items.length} title{items.length === 1 ? '' : 's'}</span><button aria-label={`Scroll ${label} left`} onClick={() => parentRef.current?.scrollBy({ left: -width * 0.85, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })}>‹</button><button aria-label={`Scroll ${label} right`} onClick={() => parentRef.current?.scrollBy({ left: width * 0.85, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })}>›</button></div></div>{items.length ? <div className="rail" ref={parentRef} data-collection data-layout="rail" data-columns="1" style={{ height: itemWidth * 1.5 + 2 * unit }}><div className="rail-inner" style={{ width: virtualizer.getTotalSize() }}>{cards.map((virtual) => <PosterCard key={items[virtual.index].id} item={items[virtual.index]} width={itemWidth} style={{ left: virtual.start, width: itemWidth }} onKeyDown={moveFocus} onOpen={onOpen} />)}</div></div> : <p className="empty-row">No titles yet.</p>}</section>;
}

function moveFocus(event: React.KeyboardEvent<HTMLButtonElement>) {
  const collection = event.currentTarget.closest<HTMLElement>('[data-collection]');
  if (!collection) return;
  const cards = Array.from(collection.querySelectorAll<HTMLButtonElement>('button[data-card]'));
  const cardIndex = cards.indexOf(event.currentTarget);
  if (collection.dataset.layout === 'rail' && (event.key === 'ArrowUp' || event.key === 'ArrowDown')) {
    const root = collection.closest('main');
    const rails = Array.from(root?.querySelectorAll<HTMLElement>('[data-collection][data-layout="rail"]') ?? []);
    const targetRail = rails[rails.indexOf(collection) + (event.key === 'ArrowDown' ? 1 : -1)];
    const targetCards = Array.from(targetRail?.querySelectorAll<HTMLButtonElement>('button[data-card]') ?? []);
    const target = targetCards[Math.min(cardIndex, targetCards.length - 1)] ?? (event.key === 'ArrowUp' ? root?.querySelector<HTMLButtonElement>('header nav [aria-current="page"], header nav button') : undefined);
    if (target) { event.preventDefault(); target.focus(); }
    return;
  }
  const columns = Number(collection.dataset.columns || 1);
  const delta = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : collection.dataset.layout === 'grid' && event.key === 'ArrowDown' ? columns : collection.dataset.layout === 'grid' && event.key === 'ArrowUp' ? -columns : 0;
  const next = cards[cardIndex + delta];
  if (delta && next) { event.preventDefault(); next.focus(); }
}

function PosterCard({ item, width, onOpen, style, onKeyDown }: { item: ViewerItem; width: number; onOpen: Open; style?: CSSProperties; onKeyDown?: KeyboardEventHandler<HTMLButtonElement> }) {
  return <button data-testid={`card-${item.id}`} data-catalog-id={item.id} data-card className="card" style={style} onKeyDown={onKeyDown} onClick={(event) => onOpen(item, event.currentTarget)}>{item.poster ? <Artwork key={item.poster} src={item.poster} width={width} /> : <span className="card-artwork" style={tonalStyle(item.title)} aria-hidden="true" />}<span className="card-scrim" aria-hidden="true" /><span className="card-kind">{kindLabel(item)}</span><strong>{item.title}</strong>{item.year && <small>{item.year}</small>}{item.demo && <small className="card-demo">Preview</small>}<span className="card-open" aria-hidden="true">↗</span></button>;
}

function tonalStyle(title?: string) { if (!title) return undefined; let hash = 0; for (const character of title) hash = (hash * 31 + character.charCodeAt(0)) >>> 0; return { backgroundImage: `radial-gradient(circle at ${25 + hash % 60}% ${20 + (hash >>> 8) % 50}%, hsl(${215 + hash % 35} 88% 62% / .52), transparent 42%), linear-gradient(135deg, #101623, #05070c 70%)` }; }
function kindLabel(item: CatalogItem) { return item.kind === 'series' ? 'SERIES' : item.kind === 'episode' ? `EPISODE · S${item.season ?? '?'} E${item.episode ?? '?'}` : 'FILM'; }
