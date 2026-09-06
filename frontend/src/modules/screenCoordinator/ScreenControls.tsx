import { useEffect, useRef, useSyncExternalStore } from 'react';
import type { ScreenPresence } from '../../core/screens';
import { screenCoordinator } from './runtime';

function useScreens() {
  return useSyncExternalStore(
    (listener) => screenCoordinator.subscribe(listener),
    () => screenCoordinator.current(),
  );
}

export function ScreenReadiness({ catalogID }: { catalogID?: string }) {
  const state = useScreens();
  const chooser = useRef<HTMLDialogElement>(null);
  const opener = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (state.status === 'idle') void screenCoordinator.refresh(); }, [state.status]);
  const screens = state.screens;
  const choose = async (screen: ScreenPresence) => {
    if (!await screenCoordinator.connect(screen)) return;
    if (catalogID && screen.catalog_id !== catalogID) screenCoordinator.command({ version: 1, type: 'play', catalog_id: catalogID, position_ms: 0 });
    chooser.current?.close();
  };
  return <section className="screen-readiness" aria-label="Local screens">
    <p role="status">{state.status === 'connected' ? `Connected to ${state.screen.name}` : state.status === 'receiving' ? `${state.screen.name} is ready` : state.status === 'disconnected' || state.status === 'error' ? state.message : screens.length ? `${screens.length} local screen${screens.length === 1 ? '' : 's'} ready` : 'No other Flixr screens ready'}</p>
    <button ref={opener} type="button" onClick={() => { void screenCoordinator.refresh(); chooser.current?.showModal(); }}>Choose a screen</button>
    <button type="button" onClick={() => void screenCoordinator.receive(window.navigator.platform ? `${window.navigator.platform} screen` : 'Flixr screen')}>Use this device as a screen</button>
    {state.status === 'connected' && <div className="actions"><button type="button" onClick={() => screenCoordinator.command({ version: 1, type: 'pause' })}>Pause remote screen</button><button type="button" onClick={() => screenCoordinator.command({ version: 1, type: 'seek', position_ms: 0 })}>Restart on remote screen</button><button type="button" onClick={() => screenCoordinator.command({ version: 1, type: 'handoff' })}>Stop remote playback</button></div>}
    <dialog ref={chooser} aria-labelledby="screen-chooser-title" onClose={() => opener.current?.focus()}>
      <h2 id="screen-chooser-title">Choose a local screen</h2>
      {screens.length ? screens.map((screen) => <button key={screen.id} onClick={() => void choose(screen)}>{screen.name} · {screen.state}</button>) : <p>No screens are available for this profile.</p>}
      <button autoFocus onClick={() => chooser.current?.close()}>Close screen chooser</button>
    </dialog>
  </section>;
}
