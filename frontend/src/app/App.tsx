import { lazy, Suspense, useCallback, useEffect, useState } from 'react';
import { api } from '../api/client';
import type { SetupStatus } from '../core/api';
import { Setup, OwnerLogin } from '../features/setup/Setup';
import { ProfileChooser } from '../features/profiles/Profiles';
import { screenCoordinator } from '../modules/screenCoordinator/runtime';

const Owner = lazy(async () => ({ default: (await import('../features/owner/Owner')).Owner }));
const Browse = lazy(async () => ({ default: (await import('../features/browse/Browse')).Browse }));
const Player = lazy(async () => ({ default: (await import('../modules/mediaPlayer/Player')).Player }));

type Route = 'loading' | 'setup' | 'login' | 'profiles' | 'owner' | 'browse' | 'player' | 'failure';
function routeFor(path = window.location.pathname): Route {
  const pathname = path.split(/[?#]/, 1)[0];
  if (pathname === '/setup') return 'setup';
  if (pathname === '/login') return 'login';
  if (pathname === '/profiles') return 'profiles';
  if (pathname === '/owner') return 'owner';
  if (pathname.startsWith('/play/')) return 'player';
  if (pathname === '/home' || pathname === '/movies' || pathname === '/tv' || pathname === '/search' || pathname.startsWith('/detail/')) return 'browse';
  return 'loading';
}

export function App() {
  const [status, setStatus] = useState<SetupStatus>();
  const [route, setRoute] = useState<Route>(routeFor());
  const [path, setPath] = useState(() => `${window.location.pathname}${window.location.search}`);
  const [restoreFocusID, setRestoreFocusID] = useState<string>();
  const [remoteStart, setRemoteStart] = useState<{ positionMS: number; sequence: number }>();
  const [lastBrowsePath, setLastBrowsePath] = useState(() => routeFor() === 'browse' && !window.location.pathname.startsWith('/detail/') ? `${window.location.pathname}${window.location.search}` : '/home');
  const navigate = useCallback((nextPath: string, replace = false) => {
    window.history[replace ? 'replaceState' : 'pushState']({}, '', nextPath);
    setPath(nextPath);
    const nextRoute = routeFor(nextPath);
    if (nextRoute !== 'player') setRemoteStart(undefined);
    if (nextRoute === 'profiles') screenCoordinator.disconnect();
    if (nextRoute === 'browse' && !nextPath.startsWith('/detail/')) setLastBrowsePath(nextPath);
    setRoute(nextRoute);
  }, []);
  useEffect(() => screenCoordinator.onCommand((command) => {
    if (command.type === 'play') {
      setRemoteStart((previous) => ({ positionMS: command.position_ms, sequence: (previous?.sequence ?? 0) + 1 }));
      navigate(`/play/${encodeURIComponent(command.catalog_id)}`);
    }
    if (command.type === 'handoff') navigate(lastBrowsePath);
  }), [lastBrowsePath, navigate]);
  const load = useCallback(() => api.setupStatus().then((value) => {
    setStatus(value);
    const requested = routeFor();
    if (requested === 'loading' || (!value.claimed && requested !== 'setup') || (value.claimed && requested === 'setup')) navigate(value.claimed ? '/profiles' : '/setup', true);
  }).catch(() => setRoute('failure')), [navigate]);
  useEffect(() => {
    void load();
    const popstate = () => {
      const nextPath = `${window.location.pathname}${window.location.search}`;
      setPath(nextPath);
      const nextRoute = routeFor(nextPath);
      if (nextRoute !== 'player') setRemoteStart(undefined);
      if (nextRoute === 'profiles') screenCoordinator.disconnect();
      if (nextRoute === 'browse' && !nextPath.startsWith('/detail/')) setLastBrowsePath(nextPath);
      if (nextRoute === 'setup') { setRoute('loading'); void load(); return; }
      setRoute(nextRoute);
    };
    window.addEventListener('popstate', popstate);
    return () => window.removeEventListener('popstate', popstate);
  }, [load]);
  if (route === 'loading') return <main><p role="status">Loading local Flixr…</p></main>;
  if (route === 'failure') return <main className="auth-panel"><h1>Flixr is unavailable.</h1><p role="alert">The local server did not respond.</p><button onClick={() => void load()}>Try again</button></main>;
  if (route === 'setup' && status) return <main className="setup-page"><Setup readiness={status.readiness} onCompleted={() => navigate('/home')} /></main>;
  if (route === 'login') return <main><OwnerLogin onLogin={() => navigate('/owner')} /></main>;
  if (route === 'owner') return <Suspense fallback={<RouteFallback />}><Owner onBrowse={() => navigate('/profiles')} onLogout={() => navigate('/profiles')} /></Suspense>;
  if (route === 'player') {
    const catalogID = decodeURIComponent(path.slice('/play/'.length));
    return <Suspense fallback={<RouteFallback />}><Player key={`${catalogID}:${remoteStart?.sequence ?? 'local'}`} catalogID={catalogID} startPositionMS={remoteStart?.positionMS} onExit={() => { setRestoreFocusID(catalogID); navigate(lastBrowsePath); }} /></Suspense>;
  }
  if (route === 'browse') {
    const browsePath = path.startsWith('/detail/') ? lastBrowsePath : path;
    return <Suspense fallback={<RouteFallback />}><Browse onExit={() => navigate('/profiles')} mode={browsePath.startsWith('/search') ? 'search' : browsePath.startsWith('/movies') ? 'movies' : browsePath.startsWith('/tv') ? 'tv' : 'home'} query={new URLSearchParams(browsePath.split('?')[1] ?? '').get('q') ?? ''} detailID={path.startsWith('/detail/') ? decodeURIComponent(path.slice('/detail/'.length)) : undefined} restoreFocusID={restoreFocusID} onFocusRestored={() => setRestoreFocusID(undefined)} onNavigate={navigate} /></Suspense>;
  }
  return <ProfileChooser owner={() => navigate('/login')} onSelected={() => navigate('/home')} />;
}

function RouteFallback() {
  return <main><p role="status" aria-label="Loading page">Loading page…</p></main>;
}
