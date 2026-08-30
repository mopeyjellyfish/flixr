import { useEffect, useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type Readiness, type Scan } from '../../core/api';
import { Readiness as ReadinessPanel } from '../setup/Setup';

type OwnerProps = {
  onLogout: () => void;
  onBrowse: () => void;
};

export function Owner({ onLogout, onBrowse }: OwnerProps) {
  const [readiness, setReadiness] = useState<Readiness>();
  const [scan, setScan] = useState<Scan>();
  const [films, setFilms] = useState('');
  const [tv, setTV] = useState('');
  const [tmdbConfigured, setTMDBConfigured] = useState(false);
  const [tmdbToken, setTMDBToken] = useState('');
  const [name, setName] = useState('');
  const [pin, setPin] = useState('');
  const [notice, setNotice] = useState('');
  const [profilesVersion, setProfilesVersion] = useState(0);

  const load = () => {
    api.setupStatus().then((status) => setReadiness(status.readiness)).catch((error) => setNotice(error instanceof ApiError ? error.message : 'Readiness is unavailable.'));
    api.scanStatus().then((result) => setScan(result.scan.status ? result.scan : undefined)).catch(() => undefined);
    api.ownerRoots().then((roots) => { setFilms(roots.films); setTV(roots.tv); }).catch(() => setNotice('Library roots are unavailable.'));
    api.tmdbSettings().then((settings) => setTMDBConfigured(settings.configured)).catch(() => setNotice('TMDB settings are unavailable.'));
  };

  const recheck = () => api.recheck().then((status) => setReadiness(status.readiness)).catch((error) => setNotice(error instanceof ApiError ? error.message : 'Readiness is unavailable.'));
  useEffect(load, []);
  useEffect(() => {
    if (scan?.status !== 'running') return;
    const timer = window.setInterval(() => api.scanStatus().then((result) => setScan(result.scan)).catch(() => undefined), 1500);
    return () => clearInterval(timer);
  }, [scan?.status]);

  const roots = async (event: FormEvent) => {
    event.preventDefault();
    try {
      await api.roots(films, tv);
      setNotice('Library roots saved.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to save roots.');
    }
  };
  const saveTMDB = async (event: FormEvent) => {
    event.preventDefault();
    try {
      const settings = await api.saveTMDBToken(tmdbToken);
      setTMDBConfigured(settings.configured);
      setTMDBToken('');
      setNotice('TMDB credential saved.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to save TMDB credential.');
    }
  };
  const removeTMDB = async () => {
    try {
      const settings = await api.removeTMDBToken();
      setTMDBConfigured(settings.configured);
      setTMDBToken('');
      setNotice('TMDB credential removed.');
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to remove TMDB credential.');
    }
  };
  const startScan = async () => {
    try {
      setScan((await api.scan()).scan);
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to start scan.');
    }
  };
  const create = async (event: FormEvent) => {
    event.preventDefault();
    try {
      await api.createProfile(name, pin);
      setName('');
      setPin('');
      setNotice('Profile created.');
      setProfilesVersion((value) => value + 1);
    } catch (error) {
      setNotice(error instanceof ApiError ? error.message : 'Unable to create profile.');
    }
  };

  return (
    <main className="owner">
      <OwnerHeader onBrowse={onBrowse} onLogout={onLogout} />
      <h1>Keep your local cinema ready.</h1>
      {notice && <p role="status">{notice}</p>}
      {readiness && <ReadinessControls readiness={readiness} onRecheck={recheck} />}
      <section className="owner-grid">
        <RootsForm films={films} tv={tv} onFilmsChange={setFilms} onTVChange={setTV} onSubmit={roots} />
        <ScanPanel scan={scan} onStart={startScan} />
        <TMDBForm configured={tmdbConfigured} token={tmdbToken} onTokenChange={setTMDBToken} onSave={saveTMDB} onRemove={removeTMDB} />
        <ProfileForm name={name} pin={pin} onNameChange={setName} onPinChange={setPin} onSubmit={create} />
        <ProfileManager version={profilesVersion} />
      </section>
    </main>
  );
}

function OwnerHeader({ onBrowse, onLogout }: OwnerProps) {
  return (
    <header>
      <b>FLIXR</b>
      <span>Owner operations</span>
      <button onClick={onBrowse}>Household home</button>
      <button onClick={() => void api.logout().finally(onLogout)}>Log out</button>
    </header>
  );
}

function ReadinessControls({ readiness, onRecheck }: { readiness: Readiness; onRecheck: () => Promise<void> }) {
  return (
    <>
      <ReadinessPanel readiness={readiness} />
      <button className="primary" onClick={() => void onRecheck()}>Recheck readiness</button>
    </>
  );
}

