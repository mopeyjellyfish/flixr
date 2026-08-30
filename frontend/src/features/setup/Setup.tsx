import { useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError } from '../../core/api';

type ReadinessProps = {
  readiness: { ffprobe: boolean; ffmpeg: boolean };
};

export function Readiness({ readiness }: ReadinessProps) {
  const ready = readiness.ffprobe && readiness.ffmpeg;

  return (
    <aside className="readiness" aria-label="Local readiness">
      <strong>{ready ? 'Local tools ready' : 'Local tools need attention'}</strong>
      {!readiness.ffprobe && <p>ffprobe is unavailable. Install FFmpeg tools before starting a scan.</p>}
      {!readiness.ffmpeg && <p>ffmpeg is unavailable. Direct play can continue, but remux and transcode will be unavailable.</p>}
    </aside>
  );
}

type SetupProps = ReadinessProps & { onClaimed: () => void };

export function Setup({ readiness, onClaimed }: SetupProps) {
  const [token, setToken] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    try {
      await api.claim(token, password);
      onClaimed();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to claim Flixr.');
    }
  };

  return (
    <section className="auth-panel">
      <p className="eyebrow">FIRST-RUN SETUP</p>
      <h1>Claim this local Flixr.</h1>
      <p>Use the one-time token shown in the server console. It is never sent by the status endpoint.</p>
      <Readiness readiness={readiness} />
      <form onSubmit={submit}>
        <label>
          Setup token
          <input required autoComplete="off" value={token} onChange={(event) => setToken(event.target.value)} />
        </label>
        <label>
          Owner password
          <input required minLength={8} type="password" autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} />
        </label>
        {error && <p role="alert">{error}</p>}
        <button className="primary">Claim Flixr</button>
      </form>
    </section>
  );
}

export function OwnerLogin({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    try {
      await api.ownerLogin(password);
      onLogin();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to sign in.');
    }
  };

  return (
    <section className="auth-panel">
      <p className="eyebrow">OWNER</p>
      <h1>Owner sign in</h1>
      <form onSubmit={submit}>
        <label>
          Owner password
          <input required autoFocus type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
        </label>
        {error && <p role="alert">{error}</p>}
        <button className="primary">Sign in</button>
      </form>
    </section>
  );
}
