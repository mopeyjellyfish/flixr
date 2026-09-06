export type Readiness = { ffprobe: boolean; ffmpeg: boolean };
export type SetupStatus = { claimed: boolean; readiness: Readiness };
export type Profile = { id: string; name: string; protected: boolean };
export type CatalogItem = {
  id: string;
  title: string;
  kind: 'film' | 'series' | 'episode' | string;
  season?: number;
  episode?: number;
  series_id?: string;
  local_only: boolean;
  container?: string;
  video_codec?: string;
  audio_codec?: string;
  duration_ms?: number;
  year?: number;
  synopsis?: string;
  poster?: string;
  backdrop?: string;
};
export type Episode = CatalogItem & { kind: 'episode'; season: number; episode: number };
export type Season = { id: string; number: number; episodes: Episode[] };
export type SeriesDetail = Omit<CatalogItem, 'kind'> & { kind: 'series'; seasons: Season[] };
export type FilmDetail = CatalogItem & { kind: 'film' };
export type OwnerRoots = { films: string; tv: string };
export type TMDBSettings = { configured: boolean };
export type CatalogPage = { items: CatalogItem[]; total?: number; next?: number | null };
export type Scan = { id?: string; status: 'running' | 'success' | 'partial' | 'failed' | string; scanned: number; failed: number; unmatched: number; message?: string; finished_at?: number };
export type ApiErrorCode =
  | 'invalid_token' | 'invalid_credentials' | 'invalid_pin' | 'pin_rate_limited'
  | 'credential_busy' | 'login_rate_limited'
  | 'ffprobe_unavailable' | 'owner_required' | 'profile_required' | 'request_failed'
  | 'invalid_request' | 'already_claimed' | 'profile_not_found' | 'invalid_pagination'
  | 'catalog_not_found' | 'catalog_artwork_not_found' | 'catalog_query_failed'
  | 'bad_origin' | 'logout_failed' | 'profile_failed' | 'progress_failed'
  | 'invalid_roots' | 'scan_active' | 'scan_failed' | 'settings_failed';
export class ApiError extends Error {
  constructor(public readonly code: ApiErrorCode, public readonly status: number) {
    super(messageFor(code));
  }
}
export function messageFor(code: string): string {
  return ({
    invalid_token: 'That setup token is not valid.', invalid_credentials: 'The owner password is not valid.',
    invalid_pin: 'That PIN is not valid.', pin_rate_limited: 'Too many PIN attempts. Please wait and try again.',
    credential_busy: 'Flixr is busy securing credentials. Please wait and try again.',
    login_rate_limited: 'Too many sign-in attempts. Please wait and try again.',
    ffprobe_unavailable: 'ffprobe is unavailable. Install it, then recheck readiness.', owner_required: 'Owner access is required.',
    profile_required: 'Choose a profile to continue.', invalid_request: 'Check the entered information and try again.',
    already_claimed: 'This Flixr has already been claimed.', profile_not_found: 'That profile no longer exists.',
    invalid_pagination: 'The requested catalog page is not available.', catalog_not_found: 'That title is no longer available.',
    catalog_artwork_not_found: 'Artwork is not available for this title.', catalog_query_failed: 'The catalog could not be read.',
    bad_origin: 'This request was blocked because it came from another site.', logout_failed: 'Flixr could not sign out.',
    profile_failed: 'Flixr could not create that profile.', progress_failed: 'Flixr could not save playback progress.',
    invalid_roots: 'Those library roots are not valid.', scan_active: 'A scan is already in progress.',
    scan_failed: 'Flixr could not start a scan.', settings_failed: 'Flixr could not save those settings.',
  } as Record<string, string>)[code] ?? 'Flixr could not complete that request. Please try again.';
}
