import { useEffect } from 'react';
import { useOtpInput } from '../../vendor/interior/otp-input';

type PinCellsProps = {
  label: string;
  length?: number;
  disabled?: boolean;
  autoFocus?: boolean;
  invalid?: boolean;
  describedBy?: string;
  onChange?: (value: string) => void;
  onComplete?: (value: string) => void;
};

/** Masked digit cells for household PINs. Native password masking keeps contrast checks honest. Remount with a new `key` to clear it. */
export function PinCells({ label, length = 4, disabled = false, autoFocus = false, invalid = false, describedBy, onChange, onComplete }: PinCellsProps) {
  const otp = useOtpInput({ length, mode: 'numeric', disabled, onChange, onComplete });
  const { focusAt } = otp;
  useEffect(() => { if (autoFocus && !disabled) focusAt(0); }, [autoFocus, disabled, focusAt]);
  return <div className={`pin-cells ${invalid ? 'is-invalid' : ''}`} role="group" aria-label={label} aria-describedby={describedBy} aria-invalid={invalid || undefined}>
    {otp.chars.map((char, index) => {
      const { ref, ...cell } = otp.getCellProps(index);
      return <span key={index} className={`pin-cell ${char ? 'is-filled' : ''} ${otp.focusedIndex === index ? 'is-focused' : ''}`}>
        <input ref={ref} {...cell} type="password" aria-label={`${label} digit ${index + 1} of ${length}`} className="pin-cell-input" />
      </span>;
    })}
  </div>;
}
