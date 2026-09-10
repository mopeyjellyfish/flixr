import { afterEach, expect, it, vi } from 'vitest';
import type { CatalogItem } from '../../core/api';
import { browserCapabilities, browserVersionCapabilities } from './capabilities';

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

it('probes ffprobe Constrained Baseline H.264 as Baseline', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });

	await browserCapabilities({ ...media, video_profile: 'Constrained Baseline' });

	expect(decodingInfo).toHaveBeenCalledWith(expect.objectContaining({ video: expect.objectContaining({ contentType: 'video/mp4; codecs="avc1.42e00c"' }) }));
});

it('requires mapped playback for an external default audio track', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	vi.stubGlobal('MediaSource', { isTypeSupported: vi.fn().mockReturnValue(true) });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo: vi.fn().mockResolvedValue({ supported: true }) } });

	const result = await browserCapabilities({ ...media, audio: [{ ...media.audio![0], external: true }] });

	expect(result).toMatchObject({ supports_direct: false, supports_remux: true });
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

it('measures alternate sources independently without inheriting canonical limits', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	vi.stubGlobal('MediaSource', { isTypeSupported: vi.fn().mockReturnValue(true) });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo: vi.fn().mockResolvedValue({ supported: true }) } });
	const input = (video_codec: string, width: number, height: number) => ({ container: 'mp4', video_codec, video_profile: 'High', video_level: 40, width, height, bitrate: 8_000_000, frame_rate_milli: 24_000, bit_depth: 8, audio: media.audio });

	const result = await browserVersionCapabilities({ ...media, width: 1920, height: 1080, video_codec: 'hevc', versions: [
		{ id: 'source-hevc', label: '1080p · HEVC', edition_id: 'film', selected: false, available: true, capability_input: input('hevc', 1920, 1080) },
		{ id: 'source-h264', label: '4K · H.264', edition_id: 'film', selected: false, available: true, capability_input: input('h264', 3840, 2160) },
	] });

	expect(result.versionCapabilities?.['source-hevc']).toMatchObject({ supports_direct: false, max_width: undefined });
	expect(result.versionCapabilities?.['source-h264']).toMatchObject({ supports_direct: true, max_width: 3840, max_height: 2160 });
});

it('measures an explicitly unavailable source alongside available fallback choices', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo: vi.fn().mockResolvedValue({ supported: true }) } });
	const input = { container: 'mp4', video_codec: 'h264', video_profile: 'High', video_level: 40, width: 1920, height: 1080, bitrate: 5_000_000, frame_rate_milli: 24_000, bit_depth: 8, audio: media.audio };
	const result = await browserVersionCapabilities({ ...media, versions: [
		{ id: 'missing', label: 'Missing', edition_id: 'film', selected: true, available: false, capability_input: input },
		{ id: 'fallback', label: 'Fallback', edition_id: 'film', selected: false, available: true, capability_input: input },
	] }, 'missing');

	expect(Object.keys(result.versionCapabilities ?? {})).toEqual(['missing', 'fallback']);
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

it('assesses Managed Media Source when conventional MediaSource is absent', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	vi.stubGlobal('MediaSource', undefined);
	vi.stubGlobal('ManagedMediaSource', { isTypeSupported: vi.fn().mockReturnValue(true) });
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });

	const result = await browserCapabilities(media);

	expect(result.supports_fmp4_hls).toBe(true);
	expect(decodingInfo.mock.calls.slice(1).map(([configuration]) => configuration.type)).toEqual(['media-source', 'media-source']);
});

it('bounds a stalled capability probe without discarding completed supported paths', async () => {
	vi.useFakeTimers();
	try {
		vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
		vi.stubGlobal('MediaSource', { isTypeSupported: vi.fn().mockReturnValue(true) });
		const decodingInfo = vi.fn().mockImplementation((configuration: MediaDecodingConfiguration) => configuration.type === 'file' ? new Promise(() => {}) : Promise.resolve({ supported: true }));
		Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });
		let result: Awaited<ReturnType<typeof browserCapabilities>> | undefined;
		void browserCapabilities(media).then((value) => { result = value; });
		await vi.advanceTimersByTimeAsync(3000);
		expect(result).toMatchObject({ supports_direct: false, supports_remux: true, supports_transcode: true });
		expect(vi.getTimerCount()).toBe(0);
	} finally { vi.useRealTimers(); }
});
