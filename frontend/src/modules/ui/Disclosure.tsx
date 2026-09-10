import type { ReactNode, SyntheticEvent } from 'react';

type DisclosureProps = {
  summary: ReactNode;
  /** Secondary line shown next to the summary while collapsed and expanded. */
  detail?: ReactNode;
  children: ReactNode;
  open?: boolean;
  onToggle?: (open: boolean) => void;
  className?: string;
  id?: string;
};

/** Native details/summary styled as a card row: keyboard, screen reader and no-JS friendly progressive reveal. */
export function Disclosure({ summary, detail, children, open, onToggle, className = '', id }: DisclosureProps) {
  const toggled = (event: SyntheticEvent<HTMLDetailsElement>) => onToggle?.(event.currentTarget.open);
  return <details id={id} className={`disclosure ${className}`} open={open} onToggle={onToggle ? toggled : undefined}>
    <summary className="disclosure-summary"><span className="disclosure-chevron" aria-hidden="true" /><span className="disclosure-title">{summary}</span>{detail && <span className="disclosure-detail">{detail}</span>}</summary>
    <div className="disclosure-body">{children}</div>
  </details>;
}
