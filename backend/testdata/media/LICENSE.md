# Generated media fixture license

The Flixr project generates the media files in this directory with `backend/scripts/generate-media-fixtures.sh`.

The fixtures contain only synthetic FFmpeg color or test-pattern video and synthetic silent or sine-wave audio. They contain no third-party film, television, music, artwork, trademark, or personal data. The project distributes the generated files under the same MIT License as Flixr.

Fixture bounds:

- duration: 0.25 seconds for the direct/remux fixtures and 2 seconds for the compatibility fixture;
- frame size: 320×180;
- direct/remux codecs: generated H.264 video and silent AAC stereo;
- compatibility codecs: generated MPEG-4 Part 2 video and MP3 sine-wave audio in AVI;
- purpose: local scanner, ffprobe, catalog, direct/remux/transcode, and production-browser acceptance tests.

Regenerate the files only with the checked-in script. Review file duration and size before publication.
