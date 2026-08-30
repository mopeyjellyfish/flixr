import { useLayoutEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { CSSProperties, KeyboardEventHandler, RefObject } from 'react';
import type { CatalogItem, ViewerItem } from '../../core/api';

type Open = (item: ViewerItem, opener: HTMLButtonElement) => void;
type Layout = 'rail' | 'grid';
const GAP = 16;

export function MediaCollection({ layout, label, items, onOpen }: { layout: Layout; label: string; items: ViewerItem[]; onOpen: Open }) {
  return layout === 'grid' ? <Grid label={label} items={items} onOpen={onOpen} /> : <Rail label={label} items={items} onOpen={onOpen} />;
}

export function focusMediaItem(id: string) {
  const target = Array.from(document.querySelectorAll<HTMLButtonElement>('[data-catalog-id]')).find((button) => button.dataset.catalogId === id);
  target?.focus();
  return Boolean(target);
}

function useCollectionWidth(ref: RefObject<HTMLElement | null>, fallback: number) {
  const [width, setWidth] = useState(fallback);
  useLayoutEffect(() => {
    const node = ref.current;
    if (!node) return;
    const measure = () => { const styles = getComputedStyle(node); const padding = Number.parseFloat(styles.paddingLeft || '0') + Number.parseFloat(styles.paddingRight || '0'); setWidth(Math.max(0, node.clientWidth - padding) || fallback); };
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [fallback, ref]);
  return width;
}

function cardWidth(containerWidth: number) { return Math.max(145, Math.min(220, containerWidth * 0.16)); }

function Grid({ label, items, onOpen }: { label: string; items: ViewerItem[]; onOpen: Open }) {
  const parentRef = useRef<HTMLElement>(null);
  const width = useCollectionWidth(parentRef, 1000);
  const minimum = width < 700 ? 145 : 180;
  const columns = Math.max(2, Math.floor((width + GAP) / (minimum + GAP)));
  const itemWidth = (width - GAP * (columns - 1)) / columns;
  const rowHeight = itemWidth * 1.5 + GAP;
  const rows = Math.ceil(items.length / columns);
  const virtualizer = useVirtualizer({ count: rows, getScrollElement: () => parentRef.current, estimateSize: () => rowHeight, overscan: 1, initialRect: { width, height: 900 } });
  const visible = virtualizer.getVirtualItems();
  const rowItems = visible.length ? visible : Array.from({ length: Math.min(rows, 4) }, (_, index) => ({ index, start: index * rowHeight }));
  return <section className="media-grid" aria-label={label} ref={parentRef} data-collection data-layout="grid" data-columns={columns}><div className="media-grid-inner" style={{ height: virtualizer.getTotalSize() }}>{rowItems.flatMap((row) => items.slice(row.index * columns, row.index * columns + columns).map((item, column) => <PosterCard key={item.id} item={item} onOpen={onOpen} onKeyDown={moveFocus} style={{ top: row.start, left: column * (itemWidth + GAP), width: itemWidth }} />))}</div></section>;
}

function Rail({ label, items, onOpen }: { label: string; items: ViewerItem[]; onOpen: Open }) {
  const parentRef = useRef<HTMLDivElement>(null);
  const width = useCollectionWidth(parentRef, 1000);
  const itemWidth = cardWidth(width);
  const stride = itemWidth + GAP;
  const virtualizer = useVirtualizer({ horizontal: true, count: items.length, getScrollElement: () => parentRef.current, estimateSize: () => stride, overscan: 3, initialRect: { width, height: itemWidth * 1.5 + 32 } });
  const visible = virtualizer.getVirtualItems();
  const cards = visible.length ? visible.map((virtual) => ({ index: virtual.index, start: virtual.start })) : items.slice(0, Math.ceil(width / stride) + 3).map((_, index) => ({ index, start: index * stride }));
  return <section aria-label={label}><div className="section-heading"><h2>{label}</h2><span>{items.length} title{items.length === 1 ? '' : 's'}</span></div>{items.length ? <div className="rail" ref={parentRef} data-collection data-layout="rail" data-columns="1" style={{ height: itemWidth * 1.5 + 32 }}><div className="rail-inner" style={{ width: virtualizer.getTotalSize() }}>{cards.map((virtual) => <PosterCard key={items[virtual.index].id} item={items[virtual.index]} style={{ transform: `translateX(${virtual.start}px)`, width: itemWidth }} onKeyDown={moveFocus} onOpen={onOpen} />)}</div></div> : <p className="empty-row">No titles yet.</p>}</section>;
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

function PosterCard({ item, onOpen, style, onKeyDown }: { item: ViewerItem; onOpen: Open; style?: CSSProperties; onKeyDown?: KeyboardEventHandler<HTMLButtonElement> }) {
  return <button data-testid={`card-${item.id}`} data-catalog-id={item.id} data-card className="card" style={style} onKeyDown={onKeyDown} onClick={(event) => onOpen(item, event.currentTarget)}>{item.poster ? <img src={item.poster} alt="" loading="lazy" /> : <span className="card-artwork" style={tonalStyle(item.title)} aria-hidden="true" />}<span className="card-scrim" aria-hidden="true" /><span>{kindLabel(item)}</span><strong>{item.title}</strong>{item.year && <small>{item.year}</small>}{item.demo && <small>Demo title · no media file</small>}</button>;
}

function tonalStyle(title?: string) { if (!title) return undefined; let hash = 0; for (const character of title) hash = (hash * 31 + character.charCodeAt(0)) >>> 0; return { backgroundImage: `radial-gradient(circle at ${25 + hash % 60}% ${20 + (hash >>> 8) % 50}%, hsl(${215 + hash % 35} 88% 62% / .52), transparent 42%), linear-gradient(135deg, #101623, #05070c 70%)` }; }
function kindLabel(item: CatalogItem) { return item.kind === 'series' ? 'SERIES' : item.kind === 'episode' ? `EPISODE · S${item.season ?? '?'} E${item.episode ?? '?'}` : 'FILM'; }
