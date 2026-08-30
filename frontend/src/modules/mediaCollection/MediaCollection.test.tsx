import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { MediaCollection } from './MediaCollection';

afterEach(cleanup);
const items = Array.from({ length: 10_000 }, (_, index) => ({ id: `${index}`, title: `Title ${index}`, kind: 'film', local_only: true, listed: false, poster: index === 0 ? '/poster.jpg' : undefined }));

it('keeps large grids bounded and exposes native lazy posters', () => {
  render(<MediaCollection layout="grid" label="Titles" items={items} onOpen={() => undefined} />);
  expect(document.querySelectorAll('[data-card]').length).toBeLessThan(30);
  expect(document.querySelector('img')).toHaveAttribute('loading', 'lazy');
});

it('moves focus within its rail without a document-level focus helper', () => {
  render(<MediaCollection layout="rail" label="Titles" items={items.slice(0, 3)} onOpen={() => undefined} />);
  const first = screen.getByTestId('card-0');
  first.focus();
  fireEvent.keyDown(first, { key: 'ArrowRight' });
  expect(screen.getByTestId('card-1')).toHaveFocus();
});

it('moves vertical rail focus between rows and back to the active navigation', () => {
  const nextItems = items.slice(0, 3).map((item) => ({ ...item, id: `next-${item.id}` }));
  render(<main><header><nav><button aria-current="page">Home</button></nav></header><MediaCollection layout="rail" label="First" items={items.slice(0, 3)} onOpen={() => undefined} /><MediaCollection layout="rail" label="Second" items={nextItems} onOpen={() => undefined} /></main>);
  const first = screen.getByTestId('card-0');
  first.focus();
  fireEvent.keyDown(first, { key: 'ArrowDown' });
  expect(screen.getByTestId('card-next-0')).toHaveFocus();
  fireEvent.keyDown(screen.getByTestId('card-next-0'), { key: 'ArrowUp' });
  expect(first).toHaveFocus();
  fireEvent.keyDown(first, { key: 'ArrowUp' });
  expect(screen.getByRole('button', { name: 'Home' })).toHaveFocus();
});

it('uses one measured grid geometry for the card width, gap, and directional focus', () => {
  render(<MediaCollection layout="grid" label="Titles" items={items.slice(0, 12)} onOpen={() => undefined} />);
  const collection = screen.getByRole('region', { name: 'Titles' });
  const columns = Number(collection.getAttribute('data-columns'));
  const first = screen.getByTestId('card-0');
  const second = screen.getByTestId('card-1');
  expect(Number.parseFloat(second.style.left) - Number.parseFloat(first.style.left) - Number.parseFloat(first.style.width)).toBe(16);
  first.focus();
  fireEvent.keyDown(first, { key: 'ArrowDown' });
  expect(screen.getByTestId(`card-${columns}`)).toHaveFocus();
});
