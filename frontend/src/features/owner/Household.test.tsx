import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { HouseholdPanel } from './Household';

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

const policy = { library_ids: [], rating_region: '', max_rating: '', unrated_policy: 'allow', allow_tags: [], deny_tags: [], version: 1 };
const libraries = [{ id: 'films', name: 'Films', kind: 'film' as const, locations: [] }, { id: 'kids', name: 'Kids', kind: 'film' as const, locations: [] }];

/** Runs a press-and-hold confirmation to completion under fake timers. */
function hold(button: HTMLElement) {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'requestAnimationFrame', 'cancelAnimationFrame', 'performance'] });
  fireEvent.keyDown(button, { key: 'Enter' });
  act(() => { vi.advanceTimersByTime(1600); });
  vi.useRealTimers();
}

describe('household panel', () => {
  it('creates a profile with a PIN from the dialog', async () => {
    const created: unknown[] = [];
    let profiles: unknown[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/api/v1/profiles' && init?.method === 'POST') { created.push(JSON.parse(String(init.body))); profiles = [{ id: 'kid', name: 'Kid', protected: true }]; return new Response(JSON.stringify(profiles[0]), { status: 201 }); }
      if (path === '/api/v1/profiles') return new Response(JSON.stringify({ profiles }));
      return new Response('{}');
    });
    const onNotice = vi.fn();
    render(<HouseholdPanel libraries={libraries} onNotice={onNotice} />);
    expect(await screen.findByText(/no profiles yet/i)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /add profile/i }));
    const dialog = screen.getByRole('dialog', { name: /new profile/i });
    fireEvent.change(within(dialog).getByLabelText(/^name$/i), { target: { value: 'Kid' } });
    fireEvent.change(within(dialog).getByLabelText(/pin digit 1 of 4/i), { target: { value: '2468' } });
    fireEvent.click(within(dialog).getByRole('button', { name: /create profile/i }));
    await waitFor(() => expect(created).toEqual([{ name: 'Kid', pin: '2468' }]));
    expect(onNotice).toHaveBeenCalledWith('Kid added to the household.');
    expect(await screen.findByRole('button', { name: /edit kid/i })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('rejects an incomplete PIN before sending anything', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({ profiles: [] })));
    render(<HouseholdPanel libraries={[]} onNotice={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /add profile/i }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/^name$/i), { target: { value: 'Kid' } });
    fireEvent.change(within(dialog).getByLabelText(/pin digit 1 of 4/i), { target: { value: '24' } });
    fireEvent.click(within(dialog).getByRole('button', { name: /create profile/i }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/all 4 digits/i);
    expect(fetcher).not.toHaveBeenCalledWith('/api/v1/profiles', expect.objectContaining({ method: 'POST' }));
  });

  it('edits a profile content policy with understandable rating and tag rules', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/api/v1/profiles') return new Response(JSON.stringify({ profiles: [{ id: 'child', name: 'Child', protected: false }] }));
      if (path.endsWith('/access-policy') && init?.method === 'PUT') return new Response(String(init.body));
      if (path.endsWith('/access-policy')) return new Response(JSON.stringify(policy));
      return new Response('{}');
    });
    const onNotice = vi.fn();
    render(<HouseholdPanel libraries={libraries} onNotice={onNotice} />);
    fireEvent.click(await screen.findByRole('button', { name: /edit child/i }));
    const access = await screen.findByRole('group', { name: /content access for child/i });
    fireEvent.click(within(access).getByLabelText(/only selected libraries/i));
    fireEvent.click(within(access).getByLabelText(/^kids$/i));
    fireEvent.change(within(access).getByLabelText(/rating region/i), { target: { value: 'GB' } });
    fireEvent.change(within(access).getByLabelText(/maximum rating/i), { target: { value: '12' } });
    fireEvent.change(within(access).getByLabelText(/unrated titles/i), { target: { value: 'deny' } });
    fireEvent.change(within(access).getByLabelText(/allowed tags/i), { target: { value: 'family' } });
    fireEvent.change(within(access).getByLabelText(/blocked tags/i), { target: { value: 'scary' } });
    fireEvent.click(within(access).getByRole('button', { name: /save content access/i }));
    await waitFor(() => expect(onNotice).toHaveBeenCalledWith(expect.stringMatching(/content access updated/i)));
    expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/profiles/child/access-policy', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ library_ids: ['kids'], rating_region: 'GB', max_rating: '12', unrated_policy: 'deny', allow_tags: ['family'], deny_tags: ['scary'] }) }));
  });

  it('removes a PIN and deletes a profile only after a held confirmation', async () => {
    const calls: string[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/api/v1/profiles/ada' && init?.method === 'PATCH') { calls.push(`PATCH ${init.body}`); return new Response(JSON.stringify({ id: 'ada', name: 'Ada', protected: false })); }
      if (path === '/api/v1/profiles/ada' && init?.method === 'DELETE') { calls.push('DELETE'); return new Response(JSON.stringify({ error: { code: 'profile_failed' } }), { status: 500 }); }
      if (path === '/api/v1/profiles') return new Response(JSON.stringify({ profiles: [{ id: 'ada', name: 'Ada', protected: true }] }));
      if (path.endsWith('/access-policy')) return new Response(JSON.stringify(policy));
      return new Response('{}');
    });
    render(<HouseholdPanel libraries={[]} onNotice={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /edit ada/i }));
    const dialog = await screen.findByRole('dialog', { name: /edit ada/i });
    const remove = within(dialog).getByRole('button', { name: /remove pin/i });
    fireEvent.click(remove);
    expect(calls).toEqual([]);
    hold(remove);
    await waitFor(() => expect(calls).toEqual(['PATCH {"unprotect":true}']));
    hold(within(dialog).getByRole('button', { name: /delete profile/i }));
    await waitFor(() => expect(calls).toContain('DELETE'));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/could not/i);
  });
});
