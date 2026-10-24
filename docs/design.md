# How it works

## Why on top of Jellyfin, not instead of it

A media server is mostly plumbing Jellyfin already does well: scanning folders, matching titles on TMDB,
artwork, transcoding with hardware acceleration, subtitles in every format, trickplay thumbnails, and the API every
TV, phone and Infuse speaks. Rewriting that would take months and give nobody anything they can feel; it would
also cut off every app in the house.

What Jellyfin's own web client doesn't do well is the household around the films: knowing what each person
actually watched (on every device), what they want to watch, what they asked for and when it arrived, what's
coming next week, and helping decide on a Friday evening. That is Pocket Watch:

| Jellyfin | Pocket Watch |
|---|---|
| Files, scanning, metadata, artwork | A fast, calm web app for the household, desktop and phone |
| Transcoding, subtitles, trickplay | A player that plays the file as it is when the browser can, HLS otherwise; skip intro, next episode, seek previews |
| Resume points, watched marks, favourites (per account) | A watch history it owns — from its own player and every other Jellyfin app — with stats and a day view |
| The API for every other app | A watchlist that spans the library and what isn't here yet |
| | Jellyseerr requests as yourself, with a push when they arrive; upcoming episodes from Sonarr and Radarr |
| | "What should we watch?" from a sentence; Pocket Core: one sign-in, per-person logins, Pocket AI, Pocket Day |

The split mirrors Pocket Music on Navidrome and Pocket Photos on Immich. Keep using Infuse or the TV app alongside:
they share resume points and watched marks with Pocket Watch because both act as the same Jellyfin account.

## Per person, always

Everyone signs in to Pocket Watch with their own account and watches with **their own** Jellyfin account: a token
from signing in once (password or Quick Connect — the password itself is never kept), or the login Pocket Core
holds for them. History, watchlist, followed requests and settings are keyed by person; another person's ids
answer 404 and admins get no read access to anyone's watching. The box's services (Jellyseerr, Sonarr, Radarr,
Maintainerr) are configured once by an admin, and Pocket Watch only ever uses them *as the person*: requests go in
as their own Jellyseerr user, and they only see their own.

## Playing

The browser asks `POST /play/{id}` with what it can decode (codecs, native HLS, screen width). Pocket Watch turns
that into a Jellyfin device profile and asks `PlaybackInfo` as the person. If the file fits the browser, it plays
**as it is** (`DirectPlay`, byte ranges, no CPU on the server); otherwise Jellyfin remuxes or transcodes to HLS,
played with hls.js (or natively on Safari and iPhone). Changing the audio track, image subtitles (burned in) or
the quality asks again from the current second; if a direct-played file turns out not to decode, the player falls
back to a transcode by itself.

The browser never sees a Jellyfin token: every stream, playlist, segment, subtitle and trickplay tile goes through
`/api/v1/media/Videos/<id>/…`, where the person's token is added on the way. Text subtitles come as WebVTT tracks
(converted by Jellyfin), so switching them is instant.

Progress goes back to Jellyfin every ten seconds as the person (`/Sessions/Playing…`), so "continue watching" is
right on the TV too; a session whose browser went quiet is ended after ten minutes. With an API key (which has no
session of its own) the resume point and watched mark are written explicitly.

Media segments (Jellyfin 10.10+, e.g. from the Intro Skipper plugin) drive "Skip intro" and "Skip recap"; the
credits, or the last 25 seconds, bring up the next episode with a ten-second countdown.

## Subtitle timing

Subtitles running early or late are fixed in the player: `−1 s / −0.1 / +0.1 / +1 s` (or `g` and `h`) move the track
for you at once — the cues are shifted in the browser, so it works for subtitles inside the video file too — and the
offset is remembered per person, title and track. When the track is an .srt next to the video and Pocket Subtitles
is connected, **Save for everyone** sends that offset to Pocket Subtitles: it shifts every line of the file, keeps a
backup (undo), pins the file so its nightly sync doesn't redo the timing, and tells Jellyfin; your own offset goes
back to zero and the track reloads. Watch reaches Pocket Subtitles as the person through a Pocket Core link
(`subtitles:sync`), or with a token an admin set for the box.

## History

One row per title per day: what it is (copied, so it survives the file leaving), how far it got, how many seconds
were actually watched, whether it counts as watched (90% by default, or into the credits), and where (Pocket
Watch, another Jellyfin app, or read back from before).

- **Here:** the player writes as it plays (every two minutes and at the end).
- **Everywhere else:** every `SYNC_EVERY` each person's Jellyfin state is read for titles whose "last played" moved
  since the last look — finished ones and ones in progress. Marking a whole season watched stamps every episode
  with the same second; those count as watched but not as time spent watching.
- **Before:** the first look copies everything the account has finished, each on the day it was last played.

Stats, the day view (`/day/{date}`, which Pocket Day reads through an app link), the weekly recap and the
`watch.watched` event all come from these rows.

## Requests and arrivals

Requests are made in Jellyseerr with its API key *on behalf of* the person's own Jellyseerr user (found by their
Jellyfin account, imported from Jellyfin on their first request if missing), so Jellyseerr's rules — permissions,
quotas, approval — apply as usual. Every 15 minutes each person's requests are read back from Jellyseerr; a request
seen turning available sends them a push and a `watch.request_available` event, and links their watchlist entry to
the arrived title.

## What should we watch?

A sentence becomes a few picks from what the person hasn't watched. With AI (Pocket AI through Core, or their own
OpenAI-compatible key) the model gets a compact list of unwatched films and shows — name, year, genres, length,
age rating, score — and answers with ids from that list and a line each; nothing else about the person is sent.
Without AI, mood words map to genres ("funny" → Comedy, "scary" → Horror and Thriller), "short" and "classic" set
length and era, and the hour shuffles the rest.
