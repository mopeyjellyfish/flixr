import { ApiError, type ActiveSession, type BackupJob, type BackupPolicy, type CatalogItem, type CatalogPage, type EpisodeSequence, type HistoryPage, type IdentityMerge, type IdentityRepairs, type Library, type LibraryLocation, type LocationChangePreview, type MediaVersion, type MediaVersionGroup, type MediaVersionGroups, type MetadataCandidate, type MetadataField, type MetadataTarget, type OwnerRoots, type OwnerSetup, type PlaybackCapabilities, type PlaybackPlan, type PlaybackSettings, type PlaybackStatus, type Profile, type ProfileAccessPolicy, type Rating, type Scan, type ScanJob, type ScanJobFile, type ScanPolicy, type SeriesDetail, type SettingsInventory, type SetupStatus, type SetupStep, type TMDBSettings, type VersionCapabilities, type ViewerModel, type ViewerPreference } from '../core/api';
import type { ScreenPresence } from '../core/screens';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      credentials: 'same-origin',
      ...init,
      headers: { ...(init?.body ? { 'Content-Type': 'application/json' } : {}), ...init?.headers },
    });
  } catch {
    throw new ApiError('request_failed', 0);
  }
  const body: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const detail = (body as { error?: { code?: ApiError['code']; requested_version_id?: string; alternatives?: MediaVersion[] } }).error;
    throw new ApiError(detail?.code ?? 'request_failed', response.status, response.headers.get('X-Flixr-Error-ID') ?? undefined, detail?.requested_version_id, detail?.alternatives ?? []);
  }
  return body as T;
}

