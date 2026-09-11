# TV episode numbering and viewing order

FlixR keeps a media file's source numbers separate from its viewing order. Changing
an order does not rename files, move episodes between libraries, or reset viewing
history. Each multi-episode file remains one playable item with one resume position.

## Name episode files

Place episodes inside a folder for their series. Season and episode numbers sort
numerically, so episode 100 follows episode 99. Use season zero for specials.

```text
TV/Signal/Season 01/Signal S01E99.mp4
TV/Signal/Season 01/Signal S01E100.mp4
TV/Signal/Season 01/Signal S01E101-E102.mp4
TV/Signal/Season 00/Signal S00E01.mp4
```

A contiguous multi-episode file such as `S01E101-E102` covers both episode numbers.
Next Up advances past its final episode. Completing the file records completion
for that playable item; it does not create separate resume positions or split the
video into chapters. Unsupported, conflicting or descending number ranges require
owner review rather than being silently truncated to their first number.

## Choose or repair an order

1. Open **Server settings → Metadata → Episode order** and find the series.
2. Select **Aired**, **DVD**, or **Absolute**. Review each file's position, final
   position, display season and episode span. Mark specials explicitly.
3. If the series has a TMDB match and online metadata is enabled, load its provider
   orders and preview one. A preview changes the draft only. FlixR does not invent
   a DVD or absolute order when the provider has none.
4. Repair any missing or ambiguous assignments, then save. Positions describe the
   viewing sequence; display numbers are the labels you want to see. A file that
   covers multiple episodes must map to one contiguous span.

Saved mappings remain available with online metadata disabled and after a server
restart. Importing a provider order is an explicit action, not a startup or
playback dependency. Provider errors leave the saved order unchanged. If another
owner session saves first, reload before applying your changes again.

An incomplete order is marked as needing repair. Episodes remain available for
manual playback, but Next Up does not guess through an unresolved sequence.
Overlapping positions within the same edition and library context are ambiguous.
Different cuts remain distinct; changing their order never authorizes a jump to
another edition, library or inaccessible file.

The default aired view retains source-season controls. Alternate-order browsing
uses the saved viewing positions rather than sorting the source episode labels.
Specials remain excluded from automatic advancement unless explicitly included.
Missing or completed episodes are skipped within a valid sequence, using the
current profile's access rules and progress.

To return to filename ordering, reset to the parsed aired order and save. Source
numbers are preserved independently of the selected order. Ordering data is part
of the configuration database and is included in normal backups; use the
[update and recovery procedure](docker.md#update-and-recovery) before upgrading.

## Provider order reference

TMDB exposes [series episode groups](https://developer.themoviedb.org/reference/tv-series-episode-groups)
and [group details](https://developer.themoviedb.org/reference/tv-episode-group-details).
FlixR supports the original-air-date, absolute and DVD group types. A provider
preview resolves a source span only when it has a unique, contiguous assignment;
owner repair is required otherwise. Local NFO episode-order fields are not imported
by the [local metadata schema](local-metadata.md).
