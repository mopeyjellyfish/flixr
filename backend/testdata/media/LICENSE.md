# Generated media fixture license

The Flixr project generates the media files in this directory with `backend/scripts/generate-media-fixtures.sh`.

The fixtures contain only synthetic FFmpeg color or test-pattern video, synthetic silent or sine-wave audio, and the one-line caption authored in the generator. They contain no third-party film, television, music, artwork, trademark, or personal data. The project distributes the generated files under the same MIT License as Flixr.

Fixture bounds:

- duration: 2 seconds to 10 minutes;
- frame size: 320×180;
- corpus: MP4, MKV, WebM with irregular frame timestamps, AVI, MOV, TS, M2TS, a ten-minute low-bitrate seek source, two named cuts, and a deliberately truncated AVI;
- streams: baseline H.264/AAC, MPEG-4/MP3 and MPEG-4/AAC compatibility paths, VP9/Opus, MPEG-2/MP2 transport paths, plus an MKV with English/French audio, English forced/default and French normal SRT tracks, and chapters;
- purpose: local scanner, ffprobe, catalog, direct/remux/transcode, and production-browser acceptance tests.

`manifest.json` records each source command, expected streams, consuming issue, classification, and committed-file SHA-256. Its hashes identify the committed files, not a reproducible-generator promise: Matroska/WebM muxing can vary container IDs between runs even with FFmpeg 8.0.1. The tagged runner checks stream, duration, chapter, subtitle-disposition, VFR, and M2TS-packet semantics; regenerate and update hashes and semantic expectations together. The named theatrical and director cuts are separate fixture files for #43; they are intentionally outside the default film/TV roots.

Regenerate the files only with the checked-in script. Review file duration, total size, and manifest hashes before publication.