function RootsForm({ films, tv, onFilmsChange, onTVChange, onSubmit }: { films: string; tv: string; onFilmsChange: (value: string) => void; onTVChange: (value: string) => void; onSubmit: (event: FormEvent) => void }) {
  return (
    <form onSubmit={onSubmit}>
      <h2>Library roots</h2>
      <label>Films root<input value={films} onChange={(event) => onFilmsChange(event.target.value)} placeholder="/media/films" /></label>
      <label>TV root<input value={tv} onChange={(event) => onTVChange(event.target.value)} placeholder="/media/tv" /></label>
      <button className="primary">Save roots</button>
    </form>
  );
}

function ScanPanel({ scan, onStart }: { scan?: Scan; onStart: () => Promise<void> }) {
  return (
    <section>
      <h2>Manual scan</h2>
      <p>{scan ? `${scan.status}: ${scan.scanned} scanned, ${scan.unmatched} unmatched, ${scan.failed} failed.` : 'No scan has started.'}</p>
      {scan?.message && <p role="alert">{scan.message}</p>}
      <button className="primary" disabled={scan?.status === 'running'} onClick={() => void onStart()}>{scan?.status === 'running' ? 'Scan in progress' : 'Start scan'}</button>
    </section>
  );
}

function TMDBForm({ configured, token, onTokenChange, onSave, onRemove }: { configured: boolean; token: string; onTokenChange: (value: string) => void; onSave: (event: FormEvent) => void; onRemove: () => Promise<void> }) {
  return (
    <form onSubmit={onSave}>
      <h2>TMDB metadata</h2>
      <p>{configured ? 'Credential configured. Enter a replacement token to change it.' : 'No credential configured.'}</p>
      <label>TMDB access token<input type="password" value={token} onChange={(event) => onTokenChange(event.target.value)} autoComplete="new-password" /></label>
      <div className="actions">
        <button className="primary">Save TMDB credential</button>
        {configured && <button type="button" onClick={() => void onRemove()}>Remove credential</button>}
      </div>
    </form>
  );
}

function ProfileForm({ name, pin, onNameChange, onPinChange, onSubmit }: { name: string; pin: string; onNameChange: (value: string) => void; onPinChange: (value: string) => void; onSubmit: (event: FormEvent) => void }) {
  return (
    <form onSubmit={onSubmit}>
      <h2>Household profiles</h2>
      <label>Name<input required value={name} onChange={(event) => onNameChange(event.target.value)} /></label>
      <label>PIN (optional)<input type="password" value={pin} onChange={(event) => onPinChange(event.target.value)} /></label>
      <button className="primary">Create profile</button>
    </form>
  );
}

export function ProfileManager({ version = 0 }: { version?: number }) {
  const [profiles, setProfiles] = useState<{ id: string; name: string; protected: boolean }[]>([]);
  const [notice, setNotice] = useState('');
  const load = () => api.profiles().then((value) => setProfiles(value.profiles)).catch(() => setNotice('Profiles are unavailable.'));
  useEffect(() => { void load(); }, [version]);

  return (
    <section>
      <h2>Manage profiles</h2>
      {notice && <p role="status">{notice}</p>}
      {profiles.map((profile) => <ProfileRow key={profile.id} profile={profile} onSaved={load} onNotice={setNotice} />)}
    </section>
  );
}

function ProfileRow({ profile, onSaved, onNotice }: { profile: { id: string; name: string; protected: boolean }; onSaved: () => void; onNotice: (value: string) => void }) {
  const [name, setName] = useState(profile.name);
  const [pin, setPin] = useState('');
  const save = async (event: FormEvent) => {
    event.preventDefault();
    try {
      await api.updateProfile(profile.id, { name, ...(pin ? { pin } : {}) });
      setPin('');
      onNotice('Profile updated.');
      onSaved();
    } catch (error) {
      onNotice(error instanceof ApiError ? error.message : 'Unable to update profile.');
    }
  };

  return (
    <form onSubmit={save}>
      <strong>{profile.name} {profile.protected ? '· PIN protected' : '· no PIN'}</strong>
      <label>Profile name<input value={name} onChange={(event) => setName(event.target.value)} /></label>
      <label>New PIN<input type="password" value={pin} onChange={(event) => setPin(event.target.value)} /></label>
      <div className="actions">
        <button>Save</button>
        {profile.protected && <button type="button" onClick={() => void api.updateProfile(profile.id, { unprotect: true }).then(onSaved).catch((error) => onNotice(error instanceof ApiError ? error.message : 'Unable to remove PIN.'))}>Remove PIN</button>}
      </div>
    </form>
  );
}
