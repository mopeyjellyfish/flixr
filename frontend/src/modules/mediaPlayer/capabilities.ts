import type { CatalogItem, PlaybackCapabilities } from '../../core/api';

const mp4Aliases = new Set(['mov', 'mp4', 'm4a', '3gp', '3g2', 'mj2']);

export async function browserCapabilities(media: CatalogItem): Promise<PlaybackCapabilities> {
	const probe = document.createElement('video');
	const mp4 = probe.canPlayType('video/mp4; codecs="avc1.64001f, mp4a.40.2"') !== '';
	const webm = probe.canPlayType('video/webm; codecs="vp9, opus"') !== '';
	const nativeHLS = probe.canPlayType('application/vnd.apple.mpegurl') !== '';
	const mediaSourceHLS = typeof MediaSource !== 'undefined' && MediaSource.isTypeSupported('video/mp4; codecs="avc1.64001f, mp4a.40.2"');
	const audio = media.audio?.[0];
	const isMP4 = media.container?.split(',').some((value) => mp4Aliases.has(value.trim().toLowerCase())) ?? false;
	const hasKnownVideo = (media.width ?? 0) > 0 && (media.height ?? 0) > 0 && (media.bitrate ?? 0) > 0 && (media.frame_rate_milli ?? 0) > 0 && (media.bit_depth ?? 0) > 0;
	const directCandidate = isMP4 && media.video_codec === 'h264' && ['Baseline', 'Main', 'High'].includes(media.video_profile ?? '') && audio?.codec === 'aac' && (audio.channels ?? 0) > 0 && hasKnownVideo;
	let direct = false;
	if (directCandidate) {
		try {
			const audioSupported = probe.canPlayType('audio/mp4; codecs="mp4a.40.2"') !== '';
			if (navigator.mediaCapabilities) {
				direct = audioSupported && (await navigator.mediaCapabilities.decodingInfo({ type: 'file', video: { contentType: 'video/mp4; codecs="avc1.64001f"', width: media.width!, height: media.height!, bitrate: media.bitrate!, framerate: media.frame_rate_milli! / 1000 } })).supported;
			} else {
				direct = audioSupported && probe.canPlayType('video/mp4; codecs="avc1.64001f"') !== '';
			}
		} catch {
			direct = false;
		}
	}
	return {
		containers: [...(mp4 ? ['mp4'] : []), ...(webm ? ['webm'] : [])],
		video_codecs: [...(mp4 ? ['h264'] : []), ...(webm ? ['vp9'] : [])],
		video_profiles: mp4 ? ['Baseline', 'Main', 'High'] : [],
		audio_codecs: [...(mp4 ? ['aac'] : []), ...(webm ? ['opus'] : [])],
		supports_fmp4_hls: nativeHLS || mediaSourceHLS,
		supports_direct: direct,
		max_width: direct ? media.width : undefined,
		max_height: direct ? media.height : undefined,
		max_frame_rate_milli: direct ? media.frame_rate_milli : undefined,
		max_bit_depth: direct ? media.bit_depth : undefined,
		max_audio_channels: direct ? audio?.channels : undefined,
		hdr: direct && media.hdr ? [media.hdr] : [],
	};
}
