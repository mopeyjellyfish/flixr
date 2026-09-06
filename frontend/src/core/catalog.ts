export type BrowseState = 'loading' | 'empty' | 'ready' | 'failure';
export function browseState(itemCount: number, failed = false): BrowseState {
  if (failed) return 'failure';
  if (itemCount === 0) return 'empty';
  return 'ready';
}
