import { useEffect, useId, type CSSProperties, type ReactNode } from 'react';
import { useHoldToConfirm } from '../../vendor/interior/hold-to-confirm';

type HoldButtonProps = {
  children: ReactNode;
  onConfirm: () => void;
  disabled?: boolean;
  className?: string;
  duration?: number;
  confirmedLabel?: string;
};

/** Press-and-hold confirmation for destructive actions. No confirm() dialogs, no accidental taps. */
export function HoldButton({ children, onConfirm, disabled = false, className = '', duration = 1200, confirmedLabel = 'Done' }: HoldButtonProps) {
  const { bind, phase, progress, reset } = useHoldToConfirm({ onConfirm, duration, disabled });
  const hintId = useId();
  useEffect(() => {
    if (phase !== 'committed') return;
    const timer = setTimeout(reset, 1600);
    return () => clearTimeout(timer);
  }, [phase, reset]);
  return <span className="hold-button-wrap">
    <button type="button" {...bind} disabled={disabled} className={`hold-button ${className}`} data-phase={phase} aria-describedby={hintId} style={{ '--hold': progress } as CSSProperties}>
      <span className="hold-button-fill" aria-hidden="true" />
      <span className="hold-button-label">{phase === 'committed' ? confirmedLabel : children}</span>
    </button>
    <span id={hintId} className="sr-only">Press and hold to confirm.</span>
  </span>;
}
