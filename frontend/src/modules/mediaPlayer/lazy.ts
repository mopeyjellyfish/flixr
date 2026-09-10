export function retryableLazy<T>(load: () => Promise<T>): () => Promise<T> {
  let pending: Promise<T> | undefined;
  return () => {
    if (!pending) {
      const candidate = load();
      pending = candidate;
      void candidate.catch(() => { if (pending === candidate) pending = undefined; });
    }
    return pending;
  };
}
