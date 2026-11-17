#!/usr/bin/env bash
# Build the release into dist/: archives of static server binaries (no cgo) for the three targets, archives of the desktop
# app (app/, a Go module of its own, also no cgo) for the same three targets, copies of
# scripts/install.sh and scripts/install.ps1 (byte for byte, under those names), and one SHA256SUMS (LF, no BOM) listing
# them. With --shell-dir DIR the app archives also hold the native window program re-psn-window (re-psn-window.exe on
# Windows): DIR holds re-psn-window-windows-amd64.exe, re-psn-window-linux-amd64 and re-psn-window-linux-arm64, each built
# natively by release.yml, since the window needs a native toolchain and cannot be cross-built from here; all three must be
# there. Without it the app archives are as before. With --appimage-tools DIR it also builds re-psn-vX.Y.Z-x86_64.AppImage with scripts/build-appimage.sh (DIR holds
# the pinned files from `scripts/build-appimage.sh fetch DIR`), SHA256SUMS lists it too, and
# `scripts/check-release.sh --assets` then checks that dist/ holds exactly the ten release assets.
# Used by .github/workflows/release.yml and runnable locally.
# usage: scripts/release-build.sh vX.Y.Z [--appimage-tools DIR] [--shell-dir DIR]
set -euo pipefail
usage="usage: scripts/release-build.sh vX.Y.Z [--appimage-tools DIR] [--shell-dir DIR]"
tag="${1:?$usage}"
shift
tools=''
shells=''
while [ "$#" -gt 0 ]; do
  [ "$#" -ge 2 ] && [ -n "$2" ] || { echo "$usage" >&2; exit 2; }
  [ -d "$2" ] || { echo "$2 is not a directory" >&2; exit 2; }
  case "$1" in
    --appimage-tools) tools="$(cd "$2" && pwd)" ;;
    --shell-dir) shells="$(cd "$2" && pwd)" ;;
    *) echo "$usage" >&2; exit 2 ;;
  esac
  shift 2
done
case "$tag" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "tag '$tag' is not vMAJOR.MINOR.PATCH" >&2; exit 1 ;; esac
case "$tag" in *[!0-9v.]*) echo "tag '$tag' has odd characters" >&2; exit 1 ;; esac
version="${tag#v}"
root="$(cd "$(dirname "$0")/.." && pwd)"
dist="$root/dist"
if [ -n "$shells" ]; then
  for f in re-psn-window-windows-amd64.exe re-psn-window-linux-amd64 re-psn-window-linux-arm64; do
    [ -s "$shells/$f" ] || { echo "release-build: $shells/$f is missing or empty" >&2; exit 1; }
  done
fi
rm -rf "$dist" && mkdir -p "$dist"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

"$root/scripts/check-deploy.sh"
"$root/scripts/check-release.sh"

for target in windows/amd64 linux/amd64 linux/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  exe=re-psn; [ "$os" = windows ] && exe=re-psn.exe
  name="re-psn-$tag-$os-$arch"
  d="$stage/$name"; mkdir -p "$d"
  (cd "$root" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$d/$exe" ./server)
  cp "$root/LICENSE" "$root/NOTICE.md" "$d/"
  cp -r "$root/deploy" "$d/examples"
  if [ "$os" = windows ]; then
    (cd "$stage" && zip -qrX "$dist/$name.zip" "$name")
  else
    tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C "$stage" -czf "$dist/$name.tar.gz" "$name"
  fi
done

# The desktop app: re-psn-app-vX.Y.Z-OS-ARCH, with the app's own module and version variable.
for target in windows/amd64 linux/amd64 linux/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  exe=re-psn-app; [ "$os" = windows ] && exe=re-psn-app.exe
  name="re-psn-app-$tag-$os-$arch"
  d="$stage/$name"; mkdir -p "$d"
  app_ldflags="-s -w -X main.version=$version"
  host_exe=re-psn-host
  server_exe=re-psn
  if [ "$os" = windows ]; then
    app_ldflags="$app_ldflags -H=windowsgui"
    host_exe=re-psn-host.exe
    server_exe=re-psn.exe
  fi
  (cd "$root/app" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -mod=readonly -trimpath -ldflags "$app_ldflags" -o "$d/$exe" ./cmd/re-psn-app)
  # Copy the already-built server bytes. Its distinct installed name keeps an
  # app update from replacing the user's independent standalone server.
  cp "$stage/re-psn-$tag-$os-$arch/$server_exe" "$d/$host_exe"
  cp "$root/LICENSE" "$root/NOTICE.md" "$d/"
  cp "$root/app/README.md" "$d/README.md"
  if [ -n "$shells" ]; then
    if [ "$os" = windows ]; then
      cp "$shells/re-psn-window-$os-$arch.exe" "$d/re-psn-window.exe"
    else
      cp "$shells/re-psn-window-$os-$arch" "$d/re-psn-window"
      chmod 0755 "$d/re-psn-window"
    fi
  fi
  if [ "$os" = windows ]; then
    (cd "$stage" && zip -qrX "$dist/$name.zip" "$name")
  else
    tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C "$stage" -czf "$dist/$name.tar.gz" "$name"
  fi
done

# With --shell-dir, every app archive must hold the window program next to the app.
if [ -n "$shells" ]; then
  for target in windows/amd64 linux/amd64 linux/arm64; do
    os="${target%/*}"; arch="${target#*/}"
    name="re-psn-app-$tag-$os-$arch"
    if [ "$os" = windows ]; then
      unzip -Z1 "$dist/$name.zip" | grep -qx "$name/re-psn-window.exe" || { echo "release-build: $name.zip lacks re-psn-window.exe" >&2; exit 1; }
    else
      tar -tzf "$dist/$name.tar.gz" | grep -qx "$name/re-psn-window" || { echo "release-build: $name.tar.gz lacks re-psn-window" >&2; exit 1; }
    fi
  done
fi

cp "$root/scripts/install.sh" "$root/scripts/install.ps1" "$dist/"

cd "$dist"
sha256sum re-psn-*.zip re-psn-*.tar.gz install.sh install.ps1 > SHA256SUMS
sha256sum -c SHA256SUMS
if [ -n "$tools" ]; then
  "$root/scripts/build-appimage.sh" build "$tag" "$tools"
  "$root/scripts/check-release.sh" --assets "$tag" "$dist"
fi
