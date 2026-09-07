import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type ViewingEvent } from '../../core/api';
import { Wordmark } from '../../modules/productChrome/Wordmark';

export function History({ onBrowse, onExit }: { onBrowse: () => void; onExit: () => void }) {
  const [events, setEvents] = useState<ViewingEvent[]>([]);
  const [next, setNext] = useState('');
  const [undo, setUndo] = useState<{ id: string; undo_until: number }>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [changing, setChanging] = useState(false);
  const version = useRef(0);
  const pending = useRef(false);
  const mutation = useRef(false);

  const load = useCallback(async (cursor = '') => {
    if (pending.current || mutation.current) return;
    pending.current = true;
    const request = ++version.current;
    setLoading(true);
    setError('');
    try {
      const page = await api.history(cursor);
      if (request !== version.current) return;
      setEvents(current => cursor ? [...current, ...page.events] : page.events);
      setNext(page.next ?? '');
    } catch {
      if (request === version.current) setError('Flixr could not load history.');
    } finally {
      if (request === version.current) { pending.current = false; setLoading(false); }
    }
  }, []);

  const invalidate = useCallback(() => { version.current++; pending.current = false; }, []);
  useEffect(() => {
    void load();
    return invalidate;
  }, [load, invalidate]);

  useEffect(() => {
    if (!undo) return;
    const timer = window.setTimeout(() => setUndo(undefined), Math.max(0, undo.undo_until - Date.now()));
    return () => window.clearTimeout(timer);
  }, [undo]);

  const changeHistory = async (restore: boolean) => {
    if (mutation.current) return;
    mutation.current = true;
    const request = ++version.current;
    pending.current = false;
    setLoading(false);
    setChanging(true);
    setError('');
    try {
      if (restore) {
        if (!undo) return;
        await api.undoHistoryClear(undo.id);
        if (request !== version.current) return;
        setUndo(undefined);
        const page = await api.history();
        if (request !== version.current) return;
        setEvents(page.events);
        setNext(page.next ?? '');
      } else {
        const action = await api.clearHistory();
        if (request !== version.current) return;
        setUndo(action);
        setEvents([]);
        setNext('');
      }
    } catch (failure) {
      if (request === version.current) {
        const expired = restore && failure instanceof ApiError && failure.status === 404;
        if (expired) setUndo(undefined);
        setError(expired ? 'The clear can no longer be undone.' : restore ? 'Flixr could not restore history. Try again.' : 'Flixr could not clear history.');
      }
    } finally {
      if (request === version.current) { mutation.current = false; setChanging(false); }
    }
  };

  return <main className="viewer">
    <header><Wordmark/><nav aria-label="Main navigation"><button className="nav-tab" onClick={onBrowse}>Browse</button><button className="nav-tab selected" aria-current="page">History</button></nav><button onClick={onExit}>Switch profile</button></header>
    <section className="state">
      <h1>Viewing history</h1><p>History stays here even if a title is temporarily unavailable.</p>
      {undo ? <button onClick={() => void changeHistory(true)} disabled={changing}>Undo clear</button> : <button onClick={() => void changeHistory(false)} disabled={changing || !events.length}>Clear history</button>}
      {error && <p role="alert">{error} <button onClick={() => void load()} disabled={changing || loading}>Reload history</button></p>}
      <ul>{events.map(event => <li key={event.id}><strong>{event.title}</strong> · {event.type === 'completed' ? 'Completed' : 'Imported summary'} · <HistoryDate event={event}/></li>)}</ul>
      {next && <button onClick={() => void load(next)} disabled={loading || changing}>Load more</button>}
    </section>
  </main>;
}

function HistoryDate({ event }: { event: ViewingEvent }) {
  const timestamp = event.provenance === 'local' ? event.recorded_at : event.source_time;
  if (timestamp === null) return <span>Date unknown</span>;
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return <span>Date unavailable</span>;
  return <time dateTime={date.toISOString()}>{date.toLocaleDateString()}</time>;
}
