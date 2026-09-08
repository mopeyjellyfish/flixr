# Text subtitles

Flixr discovers supported embedded text tracks and confined SRT or WebVTT files.
Put an external subtitle beside its video and use this name:

```text
Film 2026.mkv
Film 2026.eng.srt
Film 2026.fra.Forced.vtt
Film 2026.eng.SDH.srt
```

The file must share the exact video stem and directory. The first suffix is a
two-letter or three-letter language code, optionally followed by a BCP 47
subtag. `Forced`, `SDH`, and `CC` mark selection behavior; remaining suffix text
becomes the track title. Flixr ignores other locations and extensions.

Choose **Off** or a named track from the player. Flixr remembers explicit off,
language, and SDH preference for the active profile. Automatic selection favors
the profile language, forced tracks, SDH when preferred, and the source default.
The saved policy is applied again after reconnecting and when the next episode
starts. Each device can still choose a different track during playback.

Flixr converts cues to text-only WebVTT. Cue markup is escaped before it reaches
the browser. Extraction is limited to 10 seconds, one FFmpeg thread, 10,000
cues, 8 MiB of input or output, and 16 KiB per cue line. Stopping or revoking a
playback session cancels its active extraction. HLS cue times are rebased to the
generation start; direct-play cues retain the source timeline.

Downloaded playback is not implemented yet. Subtitle selection for downloaded
content remains an open qualification item and must be verified with the future
download runtime before issue #31 can close.