export const api = {
  setupStatus: () => request<SetupStatus>('/setup/status'),
  claim: (token: string, password: string) => request('/setup/claim', { method: 'POST', body: JSON.stringify({ token, password }) }),
  ownerLogin: (password: string) => request('/owner/login', { method: 'POST', body: JSON.stringify({ password }) }),
  logout: () => request('/logout', { method: 'POST' }),
  profiles: () => request<{ profiles: Profile[] }>('/profiles'),
  createProfile: (name: string, pin: string) => request<Profile>('/profiles', { method: 'POST', body: JSON.stringify({ name, pin }) }),
  updateProfile: (id: string, data: { name?: string; pin?: string; unprotect?: boolean }) => request<Profile>(`/profiles/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  deleteProfile: (id: string) => request<{ deleted: boolean }>(`/profiles/${id}`, { method: 'DELETE' }),
  profileAccessPolicy: (id: string) => request<ProfileAccessPolicy>(`/owner/profiles/${encodeURIComponent(id)}/access-policy`),
  saveProfileAccessPolicy: (id: string, policy: Omit<ProfileAccessPolicy, 'version'>) => request<ProfileAccessPolicy>(`/owner/profiles/${encodeURIComponent(id)}/access-policy`, { method: 'PUT', body: JSON.stringify(policy) }),
  activeSessions: () => request<{ sessions: ActiveSession[] }>('/owner/sessions'),
  revokeSession: (id: string) => request<{ revoked: boolean }>(`/owner/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  selectProfile: (id: string, pin = '') => request(`/profiles/${id}/select`, { method: 'POST', body: JSON.stringify({ pin }) }),
  ownerRoots: () => request<OwnerRoots>('/owner/roots'),
  ownerSetup: (films?: string, tv?: string) => {
    const query = new URLSearchParams();
    if (films !== undefined) query.set('films', films);
    if (tv !== undefined) query.set('tv', tv);
    const suffix = query.size > 0 ? `?${query}` : '';
    return request<OwnerSetup>(`/owner/setup${suffix}`);
  },
  updateSetup: (step: SetupStep) => request<{ step: SetupStep }>('/owner/setup', { method: 'PATCH', body: JSON.stringify({ step }) }),
  backupStatus: () => request<{ policy: BackupPolicy; jobs: BackupJob[] }>('/owner/backups'),
  saveBackupPolicy: (policy: BackupPolicy) => request<{ policy: BackupPolicy }>('/owner/backups/policy', { method: 'PUT', body: JSON.stringify(policy) }),
  runBackup: () => request<{ job: BackupJob }>('/owner/backups/jobs', { method: 'POST' }),
  cancelBackup: (id: string) => request<{ job: BackupJob }>(`/owner/backups/jobs/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  roots: (films: string, tv: string) => request<{ saved: boolean }>('/owner/roots', { method: 'POST', body: JSON.stringify({ films, tv }) }),
  libraries: () => request<{ libraries: Library[] }>('/owner/libraries'),
  createLibrary: (name: string, kind: Library['kind']) => request<Library>('/owner/libraries', { method: 'POST', body: JSON.stringify({ name, kind }) }),
  renameLibrary: (id: string, name: string) => request<Library>(`/owner/libraries/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  deleteLibrary: (id: string) => request<{ deleted: boolean }>(`/owner/libraries/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  addLibraryLocation: (libraryID: string, path: string) => request<LibraryLocation>(`/owner/libraries/${encodeURIComponent(libraryID)}/locations`, { method: 'POST', body: JSON.stringify({ path }) }),
  previewLibraryLocationChange: (locationID: string, path: string) => request<LocationChangePreview>(`/owner/library-locations/${encodeURIComponent(locationID)}/change-preview`, { method: 'POST', body: JSON.stringify({ path }) }),
  confirmLibraryLocationChange: (previewID: string) => request<{ libraries: Library[] }>(`/owner/library-location-changes/${encodeURIComponent(previewID)}/confirm`, { method: 'POST' }),
  cancelLibraryLocationChange: (previewID: string) => request<{ libraries: Library[] }>(`/owner/library-location-changes/${encodeURIComponent(previewID)}`, { method: 'DELETE' }),
  tmdbSettings: () => request<TMDBSettings>('/owner/settings/tmdb'),
  saveTMDBToken: (token: string) => request<TMDBSettings>('/owner/settings/tmdb', { method: 'PUT', body: JSON.stringify({ token }) }),
  removeTMDBToken: () => request<TMDBSettings>('/owner/settings/tmdb', { method: 'PUT', body: JSON.stringify({ token: '' }) }),
  setMetadataEnabled: (enabled: boolean) => request<TMDBSettings>('/owner/settings/tmdb', { method: 'PUT', body: JSON.stringify({ enabled }) }),
  settingsInventory: () => request<SettingsInventory>('/owner/settings'),
  settingsExport: () => request<{ version: number; settings: Record<string, string> }>('/owner/settings/export'),
  settingsImportPreview: (data: { version: number; settings: Record<string, string> }) => request<{ changes: Record<string, { from: string; to: string }>; requires_review: boolean; restart_required?: boolean }>('/owner/settings/import/preview', { method: 'POST', body: JSON.stringify(data) }),
  settingsImport: (data: { version: number; settings: Record<string, string> }) => request<{ imported: boolean; restart_required?: boolean }>('/owner/settings/import', { method: 'POST', body: JSON.stringify(data) }),
  scan: () => request<{ scan: Scan; jobs?: ScanJob[] }>('/owner/scan', { method: 'POST' }),
  scanStatus: () => request<{ scan: Scan; locations: LibraryLocation[] }>('/owner/scan/status'),
  scanJobs: () => request<{ jobs: ScanJob[] }>('/owner/scan/jobs'),
  queueScan: (libraryID: string) => request<{ job: ScanJob }>('/owner/scan/jobs', { method: 'POST', body: JSON.stringify({ library_id: libraryID }) }),
  cancelScanJob: (id: string) => request<{ job: ScanJob }>(`/owner/scan/jobs/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  retryScanJob: (id: string, files: ScanJobFile[] = []) => request<{ job: ScanJob }>(`/owner/scan/jobs/${encodeURIComponent(id)}/retry`, { method: 'POST', body: JSON.stringify({ files }) }),
  scanPolicy: (libraryID: string) => request<{ policy: ScanPolicy }>(`/owner/libraries/${encodeURIComponent(libraryID)}/scan-policy`),
  saveScanPolicy: (libraryID: string, policy: ScanPolicy) => request<{ policy: ScanPolicy }>(`/owner/libraries/${encodeURIComponent(libraryID)}/scan-policy`, { method: 'PATCH', body: JSON.stringify(policy) }),
  confirmScanRemovals: (scanID: string, locationID: string) => request<{ confirmed: boolean; scan: Scan; locations: LibraryLocation[] }>('/owner/scan/removals/confirm', { method: 'POST', body: JSON.stringify({ scan_id: scanID, location_id: locationID }) }),
  unmatchedMetadata: () => request<{ items: MetadataTarget[] }>('/owner/metadata/unmatched'),
  metadataCandidates: (kind: string, id: string) => request<{ candidates: MetadataCandidate[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/candidates`),
  matchMetadata: (kind: string, id: string, providerID: string) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/match`, { method: 'PUT', body: JSON.stringify({ provider_id: providerID, language: 'en-US', region: 'US' }) }),
  unmatchMetadata: (kind: string, id: string) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/match`, { method: 'DELETE' }),
  metadataFields: (kind: string, id: string) => request<{ fields: MetadataField[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/fields`),
  previewMetadata: (kind: string, id: string, fields: MetadataField[]) => request<{ fields: MetadataField[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/preview`, { method: 'POST', body: JSON.stringify({ fields }) }),
  editMetadata: (kind: string, id: string, fields: MetadataField[]) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/fields`, { method: 'PUT', body: JSON.stringify({ fields }) }),
  refreshMetadataPreview: (kind: string, id: string) => request<{ fields: MetadataField[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/refresh/preview`, { method: 'POST' }),
  refreshMetadata: (kind: string, id: string) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/refresh`, { method: 'POST' }),
  identityRepairs: () => request<IdentityRepairs>('/owner/identity/repairs'),
  mergeIdentity: (kind: string, survivorID: string, sourceID: string) => request<IdentityMerge>('/owner/identity/merges', { method: 'POST', body: JSON.stringify({ kind, survivor_id: survivorID, source_id: sourceID }) }),
  unmergeIdentity: (mergeID: string) => request<IdentityMerge>(`/owner/identity/merges/${encodeURIComponent(mergeID)}/unmerge`, { method: 'POST' }),
  mediaVersionGroups: (query = '', offset = 0, limit = 50) => request<MediaVersionGroups>(`/owner/media-version-groups?${new URLSearchParams({ q: query, offset: String(offset), limit: String(limit) })}`),
  createMediaVersionGroup: (kind: 'film' | 'series', canonicalID: string, memberIDs: string[]) => request<MediaVersionGroup>('/owner/media-version-groups', { method: 'POST', body: JSON.stringify({ kind, canonical_id: canonicalID, member_ids: memberIDs }) }),
  updateMediaVersionEdition: (kind: 'film' | 'series', canonicalID: string, editionLabel: string) => request<MediaVersionGroup>(`/owner/media-version-groups/${kind}/${encodeURIComponent(canonicalID)}`, { method: 'PATCH', body: JSON.stringify({ edition_label: editionLabel }) }),
  ungroupMediaVersion: (kind: 'film' | 'series', canonicalID: string, memberID: string) => request<{ group: MediaVersionGroup; ungrouped_id: string }>(`/owner/media-version-groups/${kind}/${encodeURIComponent(canonicalID)}/members/${encodeURIComponent(memberID)}`, { method: 'DELETE' }),
  recheck: () => request<SetupStatus>('/owner/readiness/recheck', { method: 'POST' }),
  playbackSettings: () => request<PlaybackSettings>('/owner/settings/playback'),
  savePlaybackSettings: (settings: PlaybackSettings) => request<{ settings: PlaybackSettings; restart_required: boolean }>('/owner/settings/playback', { method: 'PUT', body: JSON.stringify(settings) }),
  playbackStatus: () => request<PlaybackStatus>('/owner/playback/status'),
  home: (offset = 0) => request<CatalogPage>(`/catalog/home?offset=${offset}&limit=48`),
  history: (before = '') => request<HistoryPage>(`/history?limit=25${before ? `&before=${encodeURIComponent(before)}` : ''}`),
  clearHistory: () => request<{ id: string; undo_until: number }>('/history/clear', { method: 'POST' }),
  undoHistoryClear: (id: string) => request<{ restored: boolean }>(`/history/clear/${encodeURIComponent(id)}/undo`, { method: 'POST' }),
  rating: (id: string) => request<{ rating: Rating | null }>(`/ratings/${encodeURIComponent(id)}`),
  setRating: (id: string, value: number) => request<{ saved: boolean }>(`/ratings/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ value }) }),
  deleteRating: (id: string) => request<{ deleted: boolean }>(`/ratings/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  search: (query: string, offset = 0) => request<CatalogPage>(`/catalog/search?q=${encodeURIComponent(query)}&offset=${offset}&limit=48`),
  item: (id: string) => request<CatalogItem>(`/catalog/items/${id}`),
  series: (id: string) => request<SeriesDetail>(`/catalog/series/${id}`),
  viewer: (media: 'all' | 'film' | 'series') => request<ViewerModel>(`/catalog/view?media=${media}`),
  saveViewerPreference: (media: 'all' | 'film' | 'series', preference: ViewerPreference) => request<ViewerPreference>(`/catalog/preferences/${media}`, { method: 'PUT', body: JSON.stringify(preference) }),
  setListed: (kind: 'film' | 'series', id: string, listed: boolean) => request<{ listed: boolean }>(`/catalog/list/${kind}/${encodeURIComponent(id)}`, { method: listed ? 'PUT' : 'DELETE' }),
  setContinueWatchingDismissed: (kind: 'film' | 'series', id: string, dismissed: boolean) => request<{ dismissed: boolean }>(`/catalog/continue-watching/${kind}/${encodeURIComponent(id)}`, { method: dismissed ? 'DELETE' : 'PUT' }),
  setWatched: (kind: 'film' | 'episode' | 'season' | 'series', id: string, watched: boolean, season?: number) => request<{ watched: boolean }>(`/catalog/watched/${kind}/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ watched, season }) }),
  playbackPlan: (catalogID: string, capabilities: PlaybackCapabilities, continueWatchingIntent: 'user' | 'recovery' | 'automatic' = 'user', quality: import('../core/api').PlaybackQuality = { mode: 'auto', max_video_bitrate: 2_500_000, max_width: 1280, max_height: 720 }, versionID?: string, versionCapabilities?: VersionCapabilities) => request<PlaybackPlan>('/playback/plans', { method: 'POST', body: JSON.stringify({ catalog_id: catalogID, continue_watching_intent: continueWatchingIntent, capabilities, quality, ...(versionID ? { version_id: versionID } : {}), ...(versionCapabilities ? { version_capabilities: versionCapabilities } : {}) }) }),
  playbackHeartbeat: (sessionID: string, positionMs: number, observation: number, ended = false) => request<{ accepted: boolean; expires_at: number }>(`/playback/sessions/${encodeURIComponent(sessionID)}/heartbeat`, { method: 'POST', body: JSON.stringify({ position_ms: positionMs, observation, ended }) }),
  playbackNext: (sessionID: string, includeSpecials = false, signal?: AbortSignal) => request<EpisodeSequence>(`/playback/sessions/${encodeURIComponent(sessionID)}/next?include_specials=${includeSpecials}`, { signal }),
  playbackSeek: (sessionID: string, positionMs: number, observation: number, signal?: AbortSignal) => request<PlaybackPlan>(`/playback/sessions/${encodeURIComponent(sessionID)}/seek`, { method: 'POST', signal, body: JSON.stringify({ position_ms: positionMs, observation }) }),
  playbackAudio: (sessionID: string, audioStreamIndex: number, audioExternal: boolean, positionMs: number, observation: number, capabilities: PlaybackCapabilities, signal?: AbortSignal) => request<PlaybackPlan>(`/playback/sessions/${encodeURIComponent(sessionID)}/audio`, { method: 'POST', signal, body: JSON.stringify({ audio_stream_index: audioStreamIndex, audio_external: audioExternal, position_ms: positionMs, observation, capabilities }) }),
  playbackQuality: (sessionID: string, positionMs: number, observation: number, capabilities: PlaybackCapabilities, quality: import('../core/api').PlaybackQuality, signal?: AbortSignal, smoothHandoff = false) => request<PlaybackPlan>(`/playback/sessions/${encodeURIComponent(sessionID)}/quality`, { method: 'POST', signal, body: JSON.stringify({ position_ms: positionMs, observation, capabilities, quality, ...(smoothHandoff ? { smooth_handoff: true } : {}) }) }),
  playbackHandoff: (url: string, attached: boolean, signal?: AbortSignal) => request<{ attached: boolean }>(url.startsWith('/api/v1') ? url.slice('/api/v1'.length) : url, { method: 'POST', signal, body: JSON.stringify({ attached }) }),
  playbackSubtitle: (sessionID: string, selection: { mode: 'off' | 'automatic' } | { mode: 'track'; subtitle_stream_index: number; subtitle_external: boolean }) => request<PlaybackPlan>(`/playback/sessions/${encodeURIComponent(sessionID)}/subtitle`, { method: 'POST', body: JSON.stringify(selection) }),
  playbackStop: (sessionID: string) => request<{ stopped: boolean }>(`/playback/sessions/${encodeURIComponent(sessionID)}/stop`, { method: 'POST' }),
  screens: () => request<{ screens: ScreenPresence[] }>('/screens'),
  advertiseScreen: (name: string) => request<{ screen: ScreenPresence; ticket: string }>('/screens/presence', { method: 'POST', body: JSON.stringify({ name }) }),
  authorizeScreen: (id: string) => request<{ token: string; screen_id: string; expires_at: string }>(`/screens/${encodeURIComponent(id)}/sessions`, { method: 'POST', body: '{}' }),
  ownerScreens: () => request<{ screens: ScreenPresence[] }>('/owner/screens'),
};
