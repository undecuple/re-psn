#!/bin/sh
# Takes the in-app screenshots in docs/img/app-*.png: the real desktop app (re-psn-app ui) in headless Chromium, driven
# through its own screens (first run, Host, Play, Profile, Overview). The Host screen runs the app's own owned server on
# loopback; the Play screen checks a second loopback server that holds a room and a score from the C client's own test, as
# for docs/status.png.
# Everything is synthetic and runs in a private HOME, so no settings or profile of yours are read or written.
#
# These images are NOT part of tools/art/make_art.py or its --check: a browser screenshot is not byte-reproducible (fonts,
# Chromium version), so run this by hand when the app's screens change and commit the new PNGs.
#
#   sh tools/art/capture-app.sh   # from the repository root; needs go, gcc, nix (for Chromium and Node), python3 with
#                                 # Pillow (optional, makes the PNGs smaller), curl, and loopback ports 36100, 36101, 37100 and 9399 free
set -eu
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
RUN=$(mktemp -d)
APP=''
CH=''
SP=''
AP=''
trap 'kill "$CH" "$APP" "$AP" "$SP" 2>/dev/null || true; sleep 1; rm -rf "$RUN" 2>/dev/null || true' EXIT
(cd "$ROOT" && go build -o "$RUN/re-psn" ./server)
(cd "$ROOT/app" && go build -o "$RUN/re-psn-app" ./cmd/re-psn-app)
ln -s "$ROOT/client" "$RUN/client"
(cd "$RUN" && gcc -std=gnu11 -o test_psnr client/psnr.c client/test_psnr.c && gcc -std=gnu11 -o natcheck client/psnr.c "$ROOT/lab/natcheck.c")
# The server the Play screen checks: a room (alice hosting) and a score, then left running.
"$RUN/re-psn" -addr 127.0.0.1:36100 -http 127.0.0.1:36101 >"$RUN/play-server.log" 2>&1 &
SP=$!
sleep 1
"$RUN/test_psnr" 127.0.0.1 36100 >/dev/null
"$RUN/natcheck" 127.0.0.1 alice host 3658 >/dev/null 2>&1 &
AP=$!
sleep 1
mkdir -p "$RUN/home" "$RUN/profile" "$RUN/shots"
export HOME="$RUN/home" XDG_CONFIG_HOME="$RUN/home/.config" XDG_DATA_HOME="$RUN/home/.local/share"
GAME=127.0.0.1:37100
"$RUN/re-psn-app" ui --no-open --url-file "$RUN/link.txt" --profile-dir "$RUN/profile/p" >"$RUN/app.log" 2>&1 &
APP=$!
i=0; while [ ! -s "$RUN/link.txt" ] && [ $i -lt 50 ]; do sleep 0.2; i=$((i + 1)); done
[ -s "$RUN/link.txt" ] || { echo "capture-app: the app did not start" >&2; exit 1; }
nix shell nixpkgs#chromium -c chromium --headless=new --no-sandbox --disable-gpu --remote-debugging-port=9399 \
  --user-data-dir="$RUN/chrome" about:blank >"$RUN/chrome.log" 2>&1 &
CH=$!
i=0; while ! curl -sf http://127.0.0.1:9399/json/version >/dev/null 2>&1 && [ $i -lt 300 ]; do sleep 0.2; i=$((i + 1)); done
curl -sf http://127.0.0.1:9399/json/version >/dev/null 2>&1 || { echo "capture-app: Chromium did not start" >&2; exit 1; }
nix shell nixpkgs#nodejs_24 -c node "$ROOT/tools/art/capture-app.mjs" 9399 "$RUN/link.txt" "$RUN/shots" "$GAME" 127.0.0.1:36100 http://127.0.0.1:36101/
for f in "$RUN"/shots/app-*.png; do
  name=$(basename "$f")
  if python3 -c 'import PIL' 2>/dev/null; then
    python3 - "$f" "$ROOT/docs/img/$name" <<'PY'
import sys
from PIL import Image
im = Image.open(sys.argv[1]).convert("RGB")
im.quantize(64, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE).save(sys.argv[2], optimize=True)
PY
  else
    cp "$f" "$ROOT/docs/img/$name"
  fi
  echo "capture-app: wrote docs/img/$name"
done
