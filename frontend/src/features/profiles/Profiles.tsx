import { useEffect, useId, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type Profile } from '../../core/api';
import { Wordmark } from '../../modules/productChrome/Wordmark';
import { AppModal } from '../../modules/ui/AppModal';
import { Avatar, BusyButton } from '../../modules/ui/Feedback';
import { PinCells } from '../../modules/ui/PinCells';

export function ProfileChooser({ onSelected, owner, onReady }: { onSelected: () => void; owner: () => void; onReady?: () => void }) {
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState<Profile>();
  const [selecting, setSelecting] = useState<string>();
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  const busy = useRef(false);
  useEffect(() => {
    let active = true;
    setLoading(true); setError('');
    api.profiles().then((response) => { if (active) setProfiles(response.profiles); }).catch(() => { if (active) setError('Profiles are unavailable.'); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [retry]);
  useEffect(() => { if (!loading) onReady?.(); }, [loading, onReady]);
  const choose = async (profile: Profile, value = '') => {
    if (busy.current) return false;
    busy.current = true; setSelecting(profile.id); setError('');
    try {
      await api.selectProfile(profile.id, value);
      setPending(undefined); onSelected();
      return true;
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to select profile.');
      return false;
    } finally { busy.current = false; setSelecting(undefined); }
  };
  const open = (profile: Profile) => {
    setError('');
    if (!profile.protected) void choose(profile);
    else setPending(profile);
  };
  return <section className="profile-choice">
    <header><Wordmark /><button className="quiet-button" onClick={owner}>Owner settings <span aria-hidden="true">↗</span></button></header>
    <div className="profile-stage"><p className="eyebrow">MAKE YOURSELF AT HOME</p><h1>Who’s watching?</h1><p className="profile-subtitle">Your favourites. Your place to pick up.</p>
      {error && !pending && <div className="inline-alert" role="alert"><p>{error}</p><button onClick={() => setRetry((value) => value + 1)}>Try again</button></div>}
      <div className="profile-grid" aria-busy={loading || Boolean(selecting)}>{loading ? <div className="profile-placeholders" role="status" aria-label="Loading profiles">{[0, 1, 2].map((i) => <span key={i} />)}</div> : profiles.map((profile) => <button key={profile.id} className={`profile ${selecting === profile.id ? 'is-selecting' : ''}`} disabled={Boolean(selecting)} onClick={() => open(profile)}><Avatar name={profile.name} src={profile.avatar} large /><strong>{profile.name}</strong><span>{selecting === profile.id ? 'Opening…' : profile.protected ? 'PIN protected' : 'Ready'}</span></button>)}</div>
      {!loading && profiles.length === 0 && !error && <div className="empty-state"><p className="eyebrow">NO PROFILES YET</p><h2>Set up your household</h2><p>Each profile keeps its own list and viewing progress. The owner adds profiles from settings.</p><button className="primary" onClick={owner}>Sign in as owner</button></div>}
      <p className="local-note"><span aria-hidden="true">●</span> On your server. Always at home.</p>
    </div>
    {pending && <PinDialog profile={pending} error={error} busy={Boolean(selecting)} onSubmit={(pin) => choose(pending, pin)} onClose={() => { if (!busy.current) { setPending(undefined); setError(''); } }} />}
  </section>;
}

function PinDialog({ profile, error, busy, onSubmit, onClose }: { profile: Profile; error: string; busy: boolean; onSubmit: (pin: string) => Promise<boolean>; onClose: () => void }) {
  const [longPin, setLongPin] = useState(false);
  const [pin, setPin] = useState('');
  const [attempt, setAttempt] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const hintId = useId();
  const submit = async (value: string) => {
    if (!value) return;
    if (!(await onSubmit(value))) {
      // Clear the cells or reselect the field so the next attempt starts clean.
      setAttempt((value) => value + 1);
      setPin('');
      input.current?.focus();
    }
  };
  const switchMode = () => { setLongPin((value) => !value); setPin(''); setAttempt((value) => value + 1); };
  const complete = longPin ? pin.length >= 4 : pin.length === 4;
  return <AppModal open onClose={onClose} locked={busy} className="pin-dialog" title={`Enter PIN for ${profile.name}`} description="This profile is locked. Enter its PIN to continue.">
    <form onSubmit={(event) => { event.preventDefault(); void submit(pin); }} aria-busy={busy}>
      <Avatar name={profile.name} src={profile.avatar} />
      {longPin
        ? <label>Profile PIN<input ref={input} required autoFocus autoComplete="current-password" inputMode="numeric" type="password" value={pin} disabled={busy} aria-describedby={hintId} aria-invalid={Boolean(error) || undefined} onChange={(event) => setPin(event.target.value)} /></label>
        : <PinCells key={attempt} label="Profile PIN" autoFocus disabled={busy} invalid={Boolean(error)} describedBy={hintId} onChange={setPin} onComplete={(value) => void submit(value)} />}
      <p id={hintId} className={error ? 'form-error' : 'field-hint'} role={error ? 'alert' : undefined}>{error || (longPin ? 'Enter your full PIN, then continue.' : 'Four digits. The profile opens on the last one.')}</p>
      <button type="button" className="link-button" disabled={busy} onClick={switchMode}>{longPin ? 'Use a 4-digit PIN' : 'My PIN is longer than 4 digits'}</button>
      <div className="actions"><button type="button" disabled={busy} className="quiet-button" onClick={onClose}>Cancel</button><BusyButton className="primary" busy={busy} disabled={!complete}>Continue</BusyButton></div>
    </form>
  </AppModal>;
}
