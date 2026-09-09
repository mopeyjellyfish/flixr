import { BusyButton } from '../../modules/ui/Feedback';
import { useAsyncAction } from '../../vendor/interior/loading-button';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type OwnerRoots, type Profile, type SetupCheck, type SetupStep, type TMDBSettings } from '../../core/api';
import { Wordmark } from '../../modules/productChrome/Wordmark';

type ReadinessProps = {
  readiness: { ffprobe: boolean; ffmpeg: boolean };
};

export function Readiness({ readiness }: ReadinessProps) {
  const ready = readiness.ffprobe && readiness.ffmpeg;

  return (
    <aside className={`readiness ${ready ? 'is-ready' : ''}`} aria-label="Local readiness">
      <strong>{ready ? 'Local tools ready' : 'Local tools need attention'}</strong>
      {!readiness.ffprobe && <p>ffprobe is unavailable. Install FFmpeg tools before starting a scan.</p>}
      {!readiness.ffmpeg && <p>ffmpeg is unavailable. Direct play can continue, but remux and transcode will be unavailable.</p>}
    </aside>
  );
}

type SetupProps = ReadinessProps & { onCompleted: () => void; initialRoots?: OwnerRoots; initialStep?: SetupStep; initialChecks?: SetupCheck[]; metadata?: TMDBSettings };
type LocalSetupStep = 'secure' | SetupStep;

export function Setup({ readiness, metadata, onCompleted, initialRoots, initialStep, initialChecks = [] }: SetupProps) {
  const [step, setStep] = useState<LocalSetupStep>(initialStep === 'complete' ? 'profile' : initialStep ?? (initialRoots ? 'libraries' : 'secure'));
  const [token, setToken] = useState('');
  const [password, setPassword] = useState('');
  const [films, setFilms] = useState(initialRoots?.films ?? '');
  const [tv, setTV] = useState(initialRoots?.tv ?? '');
  const [tmdbToken, setTMDBToken] = useState('');
  const [name, setName] = useState('');
  const [pin, setPin] = useState('');
  const [createdProfile, setCreatedProfile] = useState<Profile>();
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [checks, setChecks] = useState<SetupCheck[]>(initialChecks);
  const [busy, setBusy] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const firstStep = useRef(true);
  const headingText = step === 'secure' ? 'Let’s set up your local cinema.' : step === 'choice' ? 'How would you like to begin?' : step === 'libraries' ? 'Bring your libraries home.' : 'Set up your first profile.';
  useEffect(() => {
    if (firstStep.current) { firstStep.current = false; return; }
    heading.current?.focus();
  }, [step]);

  const claim = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    setBusy(true);
    try {
      await api.claim(token, password);
      setStep('choice');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to claim Flixr.');
    } finally {
      setBusy(false);
    }
  };
  const moveTo = async (next: SetupStep) => {
    setError('');
    setBusy(true);
    try {
      await api.updateSetup(next);
      setStep(next);
      if (next === 'libraries') {
        const setup = await api.ownerSetup(films, tv);
        setChecks(setup.checks);
      }
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to save setup progress.');
    } finally {
      setBusy(false);
    }
  };
  const recheck = async () => {
    setError('');
    setBusy(true);
    try {
      const setup = await api.ownerSetup(films, tv);
      setChecks(setup.checks);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to recheck local folders and tools.');
    } finally {
      setBusy(false);
    }
  };
  const saveLibraries = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    setNotice('');
    if (!films && !tv) { setError('Add a film or TV library, or skip for now.'); return; }
    setBusy(true);
    try {
      await api.roots(films, tv);
      if (tmdbToken.trim()) await api.saveTMDBToken(tmdbToken);
      if (!readiness.ffprobe) {
        setNotice('Libraries saved. ffprobe is unavailable, so scanning will wait. Direct play still works, and you can start a scan later after installing FFmpeg tools.');
      } else {
        void api.scan().then(() => { if (tmdbToken.trim()) setNotice('Libraries saved. TMDB credential verified, and metadata enrichment is running.'); }).catch((cause: unknown) => setNotice(cause instanceof ApiError ? `Libraries saved. ${cause.message} You can start a scan later from owner settings.` : 'Libraries saved, but Flixr could not start a scan. You can start one later from owner settings.'));
      }
      setStep('profile');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to save libraries or metadata credential.');
    } finally {
      setBusy(false);
    }
  };
  const createProfile = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    setBusy(true);
    let selecting = false;
    try {
      const profile = createdProfile ?? await api.createProfile(name, pin);
      if (!createdProfile) setCreatedProfile(profile);
      selecting = true;
      await api.selectProfile(profile.id, pin);
      onCompleted();
    } catch (cause) {
      const fallback = selecting ? 'Unable to select the new profile.' : 'Unable to create profile.';
      setError(cause instanceof ApiError ? cause.message : fallback);
    } finally {
      setBusy(false);
    }
  };
  const stepIndex = ['secure', 'choice', 'libraries', 'profile'].indexOf(step);

  return (
    <section className="setup-flow">
      <header className="setup-header"><Wordmark /><span className="quiet-status">● Connected on your network</span></header>
      <nav className="setup-progress" aria-label="Setup progress">
        {(['Secure', 'Begin', 'Libraries', 'Profile'] as const).map((label, index) => <div key={label} className={index <= stepIndex ? 'active' : ''} aria-current={index === stepIndex ? 'step' : undefined}><span>{index + 1}</span><strong>{label}</strong></div>)}
      </nav>
      <div className="setup-intro">
        <p className="eyebrow">LOCAL SETUP</p>
        <h1 ref={heading} tabIndex={-1}>{headingText}</h1>
        <p>{step === 'secure' ? 'Four quick steps. No Flixr account, cloud connection, or metadata provider is required.' : step === 'choice' ? 'Start with empty household settings today. Existing-server import is reserved for a future release.' : step === 'libraries' ? 'Use paths inside the Flixr container. The standard Compose mount provides /media/films and /media/tv.' : 'Profiles stay in your home. A PIN is optional.'}</p>
      </div>
      {step === 'secure' && <><Readiness readiness={readiness} /><form onSubmit={claim} aria-busy={busy}><p className="setup-token-help">The setup token is shown in the server console and is never exposed by status.</p><label>Setup token<input required autoComplete="off" value={token} onChange={(event) => setToken(event.target.value)} placeholder="Paste the token from your server" /></label><label>Owner password<input required minLength={8} type="password" autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Create a private password" /></label>{error && <p role="alert">{error}</p>}<button className="primary" disabled={busy}>Secure this server →</button></form></>}
      {step === 'choice' && <section className="setup-choice" aria-busy={busy}><button className="primary" disabled={busy} onClick={() => void moveTo('libraries')}>Start fresh →</button><button aria-disabled="true" aria-describedby="import-availability" onClick={(event) => event.preventDefault()}>Import an existing server</button><p id="import-availability" className="setup-note">Existing-server import will become available in a future release. Your owner account is saved, so you can safely leave setup and return later.</p>{error && <p role="alert">{error}</p>}</section>}
      {step === 'libraries' && <form onSubmit={saveLibraries} aria-busy={busy}><label>Films library<input value={films} onChange={(event) => setFilms(event.target.value)} placeholder="/media/films" /></label><label>TV library<input value={tv} onChange={(event) => setTV(event.target.value)} placeholder="/media/tv" /></label><button type="button" disabled={busy} onClick={() => void recheck()}>Recheck folders and tools</button>{checks.length > 0 && <SetupChecks checks={checks} />}<p className="setup-note">{metadata?.message ?? 'This build has no automatic metadata access. Local playback remains available.'}</p><details><summary>Advanced: personal TMDB credential</summary><label>TMDB API Read Access Token (optional)<input type="password" value={tmdbToken} onChange={(event) => setTMDBToken(event.target.value)} autoComplete="new-password" /></label><p className="setup-note"><a href="https://www.themoviedb.org/settings/api">Get a TMDB API Read Access Token</a> to override application access. Flixr verifies it before saving.</p></details>{error && <p role="alert">{error}</p>}<div className="setup-actions"><button className="primary" disabled={busy}>Save libraries</button><button type="button" disabled={busy} onClick={() => void moveTo('profile')}>Skip for now</button><button type="button" disabled={busy} onClick={() => void moveTo('choice')}>Back to setup choices</button></div></form>}
      {step === 'profile' && <form onSubmit={createProfile} aria-busy={busy}>{notice && <p role="status">{notice}</p>}{createdProfile && <p role="status">Profile created. Retry to enter Flixr.</p>}<label>Name<input required disabled={Boolean(createdProfile)} value={name} onChange={(event) => setName(event.target.value)} placeholder="Your name" /></label><label>PIN (optional)<input disabled={Boolean(createdProfile)} type="password" inputMode="numeric" value={pin} onChange={(event) => setPin(event.target.value)} placeholder="For a little privacy" /></label>{error && <p role="alert">{error}</p>}<div className="setup-actions"><button className="primary" disabled={busy}>{createdProfile ? 'Retry entering Flixr →' : 'Create profile and enter Flixr →'}</button><button type="button" disabled={busy || Boolean(createdProfile)} onClick={() => void moveTo('libraries')}>Back to libraries</button></div></form>}
      <p className="setup-promises"><span><b>✓</b> Stays on your LAN</span><span><b>✓</b> Metadata is optional</span><span><b>✓</b> Change settings anytime</span></p>
    </section>
  );
}

