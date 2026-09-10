import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { MediaVersions } from './MediaVersions';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const candidate = (id: string, title: string, height: number) => ({ id, title, kind: 'film' as const, edition_id: id, width: height * 16 / 9, height, video_codec: 'h264', container: 'mp4' });

it('groups encoding copies only after explaining the history behavior', async () => {
  const confirm = vi.fn().mockResolvedValue(true);
  let reads = 0;
  const grouped = { id: 'film-4k', logical_title_id: 'film-4k', title: 'Arrival', kind: 'film', edition_id: 'film-4k', members: [
    { id: 'film-4k', label: '4K · H.264', edition_id: 'film-4k', selected: false, available: true },
    { id: 'film-1080', label: '1080p · H.264', edition_id: 'film-4k', selected: false, available: true },
  ] };
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
    if (!init?.method) {
      reads += 1;
      return new Response(JSON.stringify(reads === 1 ? { groups: [], candidates: [candidate('film-4k', 'Arrival', 2160), candidate('film-1080', 'Arrival 1080p', 1080)], total: 0 } : { groups: [grouped], candidates: [], total: 1 }));
    }
    return new Response(JSON.stringify(grouped));
  });
  render(<MediaVersions confirm={confirm} onOwnerRequired={() => undefined} />);

  fireEvent.change(await screen.findByLabelText('Title to keep'), { target: { value: 'film-4k' } });
  fireEvent.click(screen.getByRole('checkbox', { name: /arrival 1080p/i }));
  fireEvent.click(screen.getByRole('button', { name: 'Group selected encodings' }));

  await waitFor(() => expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringMatching(/canonical title.*history stays active.*member history returns unchanged/i) })));
  expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/media-version-groups', expect.objectContaining({ method: 'POST', body: JSON.stringify({ kind: 'film', canonical_id: 'film-4k', member_ids: ['film-1080'] }) }));
  expect(await screen.findByText('2 encodings')).toBeVisible();
  expect(fetcher.mock.calls.filter(([, init]) => !init?.method)).toHaveLength(2);
});

it('ungroups a source without describing its dormant history as merged', async () => {
  const confirm = vi.fn().mockResolvedValue(true);
  const group = { id: 'film-4k', logical_title_id: 'film-4k', title: 'Arrival', kind: 'film', edition_id: 'film-4k', edition_label: 'Theatrical cut', members: [
    { id: 'film-4k', label: '4K · HEVC', edition_id: 'film-4k', selected: false, available: true },
    { id: 'film-1080', label: '1080p · H.264', edition_id: 'film-4k', selected: false, available: true },
  ] };
  let reads = 0;
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    if (init?.method === 'DELETE') return new Response(JSON.stringify({ group: { ...group, members: [group.members[0]] }, ungrouped_id: 'film-1080' }));
    reads += 1;
    return new Response(JSON.stringify({ groups: reads === 1 ? [group] : [{ ...group, members: [group.members[0]] }], candidates: reads === 1 ? [] : [candidate('film-1080', 'Arrival', 1080)], total: 1 }));
  });
  render(<MediaVersions confirm={confirm} onOwnerRequired={() => undefined} />);

  fireEvent.click(await screen.findByRole('button', { name: 'Ungroup 1080p · H.264' }));

  await waitFor(() => expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringMatching(/returns unchanged/i) })));
  expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/media-version-groups/film/film-4k/members/film-1080', expect.objectContaining({ method: 'DELETE' }));
  await waitFor(() => expect(fetcher.mock.calls.filter(([, init]) => !init?.method)).toHaveLength(2));
});

it('shows one selected title and searches or loads additional server pages', async () => {
  const groups = ['Arrival', 'Blade Runner'].map((title, index) => ({ id: `film-${index}`, logical_title_id: `film-${index}`, title, kind: 'film', edition_id: `film-${index}`, members: [{ id: `film-${index}`, label: '1080p · H.264', edition_id: `film-${index}`, selected: false, available: true }] }));
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input);
    if (path.includes('q=Blade')) return new Response(JSON.stringify({ groups: [groups[1]], candidates: [], total: 1 }));
    if (path.includes('offset=50')) return new Response(JSON.stringify({ groups: [groups[1]], candidates: [], total: 2 }));
    return new Response(JSON.stringify({ groups: [groups[0]], candidates: [], total: 2, next_offset: 50 }));
  });
  render(<MediaVersions confirm={vi.fn().mockResolvedValue(true)} onOwnerRequired={() => undefined} />);

  expect(await screen.findByRole('option', { name: /Arrival/ })).toBeVisible();
  expect(screen.queryByText('Blade Runner')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Load more titles' }));
  expect(await screen.findByRole('option', { name: /Blade Runner/ })).toBeVisible();

  fireEvent.change(screen.getByRole('searchbox', { name: 'Find a title' }), { target: { value: 'Blade' } });
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/media-version-groups?q=Blade&offset=0&limit=50', expect.anything()));
  expect(screen.getAllByRole('article')).toHaveLength(1);
});

