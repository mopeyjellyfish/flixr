import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { Artwork, Avatar, profileAvatar } from './Feedback';
import { BootSplash } from './BootSplash';

afterEach(() => { cleanup(); vi.useRealTimers(); });
it('keeps the splash until content is ready and cancels its exit on unmount', async () => {
  vi.useFakeTimers();
  const done = vi.fn();
  const view = render(<BootSplash ready={false} onComplete={done} />);
  await act(async () => { await vi.advanceTimersByTimeAsync(4000); });
  expect(done).not.toHaveBeenCalled();
  expect(screen.getByRole('status')).toHaveAccessibleName('Preparing your cinema');
  expect(screen.getByText('Giving your favourites the best seats…')).toBeInTheDocument();
  view.rerender(<BootSplash ready onComplete={done} />);
  await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
  expect(done).toHaveBeenCalledOnce();
  done.mockClear();
  view.rerender(<BootSplash ready={false} onComplete={done} />);
  view.unmount();
  await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
  expect(done).not.toHaveBeenCalled();
});
it('generates stable offline pictures and falls back when a supplied picture fails', () => {
  expect(profileAvatar('Alex')).toBe(profileAvatar('Alex'));
  expect(profileAvatar('Alex')).not.toBe(profileAvatar('Sam'));
  expect(profileAvatar('<script>')).toMatch(/^data:image\/svg\+xml,/);
  const { container } = render(<Avatar name="Alex" src="/missing-avatar.png" />);
  const image = container.querySelector('img')!;
  fireEvent.error(image);
  expect(image).toHaveAttribute('src', profileAvatar('Alex'));
});
it('requests a capped derivative for catalog artwork', () => {
  const { container } = render(<Artwork src="/api/v1/catalog/artwork/film/poster" width={210} />);
  expect(container.querySelector('img')).toHaveAttribute('src', '/api/v1/catalog/artwork/film/poster?w=240');
});
