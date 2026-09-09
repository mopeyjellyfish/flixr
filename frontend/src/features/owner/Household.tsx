import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type ActiveSession, type Library, type Profile, type ProfileAccessPolicy } from '../../core/api';
import { useAsyncAction } from '../../vendor/interior/loading-button';
import { useInlineValidation } from '../../vendor/interior/inline-validation';
import { AppModal } from '../../modules/ui/AppModal';
import { Avatar, BusyButton } from '../../modules/ui/Feedback';
import { HoldButton } from '../../modules/ui/HoldButton';
import { PinCells } from '../../modules/ui/PinCells';

type HouseholdProps = { libraries: Library[]; onNotice: (message: string) => void };

export function HouseholdPanel({ libraries, onNotice }: HouseholdProps) {
  const [profiles, setProfiles] = useState<Profile[]>();
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<Profile | 'new'>();
  const load = useCallback(() => api.profiles().then((value) => { setProfiles(value.profiles); setError(''); }).catch(() => setError('Profiles are unavailable.')), []);
  useEffect(() => { void load(); }, [load]);
  const saved = (message: string) => { onNotice(message); void load(); };

  return <div className="household">
    <div className="panel-heading"><div><h3>Profiles</h3><p>Everyone gets their own list, progress, and an optional 4-digit PIN.</p></div>{profiles && profiles.length > 0 && <button type="button" className="primary" onClick={() => setEditing('new')}>Add profile</button>}</div>
    {error && <div className="inline-alert" role="alert"><p>{error}</p><button type="button" onClick={() => void load()}>Try again</button></div>}
    {!profiles && !error && <p role="status" className="muted">Loading profiles…</p>}
    {profiles?.length === 0 && <div className="empty-state"><p className="eyebrow">NO PROFILES YET</p><h4>Add the first person</h4><p>Profiles show up on the Who’s watching screen. Add one for each person, or one shared household profile.</p><button type="button" className="primary" onClick={() => setEditing('new')}>Add profile</button></div>}
    {profiles && profiles.length > 0 && <ul className="profile-list">{profiles.map((profile) => <li key={profile.id} className="profile-item"><Avatar name={profile.name} src={profile.avatar} /><div className="profile-item-text"><strong>{profile.name}</strong><span className={`pill ${profile.protected ? 'is-locked' : ''}`}>{profile.protected ? 'PIN' : 'Open'}</span></div><button type="button" aria-label={`Edit ${profile.name}`} onClick={() => setEditing(profile)}>Edit</button></li>)}</ul>}
    {editing && <ProfileDialog profile={editing === 'new' ? undefined : editing} others={(profiles ?? []).filter((profile) => editing === 'new' || profile.id !== editing.id)} libraries={libraries} onClose={() => setEditing(undefined)} onSaved={saved} onNotice={onNotice} />}
    <SessionManager />
  </div>;
}

type DialogProps = { profile?: Profile; others: Profile[]; libraries: Library[]; onClose: () => void; onSaved: (message: string) => void; onNotice: (message: string) => void };

