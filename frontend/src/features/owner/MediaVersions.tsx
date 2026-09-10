import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type MediaVersionCandidate, type MediaVersionGroup } from '../../core/api';
import type { ConfirmOptions } from '../../modules/ui/ConfirmDialog';

type Confirm = (options: ConfirmOptions) => Promise<boolean>;

function candidateLabel(candidate: MediaVersionCandidate) {
  const media = [candidate.height ? `${candidate.height >= 2160 ? '4K' : `${candidate.height}p`}` : undefined, candidate.hdr, candidate.video_codec?.toUpperCase(), candidate.container?.toUpperCase()].filter(Boolean).join(' · ');
  return `${candidate.title}${media ? ` — ${media}` : ''}`;
}

export function MediaVersions({ confirm }: { confirm: Confirm }) {
  const [groups, setGroups] = useState<MediaVersionGroup[]>([]);
  const [candidates, setCandidates] = useState<MediaVersionCandidate[]>([]);
  const [canonicalID, setCanonicalID] = useState('');
  const [memberIDs, setMemberIDs] = useState<string[]>([]);
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    api.mediaVersionGroups().then((result) => {
      if (!active) return;
      setGroups(result.groups ?? []);
      setCandidates(result.candidates ?? []);
    }).catch((error) => { if (active) setNotice(error instanceof ApiError ? error.message : 'Media versions are unavailable.'); });
    return () => { active = false; };
  }, []);

  const canonical = useMemo(() => candidates.find((candidate) => candidate.id === canonicalID), [candidates, canonicalID]);
  const compatibleCandidates = canonical ? candidates.filter((candidate) => candidate.id !== canonical.id && candidate.kind === canonical.kind) : [];
  const toggleMember = (id: string) => setMemberIDs((current) => current.includes(id) ? current.filter((memberID) => memberID !== id) : [...current, id]);
  const create = async (event: FormEvent) => {
    event.preventDefault();
    if (!canonical || memberIDs.length === 0 || busy) return;
    if (!await confirm({
      title: `Group ${memberIDs.length + 1} encodings of ${canonical.title}?`,
      message: 'The canonical title history stays active while grouped. Member history returns unchanged if you ungroup it.',
      confirmLabel: 'Group encodings',
    })) return;
    setBusy(true);
    setNotice('');
    try {
      const group = await api.createMediaVersionGroup(canonical.kind, canonical.id, memberIDs);
      const grouped = new Set([canonical.id, ...memberIDs]);
      setGroups((current) => [...current.filter((entry) => entry.id !== group.id), group]);
      setCandidates((current) => current.filter((candidate) => !grouped.has(candidate.id)));
      setCanonicalID('');
      setMemberIDs([]);
      setNotice('Encoding copies grouped.');
    } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Flixr could not group these encodings.'); }
    finally { setBusy(false); }
  };
  const ungroup = async (group: MediaVersionGroup, memberID: string) => {
    const member = group.members.find((version) => version.id === memberID);
    if (!member || busy) return;
    if (!await confirm({
      title: `Ungroup ${member.label}?`,
      message: 'This source becomes a separate title again. Its dormant viewing history returns unchanged; the canonical title history is not copied.',
      confirmLabel: 'Ungroup encoding',
    })) return;
    setBusy(true);
    setNotice('');
    try {
      const result = await api.ungroupMediaVersion(group.kind, group.id, memberID);
      setGroups((current) => current.map((entry) => entry.id === group.id ? result.group : entry));
      setCandidates((current) => [...current, { ...member, title: group.title, kind: group.kind }]);
      setNotice(`${member.label} is a separate title again.`);
    } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Flixr could not ungroup this encoding.'); }
    finally { setBusy(false); }
  };
  const saveEdition = async (group: MediaVersionGroup, editionLabel: string) => {
    if (busy || editionLabel === (group.edition_label ?? '')) return;
    setBusy(true);
    setNotice('');
    try {
      const updated = await api.updateMediaVersionEdition(group.kind, group.id, editionLabel.trim());
      setGroups((current) => current.map((entry) => entry.id === group.id ? updated : entry));
      setNotice(editionLabel.trim() ? 'Edition label saved.' : 'Edition label removed.');
    } catch (error) { setNotice(error instanceof ApiError ? error.message : 'Flixr could not save this edition label.'); }
    finally { setBusy(false); }
  };

  return <section className="owner-panel media-versions" aria-labelledby="media-versions-title" aria-busy={busy || undefined}>
    <div className="panel-heading"><div><h3 id="media-versions-title">Media versions</h3><p>Group resolution and encoding copies under one title. Keep different cuts as separate editions so their viewing history stays separate.</p></div></div>
    {notice && <p role="status" className="field-hint">{notice}</p>}
    {groups.length > 0 && <div className="stack">{groups.map((group) => <article key={`${group.kind}:${group.id}`} className="owner-card version-group">
      <div className="card-head"><div className="card-title"><strong>{group.title}</strong><span className="pill">{group.members.length} encodings</span></div></div>
      <label>Edition label<input key={group.edition_label} defaultValue={group.edition_label ?? ''} placeholder="For example, Director’s cut" onBlur={(event) => void saveEdition(group, event.currentTarget.value)} /></label>
      <ul className="version-members">{group.members.map((member, index) => <li key={member.id}><span><strong>{member.label}</strong>{index === 0 && <span className="muted">Canonical</span>}</span>{index > 0 && <button type="button" className="quiet-button" disabled={busy} onClick={() => void ungroup(group, member.id)}>Ungroup {member.label}</button>}</li>)}</ul>
    </article>)}</div>}
    <form className="owner-card inline-form" onSubmit={(event) => void create(event)}>
      <h4>Group encoding copies</h4>
      <label>Title to keep<select value={canonicalID} onChange={(event) => { setCanonicalID(event.target.value); setMemberIDs([]); }}><option value="">Choose a title</option>{candidates.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidateLabel(candidate)}</option>)}</select></label>
      {canonical && <fieldset className="choice-list"><legend>Copies to group</legend>{compatibleCandidates.length ? compatibleCandidates.map((candidate) => <label key={candidate.id} className="choice"><input type="checkbox" checked={memberIDs.includes(candidate.id)} onChange={() => toggleMember(candidate.id)} /> {candidateLabel(candidate)}</label>) : <p className="muted">No compatible ungrouped titles are available.</p>}</fieldset>}
      <div className="actions"><button className="primary" disabled={!canonical || memberIDs.length === 0 || busy}>Group selected encodings</button></div>
    </form>
  </section>;
}
