import { useState } from 'react';
import type { ButtonHTMLAttributes } from 'react';
import { motion, useReducedMotion } from 'motion/react';
import { useBlurUpImage } from '../../vendor/interior/blur-up-image';
import { Wordmark } from '../productChrome/Wordmark';

export function Splash({ label = 'Opening your local cinema' }: { label?: string }) {
  return <main className="splash" role="status" aria-label={label}><Wordmark /><span className="splash-line" aria-hidden="true" /><p>{label}</p></main>;
}

export function BusyButton({ busy, children, className = '', disabled, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { busy: boolean }) {
  return <button {...props} className={`busy-button ${className}`} disabled={disabled || busy} aria-busy={busy}><span className={busy ? 'button-spinner' : 'button-spinner is-idle'} aria-hidden="true" />{children}</button>;
}

export function Artwork({ src, className = '', priority = false, width }: { src: string; className?: string; priority?: boolean; width?: number }) {
  const reduced = useReducedMotion();
  const pixels = width && Math.ceil(width * Math.min(window.devicePixelRatio || 1, 2));
  const derivative = pixels ? ([160, 240, 320, 480, 640, 960, 1280, 1600].find((size) => pixels <= size) ?? 1600) : undefined;
  const responsiveSrc = derivative && src.startsWith('/api/v1/catalog/artwork/') ? `${src}?w=${derivative}` : src;
  const { ref, status, instant } = useBlurUpImage({ src: responsiveSrc });
  return <span className={`artwork ${className}`} data-state={status} aria-hidden="true"><motion.img ref={ref} src={responsiveSrc} alt="" draggable={false} decoding="async" loading={priority ? 'eager' : 'lazy'} fetchPriority={priority ? 'high' : 'auto'} initial={false} animate={{ opacity: status === 'ready' ? 1 : 0 }} transition={{ duration: reduced === true || instant ? 0 : 0.35 }} />{status === 'error' && <span className="artwork-unavailable">Artwork unavailable</span>}</span>;
}

export function CatalogSkeleton() {
  return <div className="catalog-skeleton" role="status" aria-label="Loading catalog"><span className="skeleton-title" /><div>{Array.from({ length: 6 }, (_, i) => <span key={i} />)}</div><span className="sr-only">Loading catalog…</span></div>;
}

export function profileAvatar(name: string) {
  const seed = Array.from(name).reduce((hash, letter) => (hash * 33 + letter.charCodeAt(0)) >>> 0, 5381);
  const palette = [['#5084b8', '#233f67'], ['#bd806b', '#664334'], ['#74aa93', '#304c4c'], ['#9882be', '#48385c'], ['#b79a52', '#66532d']][seed % 5];
  const tilt = (seed % 9) - 4;
  const eyes = seed % 3 === 0 ? '<path d="M29 42q6-7 12 0m20 0q6-7 12 0" stroke="#fff" stroke-width="5" stroke-linecap="round" fill="none"/>' : '<circle cx="35" cy="42" r="5" fill="#fff"/><circle cx="66" cy="42" r="5" fill="#fff"/>';
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="${palette[0]}"/><stop offset="1" stop-color="${palette[1]}"/></linearGradient></defs><rect width="100" height="100" rx="9" fill="url(#g)"/><circle cx="${seed % 70}" cy="0" r="80" fill="#ffffff08"/><g transform="rotate(${tilt} 50 50)">${eyes}<path d="M28 62Q50 ${seed % 2 ? 83 : 76} 73 59" stroke="#fff" stroke-width="5" stroke-linecap="round" fill="none"/></g></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

export function Avatar({ name, src, large = false }: { name: string; src?: string; large?: boolean }) {
  const [failed, setFailed] = useState<string>();
  return <span className={`avatar ${large ? 'avatar-large' : ''}`} aria-hidden="true"><img src={src && failed !== src ? src : profileAvatar(name)} onError={() => setFailed(src)} alt="" decoding="async" /></span>;
}
