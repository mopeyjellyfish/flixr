import { BootSplash } from '../modules/ui/BootSplash';
import { CopyButton } from '../vendor/interior/copy-button';
import { MotionConfig } from 'motion/react';
import { Splash } from '../modules/ui/Feedback';
import { lazy, Suspense, useCallback, useEffect, useState } from 'react';
import { api } from '../api/client';
import type { OwnerRoots, SetupStatus } from '../core/api';
import { Setup, OwnerLogin } from '../features/setup/Setup';
import { ProfileChooser } from '../features/profiles/Profiles';
import { screenCoordinator } from '../modules/screenCoordinator/runtime';

const InteriorGallery = lazy(() => import('../features/demo/InteriorGallery'));
const Owner = lazy(async () => ({ default: (await import('../features/owner/Owner')).Owner }));
const Browse = lazy(async () => ({ default: (await import('../features/browse/Browse')).Browse }));
const History = lazy(async () => ({ default: (await import('../features/history/History')).History }));
const Player = lazy(async () => ({ default: (await import('../modules/mediaPlayer/Player')).Player }));

type Route = 'loading' | 'setup' | 'login' | 'profiles' | 'owner' | 'browse' | 'history' | 'player' | 'failure' | 'gallery';
function routeFor(path = window.location.pathname): Route {
  const pathname = path.split(/[?#]/, 1)[0];
  if (pathname === '/dev/interior') return 'gallery';
  if (pathname === '/setup') return 'setup';
  if (pathname === '/login') return 'login';
  if (pathname === '/profiles') return 'profiles';
  if (pathname === '/owner') return 'owner';
  if (pathname === '/history') return 'history';
  if (pathname.startsWith('/play/')) return 'player';
  if (pathname === '/home' || pathname === '/movies' || pathname === '/tv' || pathname === '/search' || pathname.startsWith('/detail/')) return 'browse';
  return 'loading';
}

export function App() {
  const [booting, setBooting] = useState(true);
  const [contentReady, setContentReady] = useState(false);
  const ready = useCallback(() => setContentReady(true), []);
  const finishBoot = useCallback(() => setBooting(false), []);
  const [status, setStatus] = useState<SetupStatus>();
  const [setupRoots, setSetupRoots] = useState<OwnerRoots>();
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
  const load = useCallback(() => api.setupStatus().then(async (value) => {
    const requested = routeFor();
    if (value.claimed && requested === 'setup') {
      try {
        const [roots, { profiles }] = await Promise.all([api.ownerRoots(), api.profiles()]);
        if (profiles.length === 0) {
          setSetupRoots(roots);
          setStatus(value);
          setRoute('setup');
          return;
        }
      } catch { /* The owner can sign in from the profile chooser to resume setup. */ }
      navigate('/profiles', true);
    } else if (requested === 'loading' || (!value.claimed && requested !== 'setup')) navigate(value.claimed ? '/profiles' : '/setup', true);
    else setRoute(requested);
    setStatus(value);
  }).catch(() => setRoute('failure')), [navigate]);
  const ownerLoggedIn = async () => {
    try {
      const { profiles } = await api.profiles();
      if (profiles.length === 0) {
        navigate('/setup', true);
        setRoute('loading');
        await load();
        return;
      }
    } catch { /* Owner operations reports profile-loading errors. */ }
    navigate('/owner');
  };
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
  const renderRoute = () => {
  if (route === 'failure') return <main className="auth-panel"><h1>Flixr is unavailable.</h1><p role="alert">The local server did not respond.</p><button onClick={() => void load()}>Try again</button></main>;
  if (route === 'loading' || !status) return <Splash />;
  if (route === 'gallery') return status.demo || import.meta.env.DEV ? <Suspense fallback={<Splash />}><InteriorGallery /></Suspense> : <main><h1>Development tools are unavailable.</h1><a href="/profiles">Back to profiles</a></main>;
  if (route === 'setup') return <main className="setup-page"><Setup readiness={status.readiness} initialRoots={setupRoots} onCompleted={() => navigate('/home')} /></main>;
  if (route === 'login') return <main><OwnerLogin onLogin={() => void ownerLoggedIn()} /></main>;
  if (route === 'owner') return <Suspense fallback={<RouteFallback />}><Owner onBrowse={() => navigate('/profiles')} onLogout={() => navigate('/profiles')} /></Suspense>;
  if (route === 'history') return <Suspense fallback={<RouteFallback />}><History onBrowse={() => navigate('/home')} onExit={() => navigate('/profiles')} /></Suspense>;
  if (route === 'player') {
    const catalogID = decodeURIComponent(path.slice('/play/'.length));
    return <Suspense fallback={<RouteFallback />}><Player key={remoteStart?.sequence ?? 'local'} catalogID={catalogID} active={!booting} startPositionMS={remoteStart?.positionMS} onAdvance={(nextID) => navigate(`/play/${encodeURIComponent(nextID)}`, true)} onExit={() => { setRestoreFocusID(catalogID); navigate(lastBrowsePath); }} /></Suspense>;
  }
  if (route === 'browse') {
    const browsePath = path.startsWith('/detail/') ? lastBrowsePath : path;
    return <Suspense fallback={<RouteFallback />}><Browse onReady={ready} onExit={() => navigate('/profiles')} mode={browsePath.startsWith('/search') ? 'search' : browsePath.startsWith('/movies') ? 'movies' : browsePath.startsWith('/tv') ? 'tv' : 'home'} query={new URLSearchParams(browsePath.split('?')[1] ?? '').get('q') ?? ''} detailID={path.startsWith('/detail/') ? decodeURIComponent(path.slice('/detail/'.length)) : undefined} restoreFocusID={restoreFocusID} onFocusRestored={() => setRestoreFocusID(undefined)} onNavigate={navigate} /></Suspense>;
  }
  return <ProfileChooser onReady={ready} owner={() => navigate('/login')} onSelected={() => { setContentReady(false); setBooting(true); navigate('/home'); }} />;
  };
  return <MotionConfig reducedMotion="user">{booting && <BootSplash ready={contentReady || route === 'failure' || (Boolean(status) && !['loading', 'profiles', 'browse'].includes(route))} onComplete={finishBoot} />}<div data-app-content inert={booting} aria-hidden={booting || undefined} className={booting ? 'app-awaiting' : 'app-revealed'}>{status?.demo && <aside className="demo-banner" aria-label="Development demo"><details><summary>Development demo · artwork and metadata only</summary><div><p>Explore browsing, search, rows and grids, My List, sample Continue Watching, series details, profiles, and owner settings. No media files are included; playback and remote playback are unavailable.</p><p>New demo credentials: owner <code>flixr-demo-only</code> · Sam’s profile PIN <code>2468</code>. Sample history and lists are editable and persist.</p><nav aria-label="Demo shortcuts"><a href="/home">Browse</a><a href="/profiles">Profiles</a><a href="/login">Owner settings</a><a href="/dev/interior">Interior playground</a><CopyButton value={window.location.origin} label="Copy demo link" /></nav><p className="demo-credits">{status.demo_source}</p></div></details></aside>}{renderRoute()}</div></MotionConfig>;

}

function RouteFallback() {
  return <Splash label="Loading page" />;
}
