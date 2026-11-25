#!/usr/bin/env bash
# Build the Linux AppImage of the server, re-psn-vX.Y.Z-x86_64.AppImage, from the linux/amd64 release archive in dist/.
# It holds the same server binary as that archive, its examples, LICENSE, NOTICE.md and THIRD-PARTY-GO.txt, an AppRun that runs the binary
# with the arguments given, a .desktop file and an icon (scripts/appimage/). x86_64 only.
#
# usage: scripts/build-appimage.sh fetch DIR           download the two pinned files below into DIR (the only network use)
#        scripts/build-appimage.sh build vX.Y.Z DIR    build from dist/ with the files in DIR; no network
#
# build checks both files against the SHA-256 pins below before running anything, and stops on a mismatch. It builds the
# AppDir with sorted contents, fixed modes and mtimes (SOURCE_DATE_EPOCH, default 0), packs it with appimagetool and the
# pinned type-2 runtime (--runtime-file, so appimagetool fetches nothing), then smoke-tests the result: the files in the
# AppImage equal the AppDir, the binary equals the archive's, `--appimage-extract-and-run -h` prints what the binary's -h
# prints, and the server it starts reports the tag's version. It adds the AppImage to
# dist/SHA256SUMS and runs `sha256sum -c SHA256SUMS`. Called by scripts/release-build.sh with --appimage-tools DIR.
set -euo pipefail

# Pinned tools. .github/workflows/release.yml pins the same URLs and hashes (TestAppImagePinsAgree checks they agree).
APPIMAGETOOL_VERSION=1.9.1
APPIMAGETOOL_URL=https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage
APPIMAGETOOL_SHA256=ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
RUNTIME_VERSION=20251108
RUNTIME_URL=https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64
RUNTIME_SHA256=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d

root="$(cd "$(dirname "$0")/.." && pwd)"
dist="$root/dist"
die() { echo "build-appimage: $*" >&2; exit 1; }
usage() { echo "usage: scripts/build-appimage.sh fetch DIR | build vX.Y.Z DIR" >&2; exit 2; }

# sha256_ok FILE WANT: true when FILE exists and its SHA-256 is WANT.
sha256_ok() {
  [ -f "$1" ] || { echo "build-appimage: $1 is missing" >&2; return 1; }
  local got
  got="$(sha256sum "$1" | cut -d' ' -f1)"
  [ "$got" = "$2" ] || { echo "build-appimage: SHA-256 mismatch for $1: got $got, want $2" >&2; return 1; }
}

fetch() {
  local dir="$1" name url sha
  mkdir -p "$dir"
  for name in appimagetool-x86_64.AppImage runtime-x86_64; do
    case "$name" in
      appimagetool-*) url="$APPIMAGETOOL_URL"; sha="$APPIMAGETOOL_SHA256" ;;
      *) url="$RUNTIME_URL"; sha="$RUNTIME_SHA256" ;;
    esac
    curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$dir/$name.part" "$url"
    sha256_ok "$dir/$name.part" "$sha" || { rm -f "$dir/$name.part"; exit 1; }
    mv "$dir/$name.part" "$dir/$name"
    echo "fetched $name ($sha)"
  done
}

