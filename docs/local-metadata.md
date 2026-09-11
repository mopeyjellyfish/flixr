# Local metadata and artwork

FlixR imports a small, portable subset of XML NFO files during a library scan. The import works with remote metadata disabled and keeps every source file inside its configured library root.

## Supported names

For movies and episodes, place the NFO beside the video and give it the same base name:

```text
Movies/Arrival (2016).mkv
Movies/Arrival (2016).nfo
TV/Example/Example.S01E01.mkv
TV/Example/Example.S01E01.nfo
```

Name-specific artwork uses `<video-name>-poster.jpg` and `<video-name>-fanart.jpg`. `-backdrop` is accepted as an alias for `-fanart`; `-thumb` is accepted as an episode or movie poster alias. JPEG, PNG, and WebP files are supported.

For a TV series, use `tvshow.nfo`, `poster.jpg`, and `fanart.jpg` in the show's top-level folder. For a movie in its own folder, FlixR also accepts `movie.nfo`, `poster.jpg`, and `fanart.jpg`. This directory fallback is disabled when the folder contains more than one movie, preventing one generic file from changing unrelated movies in a flat library. Name-specific files take priority over directory fallbacks. The same names with `.jpeg`, `.png`, or `.webp` are accepted.

## Supported fields

The document root must be `<movie>`, `<tvshow>`, or `<episodedetails>` for its media type. FlixR reads:

- `title`
- `plot` as the synopsis
- `year`, or the four-digit year from `premiered`/`aired` when `year` is absent
- `mpaa` as the local content rating for movies and series
- repeated `tag` values for movies and series
- one numeric `uniqueid type="tmdb"` for movies and series

Episode `mpaa` and `tag` values are ignored because household policy currently applies series-level rating and tags to episodes. `genre`, cast, ratings, sort titles, episode orders, and other NFO fields are also outside this bounded schema. The owner scan result lists ignored field names without exposing library paths to household catalog APIs.

A TMDB ID must be positive numeric text and valid for the movie or series namespace. Repeated equal IDs are accepted; conflicting IDs are rejected. An NFO ID never overrides an explicit owner match or unmatch. Valid conflicts between different titles enter the existing identity-repair workflow and never merge watch history automatically.

## Precedence and recovery

Field resolution is deterministic:

1. a locked owner edit
2. valid local NFO or artwork
3. the last cached provider value
4. the filename-derived value

Provider refresh, manual match, and artwork retry keep local values visible. FlixR stores the provider or filename fallback separately before applying a local field. Removing a valid sidecar on a later complete scan removes that local layer and reveals the stored fallback. Version grouping does not copy metadata between editions; the selected canonical source supplies an anchor's sidecars, and ungrouping preserves each anchor's stored state.

Malformed XML, an invalid supported field, corrupt artwork, and an unsafe size do not erase the last valid imported value. Valid fields in the same NFO still update when another supported field is invalid. Owner scan-job details report the affected sidecar and a stable `local_metadata_invalid`, `local_metadata_ignored`, or `local_artwork_invalid` code. Fix the file and scan again.

NFO files are limited to 512 KiB. Artwork source files are limited to 5 MiB and 12 million pixels, with an additional maximum of 6400 by 9600 pixels. XML uses strict parsing. Built-in XML escapes such as `&amp;` work; document-type declarations and custom or external entities are rejected and are never fetched. Symlink sidecars are ignored, and a size, timestamp, or file-identity change during a confined read fails that sidecar without publishing a partial update.

Local fields, cached fallbacks, and artwork references commit in the scan's final database transaction. Cancellation or a database failure before commit leaves the previous catalog and artwork active. Committed imports survive restart and are included in the normal `/config` backup. Restore the database and artwork object directory together.