function SetupChecks({ checks }: { checks: SetupCheck[] }) {
  return <section className="setup-checks" aria-label="Setup diagnostics">{checks.map((check) => {
    const failed = !['ready', 'not_configured'].includes(check.state);
    return <article key={check.id} className={failed ? 'needs-attention' : 'is-ready'}><div><strong>{check.label}</strong>{check.path && <code>{check.path}</code>}</div><p role={failed ? 'alert' : undefined}>{check.message}</p>{check.action && <p>{check.action}</p>}</article>;
  })}</section>;
}

export function OwnerLogin({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const action = useAsyncAction({ action: async () => { setError(''); await api.ownerLogin(password); onLogin(); }, onError: (cause) => setError(cause instanceof ApiError ? cause.message : 'Unable to sign in.') });
  return <section className="auth-panel owner-login"><Wordmark /><p className="eyebrow">YOUR HOUSE. YOUR RULES.</p><h1>Owner sign in</h1><p>Make Flixr feel like home. Manage your library, profiles, and screens.</p><form onSubmit={(event) => { event.preventDefault(); action.run(); }} aria-busy={action.pending}><input className="sr-only" name="username" autoComplete="username" value="owner" readOnly tabIndex={-1} aria-hidden="true" /><label>Owner password<input required autoFocus type="password" autoComplete="current-password" value={password} disabled={action.pending} onChange={(event) => setPassword(event.target.value)} /></label>{error && <p role="alert">{error}</p>}<BusyButton className="primary" busy={action.pending}>Sign in</BusyButton></form><a className="button-link" href="/profiles">← Back to profiles</a><p className="local-note">This account belongs to your local server.</p></section>;
}
