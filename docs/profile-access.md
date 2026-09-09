# Profile content access

Owners can edit **Content access** for each household profile under **Server
settings → Household**. Existing profiles and newly created profiles start with
all libraries and content available, preserving the behavior of earlier Flixr
versions.

Library rules use logical library IDs. Choosing specific libraries excludes
libraries added later until the owner selects them. When one logical title has
physical copies in several libraries, Flixr shows it if an allowed library has
a present copy and opens that allowed copy for playback. A series contains only
episodes available through allowed libraries.

Tag matching is case-insensitive. When allowed tags are configured, a title
must have at least one of them. Any blocked tag hides the title, even when an
allowed tag also matches. Tags never bypass a library or rating rule.

Rating limits use these ordered regional mappings:

- United Kingdom: `U`, `PG`, `12`/`12A`, `15`, `18`, `R18`.
- United States: `G`/`TV-Y`/`TV-Y7`/`TV-G`, `PG`/`TV-PG`,
  `PG-13`/`TV-14`, `R`/`TV-MA`, `NC-17`.

A blank content rating follows the profile’s explicit **Unrated titles** rule.
A non-blank rating absent from the selected region’s mapping is unknown and is
blocked whenever a rating limit is active. This prevents an unfamiliar label
from silently passing a limit.

Flixr enforces these rules on the server for browse and search results, result
counts, details and episodes, artwork, viewing history, personal ratings,
progress, My List, Continue Watching, Next Up, playback and its generated
assets, previews, and local screen play commands. Policy-sensitive responses
are not browser-cached. Saving a changed policy ends that profile’s current
sessions and playback; viewers choose the profile again before continuing.
Content already downloaded into a device or browser before a restriction
changed cannot be withdrawn from that device; the server refuses later access.
Flixr does not currently expose extras or download APIs. Any future endpoint
that returns catalog metadata or media must apply the same profile policy before
resolving a source or honoring a conditional request.
