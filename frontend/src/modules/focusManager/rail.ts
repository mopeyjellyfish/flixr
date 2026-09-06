export function moveRailFocus(event: React.KeyboardEvent<HTMLButtonElement>) {
  const target = event.currentTarget;
  const rail = target.closest<HTMLElement>('[data-rail]');
  if (!rail) return;
  const cards = Array.from(rail.querySelectorAll<HTMLButtonElement>('button[data-card]'));
  const current = cards.indexOf(target);
  const horizontal = event.key === 'ArrowRight' ? current + 1 : event.key === 'ArrowLeft' ? current - 1 : -1;
  if (horizontal >= 0 && horizontal < cards.length) {
    event.preventDefault();
    cards[horizontal].focus();
    return;
  }
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
  const rails = Array.from(document.querySelectorAll<HTMLElement>('[data-rail]'));
  const index = rails.indexOf(rail);
  const sibling = rails[index + (event.key === 'ArrowDown' ? 1 : -1)];
  const destination = sibling?.querySelector<HTMLButtonElement>('button[data-card]')
    ?? (event.key === 'ArrowUp' ? document.querySelector<HTMLButtonElement>('header button, header a') : undefined);
  if (destination) {
    event.preventDefault();
    destination.focus();
  }
}
