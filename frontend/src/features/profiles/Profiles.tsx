import { useEffect, useRef, useState } from 'react';
import { api } from '../../api/client';
import { ApiError, type Profile } from '../../core/api';
import { Wordmark } from '../../modules/productChrome/Wordmark';

export function ProfileChooser({ onSelected, owner }: { onSelected: () => void; owner: () => void }) {
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [pending, setPending] = useState<Profile>();
  const [pin, setPin] = useState('');
  const [error, setError] = useState('');
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { api.profiles().then((response) => setProfiles(response.profiles)).catch(() => setError('Profiles are unavailable.')); }, []);
  const choose = async (profile: Profile, value = '') => {
    try {
      await api.selectProfile(profile.id, value);
      dialog.current?.close();
      onSelected();
    } catch (cause) { setError(cause instanceof ApiError ? cause.message : 'Unable to select profile.'); }
  };
  const open = (profile: Profile) => {
    setError('');
    if (!profile.protected) void choose(profile);
    else { setPending(profile); setPin(''); dialog.current?.showModal(); }
  };
  return <section className="profile-choice"><header><Wordmark /><button onClick={owner}>Owner sign in</button></header><p className="eyebrow">WHO IS WATCHING?</p><h1>Choose your profile</h1>{error && !pending && <p role="alert">{error}</p>}<div className="profile-grid">{profiles.map((profile) => <button key={profile.id} className="profile" onClick={() => open(profile)}>{profile.name}<span>{profile.protected ? 'PIN protected' : 'Ready'}</span></button>)}</div>{profiles.length === 0 && !error && <p>Ask the owner to create a household profile.</p>}<dialog ref={dialog} aria-labelledby="pin-title" onClose={() => setPending(undefined)}><form onSubmit={(event) => { event.preventDefault(); if (pending) void choose(pending, pin); }}><h2 id="pin-title">Enter PIN for {pending?.name}</h2><label>Profile PIN<input required autoFocus inputMode="numeric" type="password" value={pin} onChange={(event) => setPin(event.target.value)} /></label>{error && <p role="alert">{error}</p>}<div className="actions"><button type="button" onClick={() => dialog.current?.close()}>Cancel</button><button className="primary">Continue</button></div></form></dialog></section>;
}
