#!/bin/sh
# Print the release notes for one version from CHANGELOG.md: an optional one-line Summary (see below), a pre-release notice while the version is below 1.0.0, the
# entries under that version's heading, the text of --extra FILE if given (for example the list of files), and the fixed
# closing line "Next release: when ready". Output has LF line ends. Fails when the heading is missing or has no entries.
# Writes nothing. release.yml builds the release notes with it, for the version in VERSION.
# A section may start with one line "Summary: TEXT"; it is printed first, as "**Summary:** TEXT", and left out of the entries.
# usage: scripts/release-notes.sh [--extra FILE] X.Y.Z
#        scripts/release-notes.sh [--extra FILE] --unreleased   (the Unreleased entries; the notice follows VERSION)
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
usage() { echo "usage: scripts/release-notes.sh [--extra FILE] X.Y.Z | --unreleased" >&2; exit 2; }

want='' extra=''
while [ $# -gt 0 ]; do
  case "$1" in
    --extra) [ $# -ge 2 ] || usage; extra="$2"; shift ;;
    --unreleased) [ -z "$want" ] || usage; want=Unreleased ;;
    -h|--help) usage ;;
    -*) usage ;;
    *) [ -z "$want" ] || usage; want="$1" ;;
  esac
  shift
done
[ -n "$want" ] || usage
semver='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
if [ "$want" = Unreleased ]; then
  version="$(tr -d '\r\n' < "$root/VERSION")"
else
  version="$want"
fi
printf '%s\n' "$version" | grep -Eq "$semver" || { echo "release-notes: '$version' is not X.Y.Z" >&2; exit 2; }
[ -z "$extra" ] || [ -r "$extra" ] || { echo "release-notes: cannot read $extra" >&2; exit 2; }

# The lines after "## [<want>]" (alone, or followed by " - date ...") up to the next "## " heading or the link references.
# Leading and trailing blank lines are dropped. Exit 3: no such heading; exit 4: the section is empty.
body="$(tr -d '\r' < "$root/CHANGELOG.md" | awk -v want="$want" '
  BEGIN { head = "## [" want "]" }
  !found && (index($0, head) == 1) && (length($0) == length(head) || substr($0, length(head) + 1, 1) == " ") { found = 1; next }
  found && (/^## / || /^\[[^]]*\]: /) { exit }
  found { if ($0 ~ /^[ \t]*$/) { if (n) blank++ } else { while (blank) { print ""; blank-- } print; n++ } }
  END { if (!found) exit 3; if (!n) exit 4 }
')" || {
  rc=$?
  [ "$rc" = 3 ] && echo "release-notes: CHANGELOG.md has no '## [$want]' heading" >&2
  [ "$rc" = 4 ] && echo "release-notes: CHANGELOG.md has nothing under '## [$want]'" >&2
  exit 1
}

# An optional first line "Summary: TEXT" is the one-line account of what changed. It leads the notes, before the notice,
# because a chat post made from the notes shows only their start. It is not repeated in the body.
summary=''
case "$body" in
  "Summary: "*)
    first="$(printf '%s\n' "$body" | sed -n 1p)"
    summary="${first#Summary: }"
    body="$(printf '%s\n' "$body" | sed 1d | sed '/./,$!d')"
    [ -n "$(printf '%s' "$summary" | tr -d ' \t')" ] || { echo "release-notes: the Summary line of '$want' is empty" >&2; exit 1; }
    [ -n "$body" ] || { echo "release-notes: '$want' has a Summary but no entries" >&2; exit 1; }
    ;;
esac
[ -z "$summary" ] || printf '**Summary:** %s\n\n' "$summary"
case "$version" in
  0.*) printf '%s\n\n' "Pre-release: this is a 0.x version and may change without notice." ;;
esac
printf '%s\n\n' "$body"
if [ -n "$extra" ]; then
  printf '%s\n\n' "$(tr -d '\r' < "$extra")"
fi
printf '%s\n' "Next release: when ready"
