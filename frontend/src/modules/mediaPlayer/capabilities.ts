import type { CatalogItem, PlaybackCapabilities } from '../../core/api';

const mp4Aliases = new Set(['mov', 'mp4', 'm4a', '3gp', '3g2', 'mj2']);
const transcodableVideo = new Set(['h264', 'avc', 'avc1', 'vp9', 'hevc', 'h265', 'mpeg4', 'mpeg2video']);
const transcodableAudio = new Set(['aac', 'mp4a', 'opus', 'mp3', 'mp2']);
const compatibility = {
	videoContentType: 'video/mp4; codecs="avc1.640028"',
	videoBitrate: 5_000_000,
	maxWidth: 1920,
	maxHeight: 1080,
	maxFrameRateMilli: 30_000,
	audioContentType: 'audio/mp4; codecs="mp4a.40.2"',
	audioChannels: 2,
	audioSampleRate: 48_000,
	audioBitrate: 128_000,
} as const;

type DecodingType = 'file' | 'media-source';

function compatibilityDimensions(width: number, height: number): boolean {
	return width <= compatibility.maxWidth && height <= compatibility.maxHeight ||
		width <= compatibility.maxHeight && height <= compatibility.maxWidth;
}

function h264ContentType(profile: string | undefined, level: number | undefined): string | undefined {
	const prefix = ({ Baseline: '42e0', Main: '4d40', High: '6400' } as Record<string, string>)[profile ?? ''];
	if (!prefix || !level || level <= 0 || level > 255) return undefined;
	return `video/mp4; codecs="avc1.${prefix}${level.toString(16).padStart(2, '0')}"`;
}

function sourceConfiguration(media: CatalogItem, type: DecodingType): MediaDecodingConfiguration | undefined {
	const contentType = media.video_codec === 'h264' && media.bit_depth === 8 ? h264ContentType(media.video_profile, media.video_level) : undefined;
	if (!contentType || !media.width || !media.height || !media.bitrate || !media.frame_rate_milli) return undefined;
	const audio = media.audio?.[0];
	let audioConfiguration: AudioConfiguration | undefined;
	if (audio) {
		if (audio.codec !== 'aac' || audio.profile !== 'LC' || !audio.channels || !audio.sample_rate || !audio.bitrate) return undefined;
		audioConfiguration = { contentType: compatibility.audioContentType, channels: String(audio.channels), bitrate: audio.bitrate, samplerate: audio.sample_rate };
	}
	return {
		type,
		video: { contentType, width: media.width, height: media.height, bitrate: media.bitrate, framerate: media.frame_rate_milli / 1000 },
		...(audioConfiguration ? { audio: audioConfiguration } : {}),
	};
}

function transcodeConfiguration(media: CatalogItem, type: DecodingType): MediaDecodingConfiguration | undefined {
	const videoCodec = media.video_codec?.toLowerCase();
	const audioCodec = media.audio?.[0]?.codec.toLowerCase();
	if (!videoCodec || !transcodableVideo.has(videoCodec) || audioCodec && !transcodableAudio.has(audioCodec) ||
		!media.width || !media.height || media.width % 2 !== 0 || media.height % 2 !== 0 || !media.frame_rate_milli || !compatibilityDimensions(media.width, media.height) ||
		media.frame_rate_milli > compatibility.maxFrameRateMilli || media.hdr) return undefined;
	return {
		type,
		video: { contentType: compatibility.videoContentType, width: media.width, height: media.height, bitrate: compatibility.videoBitrate, framerate: compatibility.maxFrameRateMilli / 1000 },
		...(media.audio?.[0] ? { audio: { contentType: compatibility.audioContentType, channels: String(compatibility.audioChannels), bitrate: compatibility.audioBitrate, samplerate: compatibility.audioSampleRate } } : {}),
	};
}

async function supports(configuration: MediaDecodingConfiguration | undefined): Promise<boolean> {
	if (!configuration || !navigator.mediaCapabilities) return false;
	try {
		return (await navigator.mediaCapabilities.decodingInfo(configuration)).supported;
	} catch {
		return false;
	}
}

export async function browserCapabilities(media: CatalogItem): Promise<PlaybackCapabilities> {
	const probe = document.createElement('video');
	const mp4 = probe.canPlayType('video/mp4; codecs="avc1.640028, mp4a.40.2"') !== '';
	const webm = probe.canPlayType('video/webm; codecs="vp9, opus"') !== '';
	const nativeHLS = probe.canPlayType('application/vnd.apple.mpegurl') !== '';
	const mediaSourceHLS = typeof MediaSource !== 'undefined' && MediaSource.isTypeSupported('video/mp4; codecs="avc1.640028, mp4a.40.2"');
	const hlsType: DecodingType | undefined = nativeHLS ? 'file' : mediaSourceHLS ? 'media-source' : undefined;
	const isMP4 = media.container?.split(',').some((value) => mp4Aliases.has(value.trim().toLowerCase())) ?? false;
	const sourceHDR = ['smpte2084', 'arib-std-b67'].includes(media.hdr ?? '') ? media.hdr : undefined;
	const displaySupportsSource = !sourceHDR || typeof matchMedia !== 'undefined' && matchMedia('(dynamic-range: high)').matches;

	const direct = displaySupportsSource && isMP4 && await supports(sourceConfiguration(media, 'file'));
	const remux = displaySupportsSource && hlsType !== undefined && await supports(sourceConfiguration(media, hlsType));
	const transcode = hlsType !== undefined && await supports(transcodeConfiguration(media, hlsType));
	const sourceSupported = direct || remux;
	const audio = media.audio?.[0];

	return {
		containers: [...(mp4 ? ['mp4'] : []), ...(webm ? ['webm'] : [])],
		video_codecs: [...(mp4 ? ['h264'] : []), ...(webm ? ['vp9'] : [])],
		video_profiles: mp4 ? ['Baseline', 'Main', 'High'] : [],
		audio_codecs: [...(mp4 ? ['aac'] : []), ...(webm ? ['opus'] : [])],
		supports_fmp4_hls: hlsType !== undefined,
		supports_direct: direct,
		supports_remux: remux,
		supports_transcode: transcode,
		max_width: sourceSupported ? media.width : undefined,
		max_height: sourceSupported ? media.height : undefined,
		max_frame_rate_milli: sourceSupported ? media.frame_rate_milli : undefined,
		max_bit_depth: sourceSupported ? media.bit_depth : undefined,
		max_audio_channels: sourceSupported && audio ? audio.channels : undefined,
		hdr: sourceSupported && sourceHDR ? [sourceHDR] : [],
	};
}
