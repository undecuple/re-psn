#!/bin/sh
# Release checks that need no network: with --tag, that the tag is v + VERSION (checked first, so a wrong tag fails before
# anything else runs); VERSION, the defaults in server/main.go and app/cmd/re-psn-app/main.go, the newest CHANGELOG.md version and the README version spans
# agree (TestVersionAgrees); the bump and notes scripts pass their tests; and the notes for the current version build from
# the real CHANGELOG.md. Run by release-build.sh (without --tag) and by release.yml on a tag (with --tag).
# With --assets TAG DIR it checks only a built release in DIR (TestCheckReleaseAssets): DIR holds the ten assets and
# nothing else, SHA256SUMS has one 'HASH  NAME' line (LF, no BOM) for each of the other nine and verifies, and install.sh
# and install.ps1 are byte for byte this tree's scripts/install.sh and scripts/install.ps1. Run by release-build.sh once
# the AppImage is built.
# usage: scripts/check-release.sh [--tag vX.Y.Z | --assets vX.Y.Z DIR]
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
usage() { echo "usage: scripts/check-release.sh [--tag vX.Y.Z | --assets vX.Y.Z DIR]" >&2; exit 2; }
fail() { echo "check-release: $*" >&2; exit 1; }

# assets TAG DIR
assets() {
  tag="$1"; dir="$2"
  case "$tag" in v[0-9]*.[0-9]*.[0-9]*) ;; *) fail "tag '$tag' is not vMAJOR.MINOR.PATCH" ;; esac
  case "$tag" in *[!0-9v.]*) fail "tag '$tag' has odd characters" ;; esac
  [ -d "$dir" ] || fail "$dir is not a directory"
  # The release assets; .github/workflows/release.yml and release-check.yml name the same ten (TestReleaseAssetListsAgree).
  want="re-psn-$tag-windows-amd64.zip re-psn-$tag-linux-amd64.tar.gz re-psn-$tag-linux-arm64.tar.gz re-psn-app-$tag-windows-amd64.zip re-psn-app-$tag-linux-amd64.tar.gz re-psn-app-$tag-linux-arm64.tar.gz re-psn-$tag-x86_64.AppImage install.sh install.ps1 SHA256SUMS"
  # shellcheck disable=SC2086 # split the list into lines
  want="$(printf '%s\n' $want | LC_ALL=C sort)"
  got="$(cd "$dir" && find . ! -name . -prune | sed 's#^\./##' | LC_ALL=C sort)"
  if [ "$got" != "$want" ]; then
    # shellcheck disable=SC2086
    fail "the files in $dir are not the ten assets; want: $(printf '%s ' $want) got: $(printf '%s ' $got)"
  fi
  sums="$dir/SHA256SUMS"
  if LC_ALL=C grep -qv '^[0-9a-f]\{64\}  [A-Za-z0-9._-]*$' "$sums"; then
    fail "SHA256SUMS has a line that is not 'HASH  NAME' (a CR, a BOM or something else)"
  fi
  listed="$(LC_ALL=C sed 's/^[0-9a-f]*  //' "$sums" | LC_ALL=C sort)"
  if [ "$listed" != "$(printf '%s\n' "$want" | grep -vx SHA256SUMS)" ]; then
    # shellcheck disable=SC2086
    fail "SHA256SUMS does not list each of the other nine assets once; it lists: $(printf '%s ' $listed)"
  fi
  (cd "$dir" && sha256sum -c --quiet SHA256SUMS) || fail "SHA256SUMS does not verify"
  for s in install.sh install.ps1; do
    cmp -s "$dir/$s" "$root/scripts/$s" || fail "$s differs from scripts/$s"
  done
  echo "release assets passed for $tag"
}

tag=''
case "${1-}" in
  '') [ $# -eq 0 ] || usage ;;
  --tag) [ $# -eq 2 ] && [ -n "$2" ] || usage; tag="$2" ;;
  --assets) [ $# -eq 3 ] && [ -n "$2" ] && [ -n "$3" ] || usage; assets "$2" "$3"; exit 0 ;;
  *) usage ;;
esac
version="$(tr -d '\r\n' < "$root/VERSION")"
if [ -n "$tag" ] && [ "$tag" != "v$version" ]; then
  echo "check-release: tag $tag is not v + VERSION (v$version)" >&2
  exit 1
fi
(cd "$root/server" && go test -count=1 -run '^Test(VersionAgrees.*|ReleaseNotes|BumpVersion|CheckReleaseTag|CheckReleaseAssets|ReleaseAssetListsAgree|ReleaseCheckWorkflow)$' .)
"$root/scripts/release-notes.sh" "$version" > /dev/null
echo "release checks passed for $version${tag:+ (tag $tag)}"