build() {
  local tag="$1" tools="$2"
  case "$tag" in v[0-9]*.[0-9]*.[0-9]*) ;; *) die "tag '$tag' is not vMAJOR.MINOR.PATCH" ;; esac
  case "$tag" in *[!0-9v.]*) die "tag '$tag' has odd characters" ;; esac
  local version="${tag#v}" file_version
  file_version="$(tr -d '\r\n' < "$root/VERSION")"
  # v0.0.0 is the dry run's tag; any other tag must be v + VERSION, so the version inside is the one in VERSION.
  if [ "$tag" != v0.0.0 ] && [ "$version" != "$file_version" ]; then
    die "tag $tag is not v + VERSION (v$file_version)"
  fi
  local sde="${SOURCE_DATE_EPOCH:-0}"
  case "$sde" in ''|*[!0-9]*) die "SOURCE_DATE_EPOCH '$sde' is not a number" ;; esac

  local stage
  stage="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$stage'" EXIT
  # Copy the tools, then check the copies: what runs is what was checked.
  local f
  for f in appimagetool-x86_64.AppImage runtime-x86_64; do [ -f "$tools/$f" ] || die "$tools/$f is missing"; done
  cp "$tools/appimagetool-x86_64.AppImage" "$stage/appimagetool"
  cp "$tools/runtime-x86_64" "$stage/runtime"
  sha256_ok "$stage/appimagetool" "$APPIMAGETOOL_SHA256" || die "refusing to run appimagetool $APPIMAGETOOL_VERSION"
  sha256_ok "$stage/runtime" "$RUNTIME_SHA256" || die "refusing to use type-2 runtime $RUNTIME_VERSION"
  chmod 755 "$stage/appimagetool"

  local name="re-psn-$tag-x86_64.AppImage" archive="$dist/re-psn-$tag-linux-amd64.tar.gz"
  [ -f "$archive" ] || die "$archive is missing; run scripts/release-build.sh $tag first"
  mkdir -p "$stage/archive" "$stage/tmp"
  tar -xzf "$archive" -C "$stage/archive"
  local src="$stage/archive/re-psn-$tag-linux-amd64"

  local app="$stage/AppDir"
  mkdir -p "$app/usr/bin" "$app/usr/share/re-psn" "$app/usr/share/doc/re-psn"
  cp "$src/re-psn" "$app/usr/bin/re-psn"
  cp -R "$src/examples" "$app/usr/share/re-psn/examples"
  cp "$src/LICENSE" "$src/NOTICE.md" "$src/THIRD-PARTY-GO.txt" "$app/usr/share/doc/re-psn/"
  cp "$root/scripts/appimage/AppRun" "$root/scripts/appimage/re-psn.desktop" "$root/scripts/appimage/re-psn.svg" "$app/"
  cp "$root/scripts/appimage/re-psn.svg" "$app/.DirIcon"
  find "$app" -type d -exec chmod 755 {} +
  find "$app" -type f -exec chmod 644 {} +
  chmod 755 "$app/AppRun" "$app/usr/bin/re-psn"
  find "$app" -exec touch -h -d "@$sde" {} +

  # appimagetool runs from its own extracted copy (no FUSE needed); its temporary files stay under the stage.
  (cd "$stage" && env -u VERSION -u APPIMAGETOOL_APP_NAME ARCH=x86_64 SOURCE_DATE_EPOCH="$sde" TMPDIR="$stage/tmp" \
    APPIMAGE_EXTRACT_AND_RUN=1 ./appimagetool --no-appstream --runtime-file ./runtime AppDir "$dist/$name")

  smoke "$stage" "$src" "$app" "$dist/$name" "$version"

  cd "$dist"
  touch SHA256SUMS
  awk -v n="$name" '$2 != n' SHA256SUMS > "$stage/sums"
  sha256sum "$name" >> "$stage/sums"
  cat "$stage/sums" > SHA256SUMS
  sha256sum -c SHA256SUMS
}

# smoke STAGE ARCHIVE_DIR APPDIR APPIMAGE VERSION
smoke() {
  local stage="$1" src="$2" app="$3" image="$4" version="$5" s="$1/smoke"
  mkdir -p "$s"
  (cd "$s" && TMPDIR="$stage/tmp" "$image" --appimage-extract > /dev/null)
  diff -r "$app" "$s/squashfs-root" || die "the AppImage's files differ from the AppDir"
  cmp "$src/re-psn" "$s/squashfs-root/usr/bin/re-psn" || die "the AppImage's binary differs from the archive's"

  local norm='1s/^Usage of .*:$/Usage of re-psn:/'
  "$src/re-psn" -h > "$s/plain.txt" 2>&1 || die "re-psn -h failed"
  TMPDIR="$stage/tmp" "$image" --appimage-extract-and-run -h > "$s/image.txt" 2>&1 || die "the AppImage's -h failed"
  sed "$norm" "$s/plain.txt" > "$s/plain.norm"
  sed "$norm" "$s/image.txt" > "$s/image.norm"
  head -n 1 "$s/plain.norm" | grep -qx 'Usage of re-psn:' || die "re-psn -h does not start with its usage line"
  diff "$s/plain.norm" "$s/image.norm" || die "the AppImage's -h differs from the binary's"

  # The version: start the server through the AppImage's AppRun on loopback ports chosen by the system.
  "$s/squashfs-root/AppRun" -addr 127.0.0.1:0 -http '' > "$s/run.log" 2>&1 &
  local pid=$! _
  for _ in $(seq 50); do grep -q 'msg=started' "$s/run.log" && break; sleep 0.1; done
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  grep -qF "msg=started version=$version " "$s/run.log" ||
    { cat "$s/run.log" >&2; die "the server does not report version $version"; }
  echo "AppImage smoke test passed: -h matches the binary's, version $version"
}

case "${1-}" in
  fetch) [ $# -eq 2 ] && [ -n "$2" ] || usage; fetch "$2" ;;
  build) [ $# -eq 3 ] && [ -n "$2" ] && [ -n "$3" ] || usage; build "$2" "$3" ;;
  *) usage ;;
esac
