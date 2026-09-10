import type { ReactNode, RefObject } from 'react';
import { createPortal } from 'react-dom';
import { useModal } from '../../vendor/interior/modal';

type AppModalProps = {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
  initialFocusRef?: RefObject<HTMLElement | null>;
  /** Block backdrop and Escape dismissal while a request is in flight. */
  locked?: boolean;
};

/** Cobalt-styled dialog on Interior's focus-trapping modal hook. */
export function AppModal({ open, onClose, title, description, children, footer, className = '', initialFocusRef, locked = false }: AppModalProps) {
  const modal = useModal({ open, onClose, initialFocusRef, closeOnBackdrop: !locked, closeOnEscape: !locked });
  if (!open || !modal.target) return null;
  return createPortal(
    <div className="app-modal-overlay" {...modal.overlayProps}>
      <div className={`app-modal ${className}`} {...modal.panelProps} aria-describedby={description ? modal.descriptionId : undefined}>
        <header className="app-modal-head">
          <div><h2 id={modal.titleId}>{title}</h2>{description && <p id={modal.descriptionId}>{description}</p>}</div>
          <button type="button" className="app-modal-close" onClick={onClose} disabled={locked} aria-label="Close">×</button>
        </header>
        <div className="app-modal-body">{children}</div>
        {footer && <footer className="app-modal-foot">{footer}</footer>}
      </div>
    </div>,
    modal.target,
  );
}
