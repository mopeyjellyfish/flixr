import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ProfileChooser } from './Profiles';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const protectedProfiles = new Response(JSON.stringify({ profiles: [{ id: 'a', name: 'Ari', protected: true }] }));

describe('profile choice', () => {
  it('focuses the first PIN cell and submits by typing into the active cells', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(protectedProfiles.clone())
      .mockResolvedValueOnce(new Response(JSON.stringify({ selected: true })));
    const onSelected = vi.fn();
    const { rerender } = render(<ProfileChooser onSelected={onSelected} owner={() => undefined} />);
    const profile = await screen.findByRole('button', { name: /ari/i });
    let focusedDuringSelection: Element | null = null;
    document.addEventListener('click', () => { focusedDuringSelection = document.activeElement; }, { once: true });
    fireEvent.click(profile);
    expect(focusedDuringSelection).toBe(screen.getByLabelText(/profile pin digit 1 of 4/i));
    expect(screen.getByLabelText(/profile pin digit 1 of 4/i)).toHaveFocus();
    for (const [index, digit] of [...'1234'].entries()) {
      expect(screen.getByLabelText(`Profile PIN digit ${index + 1} of 4`)).toHaveFocus();
      fireEvent.change(document.activeElement!, { target: { value: digit } });
      rerender(<ProfileChooser onSelected={onSelected} owner={() => undefined} />);
    }
    await waitFor(() => expect(onSelected).toHaveBeenCalledOnce());
  });

  it('keeps deliberate focus changes and focuses a newly opened profile prompt', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(JSON.stringify({ profiles: [
      { id: 'a', name: 'Ari', protected: true }, { id: 'b', name: 'Bea', protected: true },
    ] })));
    const props = { onSelected: () => undefined, owner: () => undefined };
    const { rerender } = render(<ProfileChooser {...props} />);
    fireEvent.click(await screen.findByRole('button', { name: /ari/i }));
    expect(screen.getByLabelText(/profile pin digit 1 of 4/i)).toHaveFocus();
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    cancel.focus();
    rerender(<ProfileChooser {...props} />);
    expect(cancel).toHaveFocus();
    fireEvent.click(cancel);
    fireEvent.click(screen.getByRole('button', { name: /bea/i }));
    expect(screen.getByRole('dialog')).toHaveAccessibleName(/enter pin for bea/i);
    expect(screen.getByLabelText(/profile pin digit 1 of 4/i)).toHaveFocus();
  });

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
