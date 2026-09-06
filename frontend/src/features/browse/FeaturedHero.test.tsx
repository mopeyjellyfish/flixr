import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { FeaturedHero } from './FeaturedHero';
import { featuredItems, summaryFor } from './featured';
import type { ViewerItem } from '../../core/api';

const items: ViewerItem[] = [
  { id: 'film', title: 'A movie', kind: 'film', local_only: true, listed: false },
  { id: 'show', title: 'A series', kind: 'series', local_only: true, listed: false },
];
afterEach(() => { cleanup(); vi.useRealTimers(); });
it('rotates mixed titles, suspends for details, and lets viewers pause manually', () => {
  vi.useFakeTimers();
  const props = { items, onOpen: vi.fn(), onPlay: vi.fn() };
  const { rerender } = render(<FeaturedHero {...props} />);
  expect(screen.getByRole('heading')).toHaveTextContent('A series');
  act(() => { vi.advanceTimersByTime(9000); });
  expect(screen.getByRole('heading')).toHaveTextContent('A movie');
  rerender(<FeaturedHero {...props} suspended />);
  act(() => { vi.advanceTimersByTime(18000); });
  expect(screen.getByRole('heading')).toHaveTextContent('A movie');
  rerender(<FeaturedHero {...props} />);
  fireEvent.click(screen.getByRole('button', { name: 'Next featured title' }));
  act(() => { vi.advanceTimersByTime(18000); });
  expect(screen.getByRole('heading')).toHaveTextContent('A series');
  expect(screen.getByRole('button', { name: 'Resume featured rotation' })).toHaveAttribute('aria-pressed', 'true');
  expect(screen.queryByRole('button', { name: /^Play / })).not.toBeInTheDocument();
});
it('excludes episodes and duplicates, and keeps long provider plots out of the hero', () => {
  expect(featuredItems([...items, items[0], { ...items[0], id: 'episode', kind: 'episode' }]).map(item => item.id)).toEqual(['show', 'film']);
  const synopsis = '<p>' + 'A long story with many details. '.repeat(30) + '</p>';
  const item = { ...items[0], synopsis };
  expect(summaryFor(item).length).toBeLessThanOrEqual(240);
  expect(summaryFor(item)).not.toContain('<');
  expect(item.synopsis).toBe(synopsis);
});
