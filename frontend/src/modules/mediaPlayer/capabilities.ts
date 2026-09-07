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
	const videoCandidate = media.video_codec === 'h264' && ['Baseline', 'Main', 'High'].includes(media.video_profile ?? '') && hasKnownVideo;
	let videoSupported = false;
	if (videoCandidate) {
		try {
			if (navigator.mediaCapabilities) {
				videoSupported = (await navigator.mediaCapabilities.decodingInfo({ type: 'file', video: { contentType: 'video/mp4; codecs="avc1.64001f"', width: media.width!, height: media.height!, bitrate: media.bitrate!, framerate: media.frame_rate_milli! / 1000 } })).supported;
			} else {
				videoSupported = probe.canPlayType('video/mp4; codecs="avc1.64001f"') !== '';
			}
		} catch {
			videoSupported = false;
		}
	}
	// Generic codec support does not prove an AAC channel layout. We therefore
	// do not claim direct audio playback or an audio-channel maximum without a
	// complete native audio configuration.
	const direct = isMP4 && videoSupported && !audio;
	const hdr = ['smpte2084', 'arib-std-b67'].includes(media.hdr ?? '') ? media.hdr : undefined;
	return {
		containers: [...(mp4 ? ['mp4'] : []), ...(webm ? ['webm'] : [])],
		video_codecs: [...(mp4 ? ['h264'] : []), ...(webm ? ['vp9'] : [])],
		video_profiles: mp4 ? ['Baseline', 'Main', 'High'] : [],
		audio_codecs: [...(mp4 ? ['aac'] : []), ...(webm ? ['opus'] : [])],
		supports_fmp4_hls: nativeHLS || mediaSourceHLS,
		supports_direct: direct,
		max_width: videoSupported ? media.width : undefined,
		max_height: videoSupported ? media.height : undefined,
		max_frame_rate_milli: videoSupported ? media.frame_rate_milli : undefined,
		max_bit_depth: videoSupported ? media.bit_depth : undefined,
		hdr: videoSupported && hdr ? [hdr] : [],
	};
}
