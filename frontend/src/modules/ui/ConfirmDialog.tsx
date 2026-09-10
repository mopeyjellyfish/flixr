import { useCallback, useRef, useState, type ReactNode } from 'react';
import { AppModal } from './AppModal';

export type ConfirmOptions = {
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** Destructive confirmations get the danger treatment on the confirm button. */
  danger?: boolean;
};

type PendingConfirm = ConfirmOptions & { resolve: (confirmed: boolean) => void };

/**
 * Promise-based replacement for window.confirm() on Interior's modal hook.
 * Render `dialog` once near the top of the page and await `confirm(...)` wherever a decision is needed.
 */
export function useConfirm() {
  const [pending, setPending] = useState<PendingConfirm>();
  const confirmRef = useRef<HTMLButtonElement>(null);
  const confirm = useCallback((options: ConfirmOptions) => new Promise<boolean>((resolve) => {
    setPending((current) => { current?.resolve(false); return { ...options, resolve }; });
  }), []);
  const settle = (confirmed: boolean) => { pending?.resolve(confirmed); setPending(undefined); };
  const dialog = pending ? <AppModal open onClose={() => settle(false)} title={pending.title} className="confirm-dialog" initialFocusRef={confirmRef}
    footer={<div className="actions"><button type="button" className="quiet-button" onClick={() => settle(false)}>{pending.cancelLabel ?? 'Cancel'}</button><button ref={confirmRef} type="button" className={pending.danger ? 'danger-button' : 'primary'} onClick={() => settle(true)}>{pending.confirmLabel ?? 'Confirm'}</button></div>}>
    {typeof pending.message === 'string' ? <p className="confirm-message">{pending.message}</p> : pending.message}
  </AppModal> : null;
  return { confirm, dialog };
}
