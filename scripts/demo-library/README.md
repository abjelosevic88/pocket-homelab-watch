# Demo library

`make.sh` runs a throwaway Jellyfin (`jellyfin/jellyfin:10.11.11`, port 18096, data in
`~/.cache/pocket-watch-dev`) with 21 films and three series, for the README screenshots and the browser test in
`scripts/screenshots.mjs`. The files are generated with Jellyfin's own ffmpeg — a still frame and silence at the
real running time, ~240 MB in all — and Jellyfin matches them on TMDB. Sintel comes with an English `.srt` to test
subtitles. Credits in [CREDITS.md](CREDITS.md).

```sh
scripts/demo-library/make.sh          # build (idempotent) and start; prints how to mint an API key
scripts/demo-library/make.sh stop     # remove the container (the files stay)
```