function ProfileDialog({ profile, others, libraries, onClose, onSaved, onNotice }: DialogProps) {
  const creating = !profile;
  const [name, setName] = useState(profile?.name ?? '');
  const [pin, setPin] = useState('');
  const [pinLength, setPinLength] = useState(4);
  const [settingPin, setSettingPin] = useState(creating);
  const [isProtected, setProtected] = useState(profile?.protected ?? false);
  const [error, setError] = useState('');
  const [pinReset, setPinReset] = useState(0);
  const nameRef = useRef<HTMLInputElement>(null);
  const pinHintId = useId();
  const validation = useInlineValidation({ value: name, validate: (value) => {
    const trimmed = value.trim();
    if (!trimmed) return 'Give this profile a name.';
    if (others.some((other) => other.name.trim().toLowerCase() === trimmed.toLowerCase())) return 'Another profile already uses this name.';
    return null;
  } });
  const action = useAsyncAction({
    action: async () => {
      setError('');
      const trimmed = name.trim();
      validation.commit();
      if (!trimmed || others.some((other) => other.name.trim().toLowerCase() === trimmed.toLowerCase())) throw new Error('name');
      if (settingPin && pin.length > 0 && pin.length < pinLength) throw new Error(`Enter all ${pinLength} digits, or leave the PIN empty.`);
      if (creating) { await api.createProfile(trimmed, settingPin ? pin : ''); onSaved(`${trimmed} added to the household.`); }
      else {
        const changes: { name?: string; pin?: string } = {};
        if (trimmed !== profile.name) changes.name = trimmed;
        if (settingPin && pin) changes.pin = pin;
        if (Object.keys(changes).length > 0) await api.updateProfile(profile.id, changes);
        onSaved(changes.pin ? `${trimmed} updated. Devices using this profile will need the new PIN.` : `${trimmed} updated.`);
      }
      onClose();
    },
    onError: (cause) => { if (cause instanceof Error && cause.message === 'name') nameRef.current?.focus(); else setError(cause instanceof ApiError ? cause.message : cause instanceof Error ? cause.message : 'Unable to save this profile.'); },
  });
  const removePin = async () => {
    if (!profile) return;
    setError('');
    try { await api.updateProfile(profile.id, { unprotect: true }); setProtected(false); setSettingPin(false); onSaved(`${profile.name} no longer needs a PIN.`); }
    catch (cause) { setError(cause instanceof ApiError ? cause.message : 'Unable to remove the PIN.'); }
  };
  const remove = async () => {
    if (!profile) return;
    setError('');
    try { await api.deleteProfile(profile.id); onSaved(`${profile.name} was deleted.`); onClose(); }
    catch (cause) { setError(cause instanceof ApiError ? cause.message : 'Unable to delete this profile.'); }
  };
  const changeLength = (length: number) => { setPinLength(length); setPin(''); setPinReset((value) => value + 1); };
  const submit = (event: FormEvent) => { event.preventDefault(); action.run(); };
  const busy = action.pending;

  return <AppModal open onClose={onClose} locked={busy} className="profile-dialog" initialFocusRef={nameRef} title={creating ? 'New profile' : `Edit ${profile.name}`} description={creating ? 'Profiles appear on the Who’s watching screen.' : 'Changes apply the next time this profile is chosen.'}>
    <form onSubmit={submit} aria-busy={busy} className="profile-dialog-form">
      <label>Name<input ref={nameRef} required value={name} disabled={busy} autoComplete="off" {...validation.fieldProps} onChange={(event) => setName(event.target.value)} /></label>
      {validation.error && <p className="form-error" role="alert">{validation.error}</p>}
      <fieldset className="pin-field">
        <legend>{creating ? 'PIN' : 'PIN protection'}</legend>
        {!creating && !settingPin && <div className="pin-status"><span className={`pill ${isProtected ? 'is-locked' : ''}`}>{isProtected ? 'PIN protected' : 'No PIN'}</span><button type="button" disabled={busy} onClick={() => { setSettingPin(true); setPin(''); }}>{isProtected ? 'Change PIN' : 'Add a PIN'}</button>{isProtected && <HoldButton className="danger" disabled={busy} onConfirm={() => void removePin()} confirmedLabel="PIN removed">Remove PIN</HoldButton>}</div>}
        {settingPin && <>
          <PinCells key={`${pinLength}-${pinReset}`} label={creating ? 'PIN' : 'New PIN'} length={pinLength} disabled={busy} describedBy={pinHintId} onChange={setPin} />
          <p id={pinHintId} className="field-hint">{creating ? 'Optional. Leave empty for an open profile.' : 'Leave empty to keep the current PIN.'} <button type="button" className="link-button" disabled={busy} onClick={() => changeLength(pinLength === 4 ? 6 : 4)}>{pinLength === 4 ? 'Use 6 digits' : 'Use 4 digits'}</button></p>
          {!creating && <button type="button" className="link-button" disabled={busy} onClick={() => { setSettingPin(false); setPin(''); }}>Cancel PIN change</button>}
        </>}
      </fieldset>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="actions"><button type="button" className="quiet-button" disabled={busy} onClick={onClose}>Cancel</button><BusyButton className="primary" busy={busy}>{creating ? 'Create profile' : 'Save changes'}</BusyButton></div>
    </form>
    {profile && <>
      <AccessPolicyForm profile={profile} libraries={libraries} onNotice={onNotice} />
      <section className="danger-zone" aria-labelledby={`delete-${profile.id}`}><div><h3 id={`delete-${profile.id}`}>Delete this profile</h3><p>Removes {profile.name}’s viewing history and My List. Media files are untouched.</p></div><HoldButton className="danger" disabled={busy} onConfirm={() => void remove()} confirmedLabel="Deleting…">Delete profile</HoldButton></section>
    </>}
  </AppModal>;
}

const ratingOptions: Record<'GB' | 'US', string[]> = {
  GB: ['U', 'PG', '12', '12A', '15', '18', 'R18'],
  US: ['G', 'TV-Y', 'TV-Y7', 'TV-G', 'PG', 'TV-PG', 'PG-13', 'TV-14', 'R', 'TV-MA', 'NC-17'],
};

