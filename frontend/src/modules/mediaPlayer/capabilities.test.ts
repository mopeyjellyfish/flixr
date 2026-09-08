import { afterEach, expect, it, vi } from 'vitest';
import type { CatalogItem } from '../../core/api';
import { browserCapabilities } from './capabilities';

const media: CatalogItem = {
	id: 'film', title: 'Blue Horizon', kind: 'film', local_only: true,
	container: 'mov,mp4,m4a,3gp,3g2,mj2', video_codec: 'h264', video_profile: 'High', video_level: 12,
	width: 320, height: 180, bitrate: 5968, frame_rate_milli: 24000, bit_depth: 8,
	audio: [{ index: 1, codec: 'aac', profile: 'LC', channels: 2, sample_rate: 48000, bitrate: 2323 }],
};

afterEach(() => {
	vi.restoreAllMocks();
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: undefined });
	vi.unstubAllGlobals();
});

it('assesses the exact H.264/AAC title separately for direct and fMP4 remux playback', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockImplementation((type) => type === 'application/vnd.apple.mpegurl' ? '' : 'probably');
	vi.stubGlobal('MediaSource', { isTypeSupported: vi.fn().mockReturnValue(true) });
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });

	const result = await browserCapabilities(media);

	expect(decodingInfo).toHaveBeenNthCalledWith(1, {
		type: 'file',
		video: { contentType: 'video/mp4; codecs="avc1.64000c"', width: 320, height: 180, bitrate: 5968, framerate: 24 },
		audio: { contentType: 'audio/mp4; codecs="mp4a.40.2"', channels: '2', bitrate: 2323, samplerate: 48000 },
	});
	expect(decodingInfo).toHaveBeenNthCalledWith(2, {
		type: 'media-source',
		video: { contentType: 'video/mp4; codecs="avc1.64000c"', width: 320, height: 180, bitrate: 5968, framerate: 24 },
		audio: { contentType: 'audio/mp4; codecs="mp4a.40.2"', channels: '2', bitrate: 2323, samplerate: 48000 },
	});
	expect(result).toMatchObject({ supports_direct: true, supports_remux: true, max_width: 320, max_audio_channels: 2 });
});

it('assesses the separately bounded H.264/AAC transcode output', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockImplementation((type) => type === 'application/vnd.apple.mpegurl' ? '' : 'probably');
	vi.stubGlobal('MediaSource', { isTypeSupported: vi.fn().mockReturnValue(true) });
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });

	const result = await browserCapabilities({ ...media, container: 'avi', video_codec: 'mpeg4', video_profile: 'Simple Profile', video_level: 1, bitrate: 647312, audio: [{ index: 1, codec: 'mp3', channels: 1, sample_rate: 48000, bitrate: 64000 }] });

	expect(decodingInfo).toHaveBeenCalledTimes(1);
	expect(decodingInfo).toHaveBeenCalledWith({
		type: 'media-source',
		video: { contentType: 'video/mp4; codecs="avc1.640028"', width: 320, height: 180, bitrate: 5_000_000, framerate: 30 },
		audio: { contentType: 'audio/mp4; codecs="mp4a.40.2"', channels: '2', bitrate: 128_000, samplerate: 48000 },
	});
	expect(result).toMatchObject({ supports_direct: false, supports_remux: false, supports_transcode: true });
});

it('keeps each failed or incomplete assessment conservative', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	const decodingInfo = vi.fn().mockRejectedValue(new TypeError('invalid configuration'));
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });
	const rejected = await browserCapabilities(media);
	expect(rejected).toMatchObject({ supports_direct: false, supports_remux: false, supports_transcode: false });
	expect(rejected.max_width).toBeUndefined();

	decodingInfo.mockResolvedValue({ supported: false });
	expect(await browserCapabilities({ ...media, width: undefined })).toMatchObject({ supports_direct: false, supports_remux: false, supports_transcode: false });
});

it('requires a high-dynamic-range display for HDR source playback and does not claim tone mapping', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: false }));
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });

	const result = await browserCapabilities({ ...media, hdr: 'smpte2084' });

	expect(result).toMatchObject({ supports_direct: false, supports_remux: false, supports_transcode: false, hdr: [] });
});

it('does not assess an output with dimensions the fixed encoder cannot produce', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });

	const result = await browserCapabilities({ ...media, container: 'avi', video_codec: 'mpeg4', width: 321, audio: [{ index: 1, codec: 'mp3' }] });

	expect(decodingInfo).not.toHaveBeenCalled();
	expect(result.supports_transcode).toBe(false);
});

it('assesses the rotated display geometry for a bounded portrait transcode', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });

	const result = await browserCapabilities({ ...media, container: 'avi', video_codec: 'mpeg4', width: 1080, height: 1920, audio: [{ index: 1, codec: 'mp3' }] });

	expect(decodingInfo).toHaveBeenCalledWith({
		type: 'file',
		video: { contentType: 'video/mp4; codecs="avc1.640028"', width: 1080, height: 1920, bitrate: 5_000_000, framerate: 30 },
		audio: { contentType: 'audio/mp4; codecs="mp4a.40.2"', channels: '2', bitrate: 128_000, samplerate: 48_000 },
	});
	expect(result.supports_transcode).toBe(true);
});

it('assesses MSE when both native HLS and MSE are advertised', async () => {
 vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
 vi.stubGlobal('MediaSource', {isTypeSupported:()=>true});
 const decodingInfo=vi.fn().mockResolvedValue({supported:true});
 Object.defineProperty(navigator,'mediaCapabilities',{configurable:true,value:{decodingInfo}});
 await browserCapabilities(media);
 expect(decodingInfo.mock.calls.slice(1).map(([configuration])=>configuration.type)).toEqual(['media-source','media-source']);
});
