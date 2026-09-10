import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { MediaVersions } from './MediaVersions';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const candidate = (id: string, title: string, height: number) => ({ id, title, kind: 'film' as const, edition_id: id, width: height * 16 / 9, height, video_codec: 'h264', container: 'mp4' });

it('groups encoding copies only after explaining the history behavior', async () => {
  const confirm = vi.fn().mockResolvedValue(true);
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
    if (!init?.method) return new Response(JSON.stringify({ groups: [], candidates: [candidate('film-4k', 'Arrival', 2160), candidate('film-1080', 'Arrival 1080p', 1080)] }));
    return new Response(JSON.stringify({ id: 'film-4k', logical_title_id: 'film-4k', title: 'Arrival', kind: 'film', edition_id: 'film-4k', members: [
      { id: 'film-4k', label: '4K · H.264', edition_id: 'film-4k', selected: false, available: true },
      { id: 'film-1080', label: '1080p · H.264', edition_id: 'film-4k', selected: false, available: true },
    ] }));
  });
  render(<MediaVersions confirm={confirm} />);

  fireEvent.change(await screen.findByLabelText('Title to keep'), { target: { value: 'film-4k' } });
  fireEvent.click(screen.getByRole('checkbox', { name: /arrival 1080p/i }));
  fireEvent.click(screen.getByRole('button', { name: 'Group selected encodings' }));

  await waitFor(() => expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringMatching(/canonical title.*history stays active.*member history returns unchanged/i) })));
  expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/media-version-groups', expect.objectContaining({ method: 'POST', body: JSON.stringify({ kind: 'film', canonical_id: 'film-4k', member_ids: ['film-1080'] }) }));
  expect(await screen.findByText('2 encodings')).toBeVisible();
});

it('ungroups a source without describing its dormant history as merged', async () => {
  const confirm = vi.fn().mockResolvedValue(true);
  const group = { id: 'film-4k', logical_title_id: 'film-4k', title: 'Arrival', kind: 'film', edition_id: 'film-4k', edition_label: 'Theatrical cut', members: [
    { id: 'film-4k', label: '4K · HEVC', edition_id: 'film-4k', selected: false, available: true },
    { id: 'film-1080', label: '1080p · H.264', edition_id: 'film-4k', selected: false, available: true },
  ] };
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    if (init?.method === 'DELETE') return new Response(JSON.stringify({ group: { ...group, members: [group.members[0]] }, ungrouped_id: 'film-1080' }));
    return new Response(JSON.stringify({ groups: [group], candidates: [] }));
  });
  render(<MediaVersions confirm={confirm} />);

  fireEvent.click(await screen.findByRole('button', { name: 'Ungroup 1080p · H.264' }));

  await waitFor(() => expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringMatching(/returns unchanged/i) })));
  expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/media-version-groups/film/film-4k/members/film-1080', expect.objectContaining({ method: 'DELETE' }));
});
