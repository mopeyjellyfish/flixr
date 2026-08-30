import { lazy, Suspense, useEffect, useState } from 'react';
import { api } from '../api/client';
import type { SetupStatus } from '../core/api';
import { Setup, OwnerLogin } from '../features/setup/Setup';
import { ProfileChooser } from '../features/profiles/Profiles';

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
  if (pathname === '/home' || pathname === '/search' || pathname.startsWith('/detail/')) return 'browse';
  return 'loading';
}

export function App() {
  const [status, setStatus] = useState<SetupStatus>();
  const [route, setRoute] = useState<Route>(routeFor());
  const [path, setPath] = useState(window.location.pathname);
  const [restoreFocusID, setRestoreFocusID] = useState<string>();
  const navigate = (nextPath: string, replace = false) => {
    window.history[replace ? 'replaceState' : 'pushState']({}, '', nextPath);
    setPath(nextPath);
    setRoute(routeFor(nextPath));
  };
  const load = () => api.setupStatus().then((value) => {
    setStatus(value);
    const requested = routeFor();
    if (requested === 'loading' || (!value.claimed && requested !== 'setup') || (value.claimed && requested === 'setup')) navigate(value.claimed ? '/profiles' : '/setup', true);
  }).catch(() => setRoute('failure'));
  useEffect(() => {
    void load();
    const popstate = () => {
      const nextPath = window.location.pathname;
      setPath(nextPath);
      if (routeFor(nextPath) === 'setup') { setRoute('loading'); void load(); return; }
      setRoute(routeFor(nextPath));
    };
    window.addEventListener('popstate', popstate);
    return () => window.removeEventListener('popstate', popstate);
  }, []);
  if (route === 'loading') return <main><p role="status">Loading local Flixr…</p></main>;
  if (route === 'failure') return <main className="auth-panel"><h1>Flixr is unavailable.</h1><p role="alert">The local server did not respond.</p><button onClick={() => void load()}>Try again</button></main>;
  if (route === 'setup' && status) return <main className="setup-page"><Setup readiness={status.readiness} onCompleted={() => navigate('/home')} /></main>;
  if (route === 'login') return <main><OwnerLogin onLogin={() => navigate('/owner')} /></main>;
  if (route === 'owner') return <Suspense fallback={<RouteFallback />}><Owner onBrowse={() => navigate('/profiles')} onLogout={() => navigate('/profiles')} /></Suspense>;
  if (route === 'player') {
    const catalogID = decodeURIComponent(path.slice('/play/'.length));
    return <Suspense fallback={<RouteFallback />}><Player catalogID={catalogID} onExit={() => { setRestoreFocusID(catalogID); navigate('/home'); }} /></Suspense>;
  }
  if (route === 'browse') return <Suspense fallback={<RouteFallback />}><Browse onExit={() => navigate('/profiles')} mode={path.startsWith('/search') ? 'search' : 'home'} detailID={path.startsWith('/detail/') ? decodeURIComponent(path.slice('/detail/'.length)) : undefined} restoreFocusID={restoreFocusID} onFocusRestored={() => setRestoreFocusID(undefined)} onNavigate={navigate} /></Suspense>;
  return <ProfileChooser owner={() => navigate('/login')} onSelected={() => navigate('/home')} />;
}

function RouteFallback() {
  return <main><p role="status" aria-label="Loading page">Loading page…</p></main>;
}
