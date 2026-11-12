#!/bin/sh
# Bump the version in every place that states it, in one go. VERSION is the source; this changes, and only these:
#   VERSION            the new version;
#   server/main.go     the default in `var version = "X.Y.Z"` (release builds also set it with -ldflags);
#   app/cmd/re-psn-app/main.go   the same default in the desktop app;
#   CHANGELOG.md       the entries under "## [Unreleased]" move under a new "## [X.Y.Z] - YYYY-MM-DD" heading, a fresh empty
#                      Unreleased stays above it, and a "[Unreleased]: .../compare/vOLD...HEAD" link, if there is one, moves on;
#   README.md          every X.Y.Z inside a one-line <!-- version -->...<!-- /version --> span.
# It refuses a version that is not X.Y.Z or not newer than VERSION, a CHANGELOG whose newest version is not VERSION or
# whose Unreleased section is empty, and a dirty working tree. --dry-run prints the diff and writes nothing (a dirty tree
# is allowed then). It makes no commit, tag or push: review the diff, run scripts/check-release.sh and commit it yourself.
# usage: scripts/bump-version.sh [--dry-run] [--date YYYY-MM-DD] X.Y.Z
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
usage() { echo "usage: scripts/bump-version.sh [--dry-run] [--date YYYY-MM-DD] X.Y.Z" >&2; exit 2; }
die() { echo "bump-version: $*" >&2; exit 1; }

dry=0 new='' date="$(date -u +%Y-%m-%d)"
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) dry=1 ;;
    --date) [ $# -ge 2 ] || usage; date="$2"; shift ;;
    -h|--help) usage ;;
    -*) usage ;;
    *) [ -z "$new" ] || usage; new="$1" ;;
  esac
  shift
done
[ -n "$new" ] || usage
semver='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
printf '%s\n' "$new" | grep -Eq "$semver" || die "'$new' is not X.Y.Z"
printf '%s\n' "$date" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' || die "--date '$date' is not YYYY-MM-DD"
cur="$(tr -d '\r\n' < "$root/VERSION")"
printf '%s\n' "$cur" | grep -Eq "$semver" || die "VERSION holds '$cur', not X.Y.Z"

# newer A B: A is a higher X.Y.Z than B
newer() {
  a1="${1%%.*}"; a3="${1##*.}"; a2="${1#*.}"; a2="${a2%.*}"
  b1="${2%%.*}"; b3="${2##*.}"; b2="${2#*.}"; b2="${b2%.*}"
  [ "$a1" -ne "$b1" ] && { [ "$a1" -gt "$b1" ]; return; }
  [ "$a2" -ne "$b2" ] && { [ "$a2" -gt "$b2" ]; return; }
  [ "$a3" -gt "$b3" ]
}
newer "$new" "$cur" || die "$new is not newer than the current $cur (VERSION)"

if [ "$dry" = 0 ]; then
  st="$(git -C "$root" status --porcelain 2>/dev/null)" || die "not a git checkout: $root"
  [ -z "$st" ] || die "the working tree has changes; commit or stash them first (or use --dry-run)"
elif [ -n "$(git -C "$root" status --porcelain 2>/dev/null || true)" ]; then
  echo "bump-version: note: the working tree has changes (allowed with --dry-run)" >&2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM
mkdir -p "$tmp/server" "$tmp/app/cmd/re-psn-app"
files="VERSION server/main.go app/cmd/re-psn-app/main.go CHANGELOG.md README.md"

# VERSION
printf '%s\n' "$new" > "$tmp/VERSION"

# server/main.go and app/cmd/re-psn-app/main.go: exactly one `var version = "<cur>"` line each
line="var version = \"$cur\""
for f in server/main.go app/cmd/re-psn-app/main.go; do
  n="$(grep -Fxc "$line" "$root/$f" || true)"
  [ "$n" = 1 ] || die "$f: expected one line '$line', found $n"
  awk -v old="$line" -v new="var version = \"$new\"" '$0 == old { print new; next } { print }' \
    "$root/$f" > "$tmp/$f"
done

# CHANGELOG.md
awk -v cur="$cur" -v new="$new" -v date="$date" '
  function fail(msg) { print "bump-version: CHANGELOG.md: " msg > "/dev/stderr"; bad = 1; exit 1 }
  /^## \[Unreleased\][ \t]*$/ {
    if (unrel) fail("more than one Unreleased heading")
    unrel = 1; inunrel = 1
    print; print ""; print "## [" new "] - " date; next
  }
  /^## \[[0-9]+\.[0-9]+\.[0-9]+\]/ {
    v = substr($0, 5); v = substr(v, 1, index(v, "]") - 1)
    if (!unrel) fail("the Unreleased heading must come before the version headings")
    if (!newest) { newest = v; if (v != cur) fail("newest version is " v ", VERSION says " cur) }
    if (v == new) fail("a " new " heading exists already")
    inunrel = 0
  }
  /^## / && !/^## \[Unreleased\]/ { inunrel = 0 }
  inunrel && !/^[ \t]*$/ { entries++ }
  /^\[Unreleased\]: / {
    want = "/compare/v" cur "...HEAD"
    if (substr($0, length($0) - length(want) + 1) != want) fail("the [Unreleased] link is not \".../compare/v" cur "...HEAD\"; fix it by hand")
    base = substr($0, 15, length($0) - 14 - length(want))
    print "[Unreleased]: " base "/compare/v" new "...HEAD"
    print "[" new "]: " base "/compare/v" cur "...v" new
    next
  }
  { print }
  END {
    if (bad) exit 1
    if (!unrel) fail("no \"## [Unreleased]\" heading")
    if (!entries) fail("nothing under Unreleased to release")
    if (!newest) fail("no version heading to compare with VERSION")
  }
' "$root/CHANGELOG.md" > "$tmp/CHANGELOG.md" || exit 1

# README.md: every X.Y.Z inside each one-line marker span
awk -v new="$new" '
  {
    out = ""; rest = $0
    while ((i = index(rest, "<!-- version -->")) > 0) {
      out = out substr(rest, 1, i + 15); rest = substr(rest, i + 16)
      j = index(rest, "<!-- /version -->")
      if (j == 0) { print "bump-version: README.md:" NR ": a version span is not closed on its line" > "/dev/stderr"; bad = 1; exit 1 }
      span = substr(rest, 1, j - 1); gsub(/[0-9]+\.[0-9]+\.[0-9]+/, new, span)
      out = out span; rest = substr(rest, j); spans++
    }
    print out rest
  }
  END { if (bad) exit 1; if (!spans) { print "bump-version: README.md has no <!-- version --> span" > "/dev/stderr"; exit 1 } }
' "$root/README.md" > "$tmp/README.md" || exit 1

if [ "$dry" = 1 ]; then
  for f in $files; do
    diff -u "$root/$f" "$tmp/$f" | sed "1s|.*|--- a/$f|;2s|.*|+++ b/$f|" || true
  done
  echo "bump-version: dry run, nothing written ($cur -> $new)" >&2
  exit 0
fi
for f in $files; do
  if ! cmp -s "$root/$f" "$tmp/$f"; then
    cp "$tmp/$f" "$root/$f"
    echo "changed $f"
  fi
done
"$root/scripts/release-notes.sh" "$new" > /dev/null || die "the notes for $new do not build"
echo "bump-version: $cur -> $new. Next: git diff, scripts/check-release.sh, then commit these files as one commit."
