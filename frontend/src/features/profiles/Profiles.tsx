import { useEffect, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type Profile } from '../../core/api';
import { Wordmark } from '../../modules/productChrome/Wordmark';
import { Avatar, BusyButton } from '../../modules/ui/Feedback';

export function ProfileChooser({ onSelected, owner, onReady }: { onSelected: () => void; owner: () => void; onReady?: () => void }) {
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState<Profile>();
  const [selecting, setSelecting] = useState<string>();
  const [pin, setPin] = useState('');
  const [error, setError] = useState('');
  const busy = useRef(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setLoading(true); setError('');
    api.profiles().then((response) => { if (active) setProfiles(response.profiles); }).catch(() => { if (active) setError('Profiles are unavailable.'); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [retry]);
  useEffect(() => { if (!loading) onReady?.(); }, [loading, onReady]);
  const choose = async (profile: Profile, value = '') => {
    if (busy.current) return;
    busy.current = true; setSelecting(profile.id); setError('');
    try {
      await api.selectProfile(profile.id, value);
      dialog.current?.close(); onSelected();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Unable to select profile.');
      input.current?.select();
    } finally { busy.current = false; setSelecting(undefined); }
  };
  const open = (profile: Profile) => {
    setError('');
    if (!profile.protected) void choose(profile);
    else { setPending(profile); setPin(''); dialog.current?.showModal(); }
  };
  return <section className="profile-choice">
    <header><Wordmark /><button className="quiet-button" onClick={owner}>Owner sign in <span aria-hidden="true">↗</span></button></header>
    <div className="profile-stage"><p className="eyebrow">MAKE YOURSELF AT HOME</p><h1>Who’s watching?</h1><p className="profile-subtitle">Your favourites. Your place to pick up.</p>
      {error && !pending && <div role="alert"><p>{error}</p><button onClick={() => setRetry((value) => value + 1)}>Try again</button></div>}
      <div className="profile-grid" aria-busy={loading || Boolean(selecting)}>{loading ? <div className="profile-placeholders" role="status" aria-label="Loading profiles">{[0, 1, 2].map((i) => <span key={i} />)}</div> : profiles.map((profile) => <button key={profile.id} className={`profile ${selecting === profile.id ? 'is-selecting' : ''}`} disabled={Boolean(selecting)} onClick={() => open(profile)}><Avatar name={profile.name} src={profile.avatar} large /><strong>{profile.name}</strong><span>{selecting === profile.id ? 'Opening…' : profile.protected ? 'PIN protected' : 'Ready'}</span></button>)}</div>
      {!loading && profiles.length === 0 && !error && <p>Ask the owner to create a household profile.</p>}
      <button className="manage-profiles quiet-button" onClick={owner}>Manage profiles</button><p className="local-note"><span aria-hidden="true">●</span> On your server. Always at home.</p>
    </div>
    <dialog className="pin-dialog" ref={dialog} aria-labelledby="pin-title" onCancel={(event) => { if (busy.current) event.preventDefault(); }} onClose={() => { setPending(undefined); setPin(''); setError(''); }} onClick={(event) => { if (event.target === event.currentTarget && !busy.current) dialog.current?.close(); }}>
      <form onSubmit={(event) => { event.preventDefault(); if (pending) void choose(pending, pin); }}><Avatar name={pending?.name ?? ''} /><p className="eyebrow">A LITTLE PRIVACY</p><h2 id="pin-title">Enter PIN for {pending?.name}</h2><p>Your profile is locked. Enter your PIN to continue.</p><label>Profile PIN<input ref={input} required autoFocus autoComplete="current-password" inputMode="numeric" type="password" value={pin} disabled={Boolean(selecting)} onChange={(event) => setPin(event.target.value)} /></label>{error && <p role="alert">{error}</p>}<div className="actions"><button type="button" disabled={Boolean(selecting)} className="quiet-button" onClick={() => dialog.current?.close()}>Cancel</button><BusyButton className="primary" busy={Boolean(selecting)}>Continue</BusyButton></div></form>
    </dialog>
  </section>;
}
