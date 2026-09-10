# Flixr v0.15.2 database fixture

`flixr.db` is a stopped, checkpointed SQLite database using the schema shipped
by Flixr v0.15.2 at commit `9e63ba7565c54a59c86570664ddc3b31192f6f17`.
Its SHA-256 digest is
`6a86fb0ae0b4fc96b294ee3bf2f20f7159e5ed769773061754a24f4fca72750d`.

The fixture was built by applying the tag's embedded migrations in filename
order, recording each applied version, then applying `seed.sql`. It
contains schema version 27 and exercises preservation of owner and profile
authorization, home view/sort choice, library order, audio and subtitle
language choices, My List, progress, ratings and viewing history. The current
test upgrades a copy, leaving this source fixture immutable.

Streaming quality and measured throughput use browser `localStorage`; playback
speed and autoplay cancellation are current-player runtime state. They are not
database fields and are intentionally absent from this fixture. Database
migration and backup/restore cannot reset or preserve browser-only state.
