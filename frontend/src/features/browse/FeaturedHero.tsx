import { useEffect, useRef, useState } from 'react';
import { AnimatePresence, motion, useReducedMotion } from 'motion/react';
import type { ViewerItem } from '../../core/api';
import { Artwork } from '../../modules/ui/Feedback';
import { featuredItems, summaryFor } from './featured';

type Props = { items: ViewerItem[]; suspended?: boolean; onOpen: (item: ViewerItem, opener: HTMLButtonElement) => void; onPlay: (id: string) => void };
export function FeaturedHero({ items, suspended = false, onOpen, onPlay }: Props) {
  const slides = featuredItems(items);
  const [index, setIndex] = useState(0);
  const [paused, setPaused] = useState(false);
  const [engaged, setEngaged] = useState(false);
  const [visible, setVisible] = useState(true);
  const [foreground, setForeground] = useState(!document.hidden);
  const root = useRef<HTMLElement>(null);
  const reduced = useReducedMotion();
  const current = slides[index % Math.max(1, slides.length)];
  const next = slides[(index + 1) % Math.max(1, slides.length)];
  const nextImage = next?.backdrop || next?.poster;
  const rotating = !paused && !engaged && !suspended && visible && foreground && !reduced && slides.length > 1;
  useEffect(() => {
    const change = () => setForeground(!document.hidden);
    document.addEventListener('visibilitychange', change);
    const observer = typeof IntersectionObserver === 'undefined' ? undefined : new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting), { threshold: 0.15 });
    if (root.current) observer?.observe(root.current);
    return () => { document.removeEventListener('visibilitychange', change); observer?.disconnect(); };
  }, []);
  useEffect(() => {
    if (!rotating) return;
    const timer = window.setTimeout(() => setIndex((value) => (value + 1) % slides.length), 9000);
    return () => window.clearTimeout(timer);
  }, [index, rotating, slides.length]);
  useEffect(() => {
    if (!nextImage || !rotating) return;
    // Only warm the next locally cached image, not the entire library.
    const timer = window.setTimeout(() => { const image = new Image(); image.src = nextImage; }, 1200);
    return () => window.clearTimeout(timer);
  }, [nextImage, rotating]);
  const move = (delta: number) => { setPaused(true); setIndex((value) => (value + delta + slides.length) % slides.length); };
  return <section ref={root} className="hero featured-hero" aria-roledescription="carousel" aria-label="Featured from your library" onMouseEnter={() => setEngaged(true)} onMouseLeave={() => { if (!root.current?.contains(document.activeElement)) setEngaged(false); }} onFocusCapture={() => setEngaged(true)} onBlurCapture={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setEngaged(false); }}>
    <AnimatePresence initial={false}>{current && <motion.div key={current.id} className="featured-art" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: reduced ? 0 : 0.65 }}>{(current.backdrop || current.poster) && <Artwork className={`hero-art ${!current.backdrop ? 'poster-hero' : ''}`} src={current.backdrop || current.poster!} priority />}</motion.div>}</AnimatePresence>
    <div className="featured-copy" aria-live={rotating ? 'off' : 'polite'}><p className="eyebrow">YOUR LIBRARY. A LITTLE DISCOVERY.</p>{current ? <><h1>{current.title}</h1><p className="hero-meta">{[current.kind === 'series' ? 'Series' : 'Movie', current.year, ...(current.genres ?? []).slice(0, 3)].filter(Boolean).join(' · ')}</p><p className="hero-synopsis">{summaryFor(current) || 'Make tonight a movie night. Explore this title in your library.'}</p><div className="hero-actions">{current.playable === true && current.kind === 'film' && <button className="primary" aria-label={`Play ${current.title}`} onClick={() => onPlay(current.id)}>▶ Play {current.title}</button>}<button data-catalog-id={current.id} aria-label={`View details for ${current.title}`} onClick={(event) => onOpen(current, event.currentTarget)}>ⓘ More info</button></div></> : <h1>Your local cinema.</h1>}</div>
    {slides.length > 1 && <div className="featured-controls"><span className="featured-count">{String(index % slides.length + 1).padStart(2, '0')} <span>/ {String(slides.length).padStart(2, '0')}</span></span><button aria-label="Previous featured title" onClick={() => move(-1)}>‹</button><button aria-label="Next featured title" onClick={() => move(1)}>›</button>{!reduced && <button aria-label={paused ? 'Resume featured rotation' : 'Pause featured rotation'} aria-pressed={paused} onClick={() => setPaused(!paused)}>{paused ? '▶' : 'Ⅱ'}</button>}<span className="featured-timer" aria-hidden="true"><span key={`${index}-${rotating}`} style={{ animationPlayState: rotating ? 'running' : 'paused' }} /></span></div>}
  </section>;
}
