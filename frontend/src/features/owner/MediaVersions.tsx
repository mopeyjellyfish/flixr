import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { api } from '../../api/client';
import { ApiError, type MediaVersionCandidate, type MediaVersionGroup } from '../../core/api';
import type { ConfirmOptions } from '../../modules/ui/ConfirmDialog';

type Confirm = (options: ConfirmOptions) => Promise<boolean>;
const pageSize = 50;

function candidateLabel(candidate: MediaVersionCandidate) {
  const media = [candidate.height ? `${candidate.height >= 2160 ? '4K' : `${candidate.height}p`}` : undefined, candidate.hdr, candidate.video_codec?.toUpperCase(), candidate.container?.toUpperCase()].filter(Boolean).join(' · ');
  return `${candidate.title}${media ? ` — ${media}` : ''}`;
}

function groupLabel(group: MediaVersionGroup) {
  return `${group.title}${group.edition_label ? ` — ${group.edition_label}` : ''} (${group.members.length} ${group.members.length === 1 ? 'encoding' : 'encodings'})`;
}

export function MediaVersions({ confirm, onOwnerRequired }: { confirm: Confirm; onOwnerRequired: () => void }) {
  const [groups, setGroups] = useState<MediaVersionGroup[]>([]);
  const [candidates, setCandidates] = useState<MediaVersionCandidate[]>([]);
  const [selectedGroupID, setSelectedGroupID] = useState('');
  const [canonicalID, setCanonicalID] = useState('');
  const [memberIDs, setMemberIDs] = useState<string[]>([]);
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [total, setTotal] = useState(0);
  const [nextOffset, setNextOffset] = useState<number>();
  const [notice, setNotice] = useState('');
  const [noticeError, setNoticeError] = useState(false);
  const [busy, setBusy] = useState(false);
  const request = useRef(0);
  const loadedOffsets = useRef([0]);
  const fail = useCallback((error: unknown, fallback: string) => {
    if (error instanceof ApiError && error.code === 'owner_required') { onOwnerRequired(); return; }
    setNotice(error instanceof ApiError ? error.message : fallback);
    setNoticeError(true);
  }, [onOwnerRequired]);

  const load = useCallback(async (offset = 0, append = false): Promise<boolean> => {
    const requestID = ++request.current;
    setBusy(true);
    try {
      const result = await api.mediaVersionGroups(query, offset, pageSize);
      if (requestID !== request.current) return false;
      setGroups((current) => append ? [...current, ...(result.groups ?? [])] : result.groups ?? []);
      setCandidates((current) => append ? [...current, ...(result.candidates ?? [])] : result.candidates ?? []);
      loadedOffsets.current = append ? [...loadedOffsets.current, offset] : [0];
      setTotal(result.total ?? result.groups?.length ?? 0);
      setNextOffset(result.next_offset);
      setNotice('');
      setNoticeError(false);
      return true;
    } catch (error) {
      if (requestID === request.current) fail(error, 'Media versions are unavailable.');
      return false;
    } finally {
      if (requestID === request.current) setBusy(false);
    }
  }, [fail, query]);

  useEffect(() => { void load(); }, [load]);
  const selectedGroup = groups.find((group) => group.id === selectedGroupID) ?? groups[0];
  const canonical = useMemo(() => candidates.find((candidate) => candidate.id === canonicalID), [candidates, canonicalID]);
  const compatibleCandidates = canonical ? candidates.filter((candidate) => candidate.id !== canonical.id && candidate.kind === canonical.kind) : [];
  const toggleMember = (id: string) => setMemberIDs((current) => current.includes(id) ? current.filter((memberID) => memberID !== id) : [...current, id]);
  const refresh = async (reselectID: string): Promise<boolean> => {
    const requestID = ++request.current;
    const minimumPages = loadedOffsets.current.length;
    const refreshedGroups: MediaVersionGroup[] = [];
    const refreshedCandidates: MediaVersionCandidate[] = [];
    const offsets: number[] = [];
    let offset = 0;
    let result;
    setBusy(true);
    try {
      do {
        offsets.push(offset);
        result = await api.mediaVersionGroups(query, offset, pageSize);
        if (requestID !== request.current) return false;
        refreshedGroups.push(...(result.groups ?? []));
        refreshedCandidates.push(...(result.candidates ?? []));
        if (result.next_offset === undefined) break;
        offset = result.next_offset;
      } while (offsets.length < minimumPages || !refreshedGroups.some((group) => group.id === reselectID));
      setGroups(refreshedGroups);
      setCandidates(refreshedCandidates);
      loadedOffsets.current = offsets;
      setTotal(result.total ?? refreshedGroups.length);
      setNextOffset(result.next_offset);
      setSelectedGroupID(refreshedGroups.some((group) => group.id === reselectID) ? reselectID : '');
      setNotice('');
      setNoticeError(false);
      return true;
    } catch (error) {
      if (requestID === request.current) {
        if (error instanceof ApiError && error.code === 'owner_required') onOwnerRequired();
        else {
          setNotice('The change was saved, but media versions could not be refreshed. Try again.');
          setNoticeError(true);
        }
      }
      return false;
    } finally {
      if (requestID === request.current) setBusy(false);
    }
  };
  const search = (event: FormEvent) => {
    event.preventDefault();
    setSelectedGroupID('');
    setCanonicalID('');
    setMemberIDs([]);
    setQuery(queryDraft.trim());
  };
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
    setNoticeError(false);
    try {
      const group = await api.createMediaVersionGroup(canonical.kind, canonical.id, memberIDs);
      setSelectedGroupID(group.id);
      setCanonicalID('');
      setMemberIDs([]);
      if (await refresh(group.id)) setNotice('Encoding copies grouped.');
    } catch (error) { fail(error, 'Flixr could not group these encodings.'); }
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
    setNoticeError(false);
    try {
      await api.ungroupMediaVersion(group.kind, group.id, memberID);
      if (await refresh(group.id)) setNotice(`${member.label} is a separate title again.`);
    } catch (error) { fail(error, 'Flixr could not ungroup this encoding.'); }
    finally { setBusy(false); }
  };
  const saveEdition = async (group: MediaVersionGroup, editionLabel: string) => {
    if (busy || editionLabel.trim() === (group.edition_label ?? '')) return;
    setBusy(true);
    setNotice('');
    setNoticeError(false);
    try {
      await api.updateMediaVersionEdition(group.kind, group.id, editionLabel.trim());
      if (await refresh(group.id)) setNotice(editionLabel.trim() ? 'Edition label saved.' : 'Edition label removed.');
    } catch (error) { fail(error, 'Flixr could not save this edition label.'); }
    finally { setBusy(false); }
  };

  return <section className="owner-panel media-versions" aria-labelledby="media-versions-title" aria-busy={busy || undefined}>
    <div className="panel-heading"><div><h3 id="media-versions-title">Media versions</h3><p>Group resolution and encoding copies under one title. Keep different cuts as separate editions so their viewing history stays separate.</p></div></div>
    <form className="inline-form version-search" role="search" onSubmit={search}><label>Find a title<input type="search" value={queryDraft} onChange={(event) => setQueryDraft(event.target.value)} /></label><button type="submit" disabled={busy}>Search</button></form>
    {notice && <p role={noticeError ? 'alert' : 'status'} className="field-hint">{notice}</p>}
    {groups.length > 0 ? <>
      <label>Title to manage<select value={selectedGroup?.id ?? ''} onChange={(event) => setSelectedGroupID(event.target.value)}>{groups.map((group) => <option key={`${group.kind}:${group.id}`} value={group.id}>{groupLabel(group)}</option>)}</select></label>
      {selectedGroup && <article className="owner-card version-group">
        <div className="card-head"><div className="card-title"><strong>{selectedGroup.title}</strong><span className="pill">{selectedGroup.members.length} {selectedGroup.members.length === 1 ? 'encoding' : 'encodings'}</span></div></div>
        <label>Edition label<input key={selectedGroup.edition_label} defaultValue={selectedGroup.edition_label ?? ''} placeholder="For example, Director’s cut" onBlur={(event) => void saveEdition(selectedGroup, event.currentTarget.value)} /></label>
        <ul className="version-members">{selectedGroup.members.map((member, index) => <li key={member.id}><span><strong>{member.label}</strong>{index === 0 && <span className="muted">Canonical</span>}</span>{index > 0 && <button type="button" className="quiet-button" disabled={busy} onClick={() => void ungroup(selectedGroup, member.id)}>Ungroup {member.label}</button>}</li>)}</ul>
      </article>}
      <div className="version-results"><span>Showing {groups.length} of {total} titles</span>{nextOffset !== undefined && <button type="button" disabled={busy} onClick={() => void load(nextOffset, true)}>Load more titles</button>}</div>
    </> : !busy && <p className="muted">No titles match this search.</p>}
    <form className="owner-card inline-form" onSubmit={(event) => void create(event)}>
      <h4>Group encoding copies</h4>
      <label>Title to keep<select value={canonicalID} onChange={(event) => { setCanonicalID(event.target.value); setMemberIDs([]); }}><option value="">Choose a title from these results</option>{candidates.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidateLabel(candidate)}</option>)}</select></label>
      {canonical && <fieldset className="choice-list"><legend>Copies to group</legend>{compatibleCandidates.length ? compatibleCandidates.map((candidate) => <label key={candidate.id} className="choice"><input type="checkbox" checked={memberIDs.includes(candidate.id)} onChange={() => toggleMember(candidate.id)} /> {candidateLabel(candidate)}</label>) : <p className="muted">No compatible ungrouped titles are available in these results.</p>}</fieldset>}
      <div className="actions"><button className="primary" disabled={!canonical || memberIDs.length === 0 || busy}>Group selected encodings</button></div>
    </form>
  </section>;
}
