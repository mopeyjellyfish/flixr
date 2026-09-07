import { afterEach, expect, it, vi } from 'vitest';
import type { CatalogItem } from '../../core/api';
import { browserCapabilities } from './capabilities';

const media: CatalogItem = { id: 'film', title: 'Blue Horizon', kind: 'film', local_only: true, container: 'mov,mp4,m4a,3gp,3g2,mj2', video_codec: 'h264', video_profile: 'High', width: 320, height: 180, bitrate: 5968, frame_rate_milli: 24000, bit_depth: 8, audio: [{ codec: 'aac', channels: 2 }] };

afterEach(() => { vi.restoreAllMocks(); Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: undefined }); });

it('uses a valid video-only MediaCapabilities request for MP4 aliases', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	const decodingInfo = vi.fn().mockResolvedValue({ supported: true });
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });
	const result = await browserCapabilities(media);
	expect(decodingInfo).toHaveBeenCalledWith({ type: 'file', video: { contentType: 'video/mp4; codecs="avc1.64001f"', width: 320, height: 180, bitrate: 5968, framerate: 24 } });
	expect(result).toMatchObject({ supports_direct: true, max_width: 320, max_audio_channels: 2 });
	expect(result.containers).toContain('mp4');
	expect(result.video_codecs).toContain('h264');
	expect(result.audio_codecs).toContain('aac');
});

it('falls back conservatively when the API rejects or media details are incomplete', async () => {
	vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
	const decodingInfo = vi.fn().mockRejectedValue(new TypeError('invalid configuration'));
	Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo } });
	expect((await browserCapabilities(media)).supports_direct).toBe(false);
	expect((await browserCapabilities({ ...media, width: undefined })).supports_direct).toBe(false);
});
