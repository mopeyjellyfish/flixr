import { ApiError, type CatalogItem, type CatalogPage, type FilmDetail, type OwnerRoots, type Profile, type Scan, type SeriesDetail, type SetupStatus, type TMDBSettings } from '../core/api';

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
  if (!response.ok) throw new ApiError((body as { error?: { code?: ApiError['code'] } }).error?.code ?? 'request_failed', response.status);
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
  selectProfile: (id: string, pin = '') => request(`/profiles/${id}/select`, { method: 'POST', body: JSON.stringify({ pin }) }),
  ownerRoots: () => request<OwnerRoots>('/owner/roots'),
  roots: (films: string, tv: string) => request<{ saved: boolean }>('/owner/roots', { method: 'POST', body: JSON.stringify({ films, tv }) }),
  tmdbSettings: () => request<TMDBSettings>('/owner/settings/tmdb'),
  saveTMDBToken: (token: string) => request<TMDBSettings>('/owner/settings/tmdb', { method: 'PUT', body: JSON.stringify({ token }) }),
  removeTMDBToken: () => request<TMDBSettings>('/owner/settings/tmdb', { method: 'PUT', body: JSON.stringify({ token: '' }) }),
  scan: () => request<{ scan: Scan }>('/owner/scan', { method: 'POST' }),
  scanStatus: () => request<{ scan: Scan }>('/owner/scan/status'),
  recheck: () => request<SetupStatus>('/owner/readiness/recheck', { method: 'POST' }),
  home: (offset = 0) => request<CatalogPage>(`/catalog/home?offset=${offset}&limit=48`),
  search: (query: string, offset = 0) => request<CatalogPage>(`/catalog/search?q=${encodeURIComponent(query)}&offset=${offset}&limit=48`),
  item: (id: string) => request<CatalogItem>(`/catalog/items/${id}`),
  film: (id: string) => request<FilmDetail>(`/catalog/films/${id}`),
  series: (id: string) => request<SeriesDetail>(`/catalog/series/${id}`),
};
