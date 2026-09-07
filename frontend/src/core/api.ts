export type Readiness = { ffprobe: boolean; ffmpeg: boolean };
export type SetupStatus = { claimed: boolean; readiness: Readiness; demo?: boolean; demo_source?: string };
export type Profile = { id: string; name: string; protected: boolean; avatar?: string };
export type CatalogItem = {
  genres?: string[];
  id: string;
  title: string;
  kind: 'film' | 'series' | 'episode' | string;
  season?: number;
  episode?: number;
  series_id?: string;
  local_only: boolean;
  container?: string;
  video_codec?: string;
  video_profile?: string;
	width?: number;
	height?: number;
	bitrate?: number;
	frame_rate_milli?: number;
	bit_depth?: number;
	hdr?: string;
	audio?: Array<{ codec: string; channels?: number }>;
  audio_codec?: string;
  duration_ms?: number;
  year?: number;
  synopsis?: string;
  poster?: string;
  backdrop?: string;
  playable?: boolean;
  demo?: boolean;
};
export type ViewerItem = CatalogItem & { listed: boolean };
export type ViewerPreference = { view: 'rows' | 'grid'; sort: 'title' | 'year' | 'added' | 'watched' };
export type ViewerSection = { name: string; items: ViewerItem[] };
export type ViewerModel = { preference: ViewerPreference; sections?: ViewerSection[]; items?: ViewerItem[] };
export type Episode = CatalogItem & { kind: 'episode'; season: number; episode: number };
export type Season = { id: string; number: number; episodes: Episode[] };
export type SeriesDetail = Omit<CatalogItem, 'kind'> & { kind: 'series'; seasons: Season[] };
export type OwnerRoots = { films: string; tv: string };
export type TMDBSettings = { configured: boolean };
export type MetadataCandidate = { provider: string; id: string; title: string; year?: number; language?: string; region?: string; confidence: number };
export type MetadataTarget = CatalogItem & { provider_id?: string; metadata_provider?: string; metadata_language?: string; metadata_region?: string; match_confidence?: number; owner_matched?: boolean };
export type CatalogPage = { items: CatalogItem[]; total?: number; next?: number | null };
export type PlaybackSettings = { segment_dir: string; generation_bytes: number; global_bytes: number; max_generations: number };
export type PlaybackGeneration = { id: string; catalog_id: string; kind: 'remux' | 'transcode'; start_ms: number; leases: number; bytes: number; running: boolean; started_at: number };
export type PlaybackStatus = { settings: PlaybackSettings; generations: PlaybackGeneration[] };
export type EffectiveSetting = { key: string; category: string; scope: string; default: string; environment: string; file_secret: string; persistence: string; restart: string; valid: string; secret: boolean; advanced: boolean; value: string; source: 'default' | 'saved' | 'environment'; mutable: boolean };
export type SettingsInventory = { settings: EffectiveSetting[] };
export type Scan = { id?: string; status: 'running' | 'success' | 'partial' | 'failed' | string; scanned: number; failed: number; unmatched: number; message?: string; finished_at?: number };
export type ApiErrorCode =
  | 'invalid_token' | 'invalid_credentials' | 'invalid_pin' | 'pin_rate_limited'
  | 'credential_busy' | 'login_rate_limited'
  | 'ffprobe_unavailable' | 'owner_required' | 'profile_required' | 'request_failed'
  | 'invalid_request' | 'already_claimed' | 'profile_not_found' | 'invalid_pagination'
  | 'playback_capability_unknown'
  | 'catalog_not_found' | 'catalog_artwork_not_found' | 'catalog_query_failed'
  | 'bad_origin' | 'logout_failed' | 'profile_failed' | 'progress_failed'
  | 'invalid_roots' | 'scan_active' | 'scan_failed' | 'settings_failed' | 'metadata_unavailable' | 'metadata_busy'
  | 'playback_unsupported' | 'ffmpeg_unavailable' | 'playback_failed' | 'playback_capacity' | 'playback_preparing'
  | 'playback_session_invalid' | 'playback_not_direct' | 'playback_not_hls' | 'playback_asset_not_found' | 'playback_not_playable'
  | 'catalog_list_failed' | 'catalog_preferences_failed'
  | 'invalid_playback_settings' | 'playback_active' | 'playback_settings_failed' | 'environment_locked' | 'import_requires_review';
export type PlaybackCapabilities = { containers: string[]; video_codecs: string[]; video_profiles?: string[]; audio_codecs: string[]; supports_fmp4_hls: boolean; supports_direct: boolean; max_width?: number; max_height?: number; max_frame_rate_milli?: number; max_bit_depth?: number; max_audio_channels?: number; hdr?: string[] };
export type PlaybackPlan = {
  plan: { kind: 'direct' | 'remux' | 'transcode'; description?: string };
  session_id: string;
  media_url: string;
  heartbeat_url: string;
  seek_url: string;
  stop_url: string;
  resume_ms: number;
  stream_offset_ms: number;
  expires_at: number;
};
export class ApiError extends Error {
  constructor(public readonly code: ApiErrorCode, public readonly status: number, public readonly errorID?: string) {
    super(`${messageFor(code)}${errorID ? ` Support ID: ${errorID}.` : ''}`);
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
    metadata_unavailable: 'Metadata is unavailable. Your local title is unchanged; retry when the provider is available.',
    metadata_busy: 'Wait for the current scan to finish, then retry metadata repair.',
    playback_unsupported: 'This title is not compatible with this browser.',
	playback_capability_unknown: 'This browser reported an unsupported playback capability. Update the browser or use a supported device.',
    ffmpeg_unavailable: 'FFmpeg is unavailable. Install it, then recheck readiness.',
    playback_capacity: 'Flixr is at its playback limit. Try again after another stream stops.',
    playback_preparing: 'This local stream is already preparing. Try again in a moment.',
    playback_failed: 'Flixr could not prepare this title for playback.',
    invalid_playback_settings: 'Playback limits are invalid. Check the directory and byte limits.',
    playback_active: 'Stop active compatibility streams before changing playback limits.',
    playback_settings_failed: 'Flixr could not save playback settings.',
    environment_locked: 'This value is managed by the server environment and cannot be changed here.',
    import_requires_review: 'Import one supported setting scope at a time. Network and access settings require review.',
    playback_session_invalid: 'This playback session expired. Start the title again.',
  } as Record<string, string>)[code] ?? 'Flixr could not complete that request. Please try again.';
}
