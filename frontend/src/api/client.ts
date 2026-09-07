import { ApiError, type ActiveSession, type CatalogItem, type CatalogPage, type MetadataCandidate, type MetadataField, type MetadataTarget, type OwnerRoots, type PlaybackCapabilities, type PlaybackPlan, type PlaybackSettings, type PlaybackStatus, type Profile, type Scan, type SeriesDetail, type SettingsInventory, type SetupStatus, type TMDBSettings, type ViewerModel, type ViewerPreference, type HistoryPage, type Rating } from '../core/api';
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
  if (!response.ok) throw new ApiError((body as { error?: { code?: ApiError['code'] } }).error?.code ?? 'request_failed', response.status, response.headers.get('X-Flixr-Error-ID') ?? undefined);
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
  activeSessions: () => request<{ sessions: ActiveSession[] }>('/owner/sessions'),
  revokeSession: (id: string) => request<{ revoked: boolean }>(`/owner/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  selectProfile: (id: string, pin = '') => request(`/profiles/${id}/select`, { method: 'POST', body: JSON.stringify({ pin }) }),
  ownerRoots: () => request<OwnerRoots>('/owner/roots'),
  roots: (films: string, tv: string) => request<{ saved: boolean }>('/owner/roots', { method: 'POST', body: JSON.stringify({ films, tv }) }),
  tmdbSettings: () => request<TMDBSettings>('/owner/settings/tmdb'),
  saveTMDBToken: (token: string) => request<TMDBSettings>('/owner/settings/tmdb', { method: 'PUT', body: JSON.stringify({ token }) }),
  removeTMDBToken: () => request<TMDBSettings>('/owner/settings/tmdb', { method: 'PUT', body: JSON.stringify({ token: '' }) }),
  settingsInventory: () => request<SettingsInventory>('/owner/settings'),
  settingsExport: () => request<{ version: number; settings: Record<string, string> }>('/owner/settings/export'),
  settingsImportPreview: (data: { version: number; settings: Record<string, string> }) => request<{ changes: Record<string, { from: string; to: string }>; requires_review: boolean; restart_required?: boolean }>('/owner/settings/import/preview', { method: 'POST', body: JSON.stringify(data) }),
  settingsImport: (data: { version: number; settings: Record<string, string> }) => request<{ imported: boolean; restart_required?: boolean }>('/owner/settings/import', { method: 'POST', body: JSON.stringify(data) }),
  scan: () => request<{ scan: Scan }>('/owner/scan', { method: 'POST' }),
  scanStatus: () => request<{ scan: Scan }>('/owner/scan/status'),
  unmatchedMetadata: () => request<{ items: MetadataTarget[] }>('/owner/metadata/unmatched'),
  metadataCandidates: (kind: string, id: string) => request<{ candidates: MetadataCandidate[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/candidates`),
  matchMetadata: (kind: string, id: string, providerID: string) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/match`, { method: 'PUT', body: JSON.stringify({ provider_id: providerID, language: 'en-US', region: 'US' }) }),
  unmatchMetadata: (kind: string, id: string) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/match`, { method: 'DELETE' }),
  metadataFields: (kind: string, id: string) => request<{ fields: MetadataField[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/fields`),
  previewMetadata: (kind: string, id: string, fields: MetadataField[]) => request<{ fields: MetadataField[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/preview`, { method: 'POST', body: JSON.stringify({ fields }) }),
  editMetadata: (kind: string, id: string, fields: MetadataField[]) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/fields`, { method: 'PUT', body: JSON.stringify({ fields }) }),
  refreshMetadataPreview: (kind: string, id: string) => request<{ fields: MetadataField[] }>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/refresh/preview`, { method: 'POST' }),
  refreshMetadata: (kind: string, id: string) => request<MetadataTarget>(`/owner/metadata/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/refresh`, { method: 'POST' }),
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
  setWatched: (kind: 'film' | 'episode' | 'season' | 'series', id: string, watched: boolean, season?: number) => request<{ watched: boolean }>(`/catalog/watched/${kind}/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ watched, season }) }),
  playbackPlan: (catalogID: string, capabilities: PlaybackCapabilities) => request<PlaybackPlan>('/playback/plans', { method: 'POST', body: JSON.stringify({ catalog_id: catalogID, capabilities }) }),
  playbackHeartbeat: (sessionID: string, positionMs: number, observation: number, ended = false) => request<{ expires_at: number }>(`/playback/sessions/${encodeURIComponent(sessionID)}/heartbeat`, { method: 'POST', body: JSON.stringify({ position_ms: positionMs, observation, ended }) }),
  playbackSeek: (sessionID: string, positionMs: number, observation: number) => request<PlaybackPlan>(`/playback/sessions/${encodeURIComponent(sessionID)}/seek`, { method: 'POST', body: JSON.stringify({ position_ms: positionMs, observation }) }),
  playbackAudio: (sessionID: string, audioStreamIndex: number, audioExternal: boolean, positionMs: number, observation: number, capabilities: PlaybackCapabilities) => request<PlaybackPlan>(`/playback/sessions/${encodeURIComponent(sessionID)}/audio`, { method: 'POST', body: JSON.stringify({ audio_stream_index: audioStreamIndex, audio_external: audioExternal, position_ms: positionMs, observation, capabilities }) }),
  playbackStop: (sessionID: string) => request<{ stopped: boolean }>(`/playback/sessions/${encodeURIComponent(sessionID)}/stop`, { method: 'POST' }),
  screens: () => request<{ screens: ScreenPresence[] }>('/screens'),
  advertiseScreen: (name: string) => request<{ screen: ScreenPresence; ticket: string }>('/screens/presence', { method: 'POST', body: JSON.stringify({ name }) }),
  authorizeScreen: (id: string) => request<{ token: string; screen_id: string; expires_at: string }>(`/screens/${encodeURIComponent(id)}/sessions`, { method: 'POST', body: '{}' }),
  ownerScreens: () => request<{ screens: ScreenPresence[] }>('/owner/screens'),
};