it('returns an expired owner mutation to the owner sign-in flow', async () => {
  const onOwnerRequired = vi.fn();
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
    if (init?.method === 'POST') return new Response(JSON.stringify({ error: { code: 'owner_required' } }), { status: 401 });
    return new Response(JSON.stringify({ groups: [], candidates: [candidate('film-4k', 'Arrival', 2160), candidate('film-1080', 'Arrival', 1080)], total: 0 }));
  });
  render(<MediaVersions confirm={vi.fn().mockResolvedValue(true)} onOwnerRequired={onOwnerRequired} />);

  fireEvent.change(await screen.findByLabelText('Title to keep'), { target: { value: 'film-4k' } });
  fireEvent.click(screen.getByRole('checkbox', { name: /Arrival — 1080p/i }));
  fireEvent.click(screen.getByRole('button', { name: 'Group selected encodings' }));

  await waitFor(() => expect(onOwnerRequired).toHaveBeenCalledOnce());
});

it('reloads through the selected page after editing a page-two title', async () => {
  const first = { id: 'film-1', logical_title_id: 'film-1', title: 'Arrival', kind: 'film', edition_id: 'film-1', members: [{ id: 'film-1', label: '1080p', edition_id: 'film-1', selected: false, available: true }] };
  const second = { id: 'film-2', logical_title_id: 'film-2', title: 'Blade Runner', kind: 'film', edition_id: 'film-2', edition_label: 'Theatrical cut', members: [{ id: 'film-2', label: '4K', edition_id: 'film-2', selected: false, available: true }] };
  let patched = false;
  const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const path = String(input);
    if (init?.method === 'PATCH') { patched = true; return new Response(JSON.stringify({ ...second, edition_label: 'Final cut' })); }
    if (path.includes('offset=50')) return new Response(JSON.stringify({ groups: [{ ...second, edition_label: patched ? 'Final cut' : 'Theatrical cut' }], candidates: [], total: 2 }));
    return new Response(JSON.stringify({ groups: [first], candidates: [], total: 2, next_offset: 50 }));
  });
  render(<MediaVersions confirm={vi.fn().mockResolvedValue(true)} onOwnerRequired={() => undefined} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Load more titles' }));
  await screen.findByRole('option', { name: /Blade Runner/ });
  fireEvent.change(screen.getByLabelText('Title to manage'), { target: { value: 'film-2' } });
  const edition = screen.getByLabelText('Edition label');
  fireEvent.change(edition, { target: { value: 'Final cut' } });
  fireEvent.blur(edition);

  await waitFor(() => expect(screen.getByLabelText('Title to manage')).toHaveValue('film-2'));
  expect(await screen.findByDisplayValue('Final cut')).toBeVisible();
  expect(fetcher.mock.calls.filter(([input]) => String(input).includes('offset=50'))).toHaveLength(2);
});

it('keeps a refresh failure visible instead of replacing it with success', async () => {
  const group = { id: 'film-1', logical_title_id: 'film-1', title: 'Arrival', kind: 'film', edition_id: 'film-1', members: [{ id: 'film-1', label: '1080p', edition_id: 'film-1', selected: false, available: true }] };
  let patched = false;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
    if (init?.method === 'PATCH') { patched = true; return new Response(JSON.stringify({ ...group, edition_label: 'Final cut' })); }
    if (patched) return new Response(JSON.stringify({ error: { code: 'request_failed' } }), { status: 500 });
    return new Response(JSON.stringify({ groups: [group], candidates: [], total: 1 }));
  });
  render(<MediaVersions confirm={vi.fn().mockResolvedValue(true)} onOwnerRequired={() => undefined} />);
  const edition = await screen.findByLabelText('Edition label');
  fireEvent.change(edition, { target: { value: 'Final cut' } });
  fireEvent.blur(edition);

  expect(await screen.findByRole('alert')).toHaveTextContent(/change was saved.*could not be refreshed/i);
  expect(screen.queryByText('Edition label saved.')).not.toBeInTheDocument();
});
