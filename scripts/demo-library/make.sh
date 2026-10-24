#!/usr/bin/env bash
# A throwaway Jellyfin with a demo library, for the README screenshots and the
# end-to-end test: Blender open movies (CC BY), silent-era films in the public
# domain and three old or Creative Commons series. The files are generated —
# a still frame with the title and silence, as long as the real thing — so the
# whole library is ~150 MB; Jellyfin fetches posters, backdrops and plots from
# TMDB by itself. See CREDITS.md.
#
#   scripts/demo-library/make.sh            # build (idempotent) and start on :18096
#   scripts/demo-library/make.sh stop       # remove the container (files stay)
#
# Account: demo / pocket-watch-demo
set -euo pipefail
DIR=${DEMO_DIR:-$HOME/.cache/pocket-watch-dev}
PORT=${DEMO_PORT:-18096}
NAME=pw-demo-jellyfin
IMAGE=jellyfin/jellyfin:10.11.11
HERE=$(cd "$(dirname "$0")" && pwd)
FONT=${DEMO_FONT:-/usr/share/fonts/truetype/dejavu}

if [ "${1:-}" = stop ]; then docker rm -f "$NAME"; exit 0; fi
mkdir -p "$DIR"/{config,cache,media/movies,media/shows}
if ! docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker run -d --name "$NAME" -p "$PORT:8096" -v "$DIR/config:/config" -v "$DIR/cache:/cache" -v "$DIR/media:/media" -v "$FONT:/fonts:ro" "$IMAGE" >/dev/null
fi

# the files, made with Jellyfin's own ffmpeg
FF=/usr/lib/jellyfin-ffmpeg/ffmpeg
make() { # out minutes label sub
  local out=$1 min=$2 label=$3 sub=$4
  docker exec "$NAME" test -s "$out" && return 0
  docker exec "$NAME" mkdir -p "$(dirname "$out")"
  local txt=${label//:/\\:}; txt=${txt//\'/}
  docker exec "$NAME" $FF -nostdin -loglevel error -y \
    -f lavfi -i "color=c=0x1e1b4b:s=960x540:r=1,drawtext=fontfile=/fonts/DejaVuSans-Bold.ttf:text='$txt':fontcolor=white:fontsize=46:x=(w-tw)/2:y=(h-th)/2-18,drawtext=fontfile=/fonts/DejaVuSans.ttf:text='$sub':fontcolor=0xc4b5fd:fontsize=24:x=(w-tw)/2:y=(h/2)+40" \
    -f lavfi -i "anullsrc=r=16000:cl=mono" -t $((min * 60)) \
    -c:v libx264 -preset ultrafast -tune stillimage -g 10 -pix_fmt yuv420p -c:a aac -b:a 12k -movflags +faststart "$out"
}
grep -v '^#' "$HERE/titles.txt" | while IFS='|' read -r kind a b c d e; do
  if [ "$kind" = movie ]; then
    make "/media/movies/$a ($b)/$a ($b).mp4" "$c" "$a" "$b · Pocket Watch demo"
  else
    for n in $(seq 1 "$d"); do
      ep=$(printf 'S%02dE%02d' "$c" "$n")
      make "/media/shows/$a ($b)/Season $(printf %02d "$c")/$a $ep.mp4" "$e" "$a" "$ep · Pocket Watch demo"
    done
  fi
done
# a subtitle file to test subtitles with
docker exec "$NAME" sh -c 'f="/media/movies/Sintel (2010)/Sintel (2010).en.srt"; [ -s "$f" ] || printf "1\n00:00:02,000 --> 00:00:06,000\nWhat brings you to the land of the gatekeepers?\n\n2\n00:00:08,000 --> 00:00:12,000\nI am searching for someone.\n\n3\n00:00:14,000 --> 00:00:19,000\nA dragon. A baby dragon.\n" > "$f"'

# first start: the wizard, the account and the libraries
API=http://localhost:$PORT
AUTH='MediaBrowser Client="pocket-watch-demo", Device="script", DeviceId="pw-demo-script", Version="1"'
for i in $(seq 1 60); do curl -fs "$API/System/Info/Public" >/dev/null && break; sleep 2; done
if curl -fs "$API/System/Info/Public" | grep -q '"StartupWizardCompleted":false'; then
  curl -fs -X POST "$API/Startup/Configuration" -H "Authorization: $AUTH" -H 'Content-Type: application/json' -d '{"UICulture":"en-US","MetadataCountryCode":"US","PreferredMetadataLanguage":"en"}'
  curl -fs "$API/Startup/User" -H "Authorization: $AUTH" >/dev/null
  curl -fs -X POST "$API/Startup/User" -H "Authorization: $AUTH" -H 'Content-Type: application/json' -d '{"Name":"demo","Password":"pocket-watch-demo"}'
  curl -fs -X POST "$API/Startup/RemoteAccess" -H "Authorization: $AUTH" -H 'Content-Type: application/json' -d '{"EnableRemoteAccess":true}'
  curl -fs -X POST "$API/Startup/Complete" -H "Authorization: $AUTH"
fi
TOKEN=$(curl -fs -X POST "$API/Users/AuthenticateByName" -H "Authorization: $AUTH" -H 'Content-Type: application/json' -d '{"Username":"demo","Pw":"pocket-watch-demo"}' | sed -n 's/.*"AccessToken":"\([^"]*\)".*/\1/p')
H="Authorization: $AUTH, Token=\"$TOKEN\""
if ! curl -fs "$API/Library/VirtualFolders" -H "$H" | grep -q '"Name":"Movies"'; then
  curl -fs -X POST "$API/Library/VirtualFolders?name=Movies&collectionType=movies&refreshLibrary=false" -H "$H" -H 'Content-Type: application/json' -d '{"LibraryOptions":{"PathInfos":[{"Path":"/media/movies"}],"EnableTrickplayImageExtraction":false}}'
  curl -fs -X POST "$API/Library/VirtualFolders?name=Shows&collectionType=tvshows&refreshLibrary=false" -H "$H" -H 'Content-Type: application/json' -d '{"LibraryOptions":{"PathInfos":[{"Path":"/media/shows"}],"EnableTrickplayImageExtraction":false}}'
  curl -fs -X POST "$API/Library/Refresh" -H "$H"
  echo "scanning… (TMDB metadata takes a minute or two)"
  # a fresh 10.11 links seasons to their show only on the second scan
  idle() { curl -fs "$API/ScheduledTasks?isHidden=false" -H "$H" | grep -q '"State":"Running"' && return 1 || return 0; }
  sleep 10; until idle; do sleep 3; done
  curl -fs -X POST "$API/Library/Refresh" -H "$H"
  sleep 5; until idle; do sleep 3; done
fi
echo "demo Jellyfin on $API (demo / pocket-watch-demo)"
echo "API key for Pocket Watch: create one with"
echo "  curl -X POST '$API/Auth/Keys?App=pocket-watch' -H '$H'"