function AccessPolicyForm({ profile, libraries, onNotice }: { profile: { id: string; name: string }; libraries: Library[]; onNotice: (value: string) => void }) {
  const [policy, setPolicy] = useState<ProfileAccessPolicy>();
  const [selectedLibraries, setSelectedLibraries] = useState(false);
  const [allowTags, setAllowTags] = useState('');
  const [denyTags, setDenyTags] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    void api.profileAccessPolicy(profile.id).then((loaded) => {
      if (!active) return;
      setPolicy(loaded);
      setSelectedLibraries(loaded.library_ids.length > 0);
      setAllowTags(loaded.allow_tags.join(', '));
      setDenyTags(loaded.deny_tags.join(', '));
    }).catch((cause) => { if (active) setError(cause instanceof ApiError ? cause.message : 'Content access is unavailable.'); });
    return () => { active = false; };
  }, [profile.id]);
  const tags = (value: string) => [...new Set(value.split(',').map((tag) => tag.trim().toLowerCase()).filter(Boolean))];
  const save = useAsyncAction({
    action: async () => {
      if (!policy) return;
      setError('');
      const updated = await api.saveProfileAccessPolicy(profile.id, {
        library_ids: selectedLibraries ? policy.library_ids : [],
        rating_region: policy.rating_region,
        max_rating: policy.rating_region ? policy.max_rating : '',
        unrated_policy: policy.unrated_policy,
        allow_tags: tags(allowTags),
        deny_tags: tags(denyTags),
      });
      setPolicy(updated);
      onNotice('Content access updated. Devices using this profile must choose it again.');
    },
    onError: (cause) => setError(cause instanceof ApiError ? cause.message : 'Unable to save content access.'),
  });
  if (!policy) return <section className="access-policy"><h3>Content access</h3>{error ? <p role="alert" className="form-error">{error}</p> : <p className="muted" role="status">Loading content access…</p>}</section>;
  const toggleLibrary = (id: string, checked: boolean) => setPolicy((current) => current ? { ...current, library_ids: checked ? [...current.library_ids, id] : current.library_ids.filter((value) => value !== id) } : current);
  const invalid = selectedLibraries && policy.library_ids.length === 0;
  return <fieldset className="access-policy" aria-label={`Content access for ${profile.name}`} disabled={save.pending}>
    <legend>Content access</legend>
    <p className="muted">Library, rating, and tag rules apply to browsing, artwork, playback, and local screens.</p>
    <div className="choice-list">
      <label className="choice"><input type="radio" name={`libraries-${profile.id}`} checked={!selectedLibraries} onChange={() => setSelectedLibraries(false)} /> All libraries, including new ones</label>
      <label className="choice"><input type="radio" name={`libraries-${profile.id}`} checked={selectedLibraries} onChange={() => setSelectedLibraries(true)} /> Only selected libraries</label>
      {selectedLibraries && <div className="choice-list is-nested">{libraries.map((library) => <label key={library.id} className="choice"><input type="checkbox" checked={policy.library_ids.includes(library.id)} onChange={(event) => toggleLibrary(library.id, event.target.checked)} />{library.name}</label>)}</div>}
    </div>
    {invalid && <p role="alert" className="form-error">Choose at least one library.</p>}
    <div className="field-row">
      <label>Rating region<select value={policy.rating_region} onChange={(event) => { const region = event.target.value as ProfileAccessPolicy['rating_region']; setPolicy({ ...policy, rating_region: region, max_rating: region ? ratingOptions[region][0] : '' }); }}><option value="">No rating limit</option><option value="GB">United Kingdom</option><option value="US">United States</option></select></label>
      {policy.rating_region && <><label>Maximum rating<select value={policy.max_rating} onChange={(event) => setPolicy({ ...policy, max_rating: event.target.value })}>{ratingOptions[policy.rating_region].map((rating) => <option key={rating}>{rating}</option>)}</select></label><label>Unrated titles<select value={policy.unrated_policy} onChange={(event) => setPolicy({ ...policy, unrated_policy: event.target.value as ProfileAccessPolicy['unrated_policy'] })}><option value="allow">Allow</option><option value="deny">Block</option></select></label></>}
    </div>
    {policy.rating_region && <p className="field-hint">Unknown ratings are blocked while a rating limit is active.</p>}
    <div className="field-row">
      <label>Allowed tags<input value={allowTags} placeholder="family, animation" onChange={(event) => setAllowTags(event.target.value)} /></label>
      <label>Blocked tags<input value={denyTags} placeholder="scary" onChange={(event) => setDenyTags(event.target.value)} /></label>
    </div>
    <p className="field-hint">Blocked tags always win. Allowed tags never override a library or rating limit.</p>
    {error && <p role="alert" className="form-error">{error}</p>}
    <div className="actions"><BusyButton type="button" busy={save.pending} disabled={invalid} onClick={() => save.run()}>Save content access</BusyButton></div>
  </fieldset>;
}

function SessionManager() {
  const [sessions, setSessions] = useState<ActiveSession[]>([]);
  const [notice, setNotice] = useState('');
  const load = () => api.activeSessions().then((value) => setSessions(value.sessions ?? [])).catch(() => setNotice('Sessions are unavailable.'));
  useEffect(() => { void load(); }, []);
  return <section className="sessions" aria-labelledby="sessions-title">
    <div className="panel-heading"><div><h3 id="sessions-title">Signed-in devices</h3><p>Revoking a session signs that device out the next time it talks to Flixr.</p></div></div>
    {notice && <p role="status" className="muted">{notice}</p>}
    {sessions.length ? <ul className="session-list">{sessions.map((session) => <li key={session.id}><div><strong>{session.subject === 'owner' ? 'Owner' : session.subject}</strong><span className="muted">Expires {new Date(session.expires_at * 1000).toLocaleString()}</span></div><button type="button" className="quiet-button" onClick={() => void api.revokeSession(session.id).then(load).catch(() => setNotice('Unable to revoke session.'))}>Revoke</button></li>)}</ul> : <p className="muted">No active sessions.</p>}
  </section>;
}
