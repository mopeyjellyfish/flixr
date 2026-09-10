import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ProfileChooser } from './Profiles';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const protectedProfiles = new Response(JSON.stringify({ profiles: [{ id: 'a', name: 'Ari', protected: true }] }));

describe('profile choice', () => {
  it('submits a four digit PIN from the cells and reports rate limiting in the dialog', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(protectedProfiles.clone())
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'invalid_pin' } }), { status: 401 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'pin_rate_limited' } }), { status: 429 }));
    render(<ProfileChooser onSelected={() => undefined} owner={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /ari/i }));
    expect(screen.getByRole('dialog')).toHaveAccessibleName(/enter pin for ari/i);
    fireEvent.change(screen.getByLabelText(/profile pin digit 1 of 4/i), { target: { value: '1111' } });
    expect(await screen.findByRole('alert')).toHaveTextContent(/not valid/i);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/profiles/a/select', expect.objectContaining({ body: JSON.stringify({ pin: '1111' }) }));
    // A failed attempt clears the cells so the next digits start fresh.
    expect(screen.getByLabelText(/profile pin digit 1 of 4/i)).toHaveValue('');
    fireEvent.change(screen.getByLabelText(/profile pin digit 1 of 4/i), { target: { value: '2222' } });
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/too many pin attempts/i));
  });

  it('accepts a longer PIN through the fallback field', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(protectedProfiles.clone())
      .mockResolvedValueOnce(new Response(JSON.stringify({ selected: true })));
    const onSelected = vi.fn();
    render(<ProfileChooser onSelected={onSelected} owner={() => undefined} />);
    fireEvent.click(await screen.findByRole('button', { name: /ari/i }));
    fireEvent.click(screen.getByRole('button', { name: /longer than 4 digits/i }));
    fireEvent.change(screen.getByLabelText(/^profile pin$/i), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: /continue/i }));
    await waitFor(() => expect(onSelected).toHaveBeenCalled());
    expect(fetcher).toHaveBeenCalledWith('/api/v1/profiles/a/select', expect.objectContaining({ body: JSON.stringify({ pin: '123456' }) }));
  });

  it('explains the empty household and offers the owner sign in', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(JSON.stringify({ profiles: [] })));
    const owner = vi.fn();
    render(<ProfileChooser onSelected={() => undefined} owner={owner} />);
    fireEvent.click(await screen.findByRole('button', { name: /sign in as owner/i }));
    expect(owner).toHaveBeenCalled();
  });
});
