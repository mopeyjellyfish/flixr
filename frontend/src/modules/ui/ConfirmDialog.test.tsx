import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useConfirm } from './ConfirmDialog';

afterEach(cleanup);

function Harness({ onResult }: { onResult: (value: boolean) => void }) {
  const { confirm, dialog } = useConfirm();
  return <>
    <button type="button" onClick={() => void confirm({ title: 'Remove folder?', message: 'Titles become unavailable.', confirmLabel: 'Remove', danger: true }).then(onResult)}>Trigger</button>
    {dialog}
  </>;
}

it('resolves true when the owner confirms', async () => {
  const onResult = vi.fn();
  render(<Harness onResult={onResult} />);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Trigger' }));
  const dialog = await screen.findByRole('dialog', { name: 'Remove folder?' });
  expect(dialog).toHaveTextContent('Titles become unavailable.');
  fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('resolves false on cancel and on escape', async () => {
  const onResult = vi.fn();
  render(<Harness onResult={onResult} />);
  fireEvent.click(screen.getByRole('button', { name: 'Trigger' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
  await vi.waitFor(() => expect(onResult).toHaveBeenLastCalledWith(false));
  fireEvent.click(screen.getByRole('button', { name: 'Trigger' }));
  fireEvent.keyDown(await screen.findByRole('dialog'), { key: 'Escape' });
  await vi.waitFor(() => expect(onResult).toHaveBeenCalledTimes(2));
  expect(onResult).toHaveBeenLastCalledWith(false);
});
