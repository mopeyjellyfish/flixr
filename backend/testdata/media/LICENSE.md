# Generated media fixture license

The Flixr project generates the media files in this directory with `backend/scripts/generate-media-fixtures.sh`.

The fixtures contain only solid colors and silent synthetic audio. They contain no third-party film, television, music, artwork, trademark, or personal data. The project distributes the generated files under the same MIT License as Flixr.

Fixture bounds:

- duration: 0.25 seconds per file;
- frame size: 320×180;
- video: generated H.264 color frames;
- audio: generated silent AAC stereo;
- purpose: local scanner, ffprobe, catalog, and production-browser acceptance tests.

Regenerate the files only with the checked-in script. Review file duration and size before publication.
