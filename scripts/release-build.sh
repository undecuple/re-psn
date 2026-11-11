#!/usr/bin/env bash
# Build the release into dist/: archives of static server binaries (no cgo) for the three targets, archives of the desktop
# app (app/, a Go module of its own, also no cgo) for the same three targets, copies of
# scripts/install.sh and scripts/install.ps1 (byte for byte, under those names), and one SHA256SUMS (LF, no BOM) listing
# them. With --appimage-tools DIR it also builds re-psn-vX.Y.Z-x86_64.AppImage with scripts/build-appimage.sh (DIR holds
# the pinned files from `scripts/build-appimage.sh fetch DIR`), SHA256SUMS lists it too, and
# `scripts/check-release.sh --assets` then checks that dist/ holds exactly the ten release assets.
# Used by .github/workflows/release.yml and runnable locally.
# usage: scripts/release-build.sh vX.Y.Z [--appimage-tools DIR]
set -euo pipefail
usage="usage: scripts/release-build.sh vX.Y.Z [--appimage-tools DIR]"
tag="${1:?$usage}"
tools=''
case "$#" in
  1) ;;
  3) [ "$2" = --appimage-tools ] && [ -n "$3" ] || { echo "$usage" >&2; exit 2; }
     tools="$(cd "$3" && pwd)" ;;
  *) echo "$usage" >&2; exit 2 ;;
esac
case "$tag" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "tag '$tag' is not vMAJOR.MINOR.PATCH" >&2; exit 1 ;; esac
case "$tag" in *[!0-9v.]*) echo "tag '$tag' has odd characters" >&2; exit 1 ;; esac
version="${tag#v}"
root="$(cd "$(dirname "$0")/.." && pwd)"
dist="$root/dist"
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
  (cd "$root/app" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -mod=readonly -trimpath -ldflags "-s -w -X main.version=$version" -o "$d/$exe" ./cmd/re-psn-app)
  cp "$root/LICENSE" "$root/NOTICE.md" "$d/"
  cp "$root/app/README.md" "$d/README.md"
  if [ "$os" = windows ]; then
    (cd "$stage" && zip -qrX "$dist/$name.zip" "$name")
  else
    tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C "$stage" -czf "$dist/$name.tar.gz" "$name"
  fi
done

cp "$root/scripts/install.sh" "$root/scripts/install.ps1" "$dist/"

cd "$dist"
sha256sum re-psn-*.zip re-psn-*.tar.gz install.sh install.ps1 > SHA256SUMS
sha256sum -c SHA256SUMS
if [ -n "$tools" ]; then
  "$root/scripts/build-appimage.sh" build "$tag" "$tools"
  "$root/scripts/check-release.sh" --assets "$tag" "$dist"
fi
