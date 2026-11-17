#!/bin/sh
# Install the re-psn server (or, with --app, the desktop app) on Linux from a GitHub release of undecuple/re-psn, for the current user only.
#
# usage: sh install.sh [--version vX.Y.Z] [--dir DIR] [--appimage | --app] [--dry-run] [--allow-root] [--help]
#
# What it does, in order:
#   1. Checks the system: Linux on amd64 (x86_64) or arm64 (aarch64). macOS and other systems have no release
#      builds; it says so and exits 1. It refuses to run as root unless --allow-root is given.
#   2. Picks the release: --version or RE_PSN_VERSION, else the newest release that is not a draft, pre-releases
#      included (every release is a pre-release for now). That one lookup reads
#      https://api.github.com/repos/undecuple/re-psn/releases; with a pinned version it is skipped.
#   3. Downloads re-psn-vX.Y.Z-linux-<arch>.tar.gz (with --appimage, re-psn-vX.Y.Z-x86_64.AppImage; with --app,
#      re-psn-app-vX.Y.Z-linux-<arch>.tar.gz, which releases after v0.4.0 carry) and
#      SHA256SUMS from https://github.com/undecuple/re-psn/releases/download/vX.Y.Z/ into a temporary directory,
#      with curl (--proto =https --tlsv1.2) or GNU wget (HTTPS and TLS 1.2 only). No other host is named here;
#      GitHub redirects the download to its own file servers.
#   4. Checks the download against its line in SHA256SUMS (sha256sum, shasum -a 256 or openssl). A missing line
#      or a different hash stops it before anything is written outside the temporary directory.
#   5. Installs the re-psn binary (or re-psn.AppImage) into DIR, mode 0755: copied to a temporary name in DIR,
#      then renamed over the old file, so a running server keeps its old file and nothing half-written is left.
#      The archive's LICENSE, NOTICE.md and examples/ go to ${XDG_DATA_HOME:-~/.local/share}/re-psn/vX.Y.Z/ (with
#      --app: re-psn-app plus its matching re-psn-host; metadata into .../re-psn-app/vX.Y.Z/).
#      App updates stage both executables and attempt rollback on a reported move failure. Stop the app/host first;
#      two flat files are not a crash-atomic update. Archives without re-psn-host are refused.
#   6. Says what it did, how to start the server (or the app), and the line to add to your shell profile when DIR is not on
#      PATH. It never edits a profile and never uses sudo.
#
# DIR is --dir, else RE_PSN_INSTALL_DIR, else ~/.local/bin. --dry-run prints these steps with the URLs and
# paths and then stops: no lookup, no download, nothing written. scripts/test-install.sh tests this script with
# stub curl, uname and id commands on PATH; the script has no test switch of its own.
set -eu

repo=undecuple/re-psn
api="https://api.github.com/repos/$repo/releases?per_page=20"
dl="https://github.com/$repo/releases/download"

say() { printf '%s\n' "$*"; }
die() { printf 're-psn install: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
usage: sh install.sh [options]

Installs the re-psn server for the current user from a GitHub release (Linux amd64 or arm64).

  --version vX.Y.Z  install this release (or set RE_PSN_VERSION); default: the newest release,
                    pre-releases included
  --dir DIR         install into DIR (or set RE_PSN_INSTALL_DIR); default: ~/.local/bin
  --appimage        install re-psn.AppImage instead of the archive's binary (x86_64 only)
  --app             install re-psn-app and its matching re-psn-host component; keeps standalone re-psn
  --dry-run         print the steps and stop; download and write nothing
  --allow-root      run even as root (not recommended)
  -h, --help        show this help

The download is checked against SHA256SUMS before anything is installed. No sudo, no shell profile edits.
EOF
}

version="${RE_PSN_VERSION:-}"
dir="${RE_PSN_INSTALL_DIR:-}"
appimage=0
app=0
dry=0
allow_root=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || die "--version needs a value, such as v0.3.0"; version="$2"; shift 2 ;;
    --version=*) version="${1#--version=}"; shift ;;
    --dir) [ "$#" -ge 2 ] || die "--dir needs a directory"; dir="$2"; shift 2 ;;
    --dir=*) dir="${1#--dir=}"; shift ;;
    --appimage) appimage=1; shift ;;
    --app) app=1; shift ;;
    --dry-run) dry=1; shift ;;
    --allow-root) allow_root=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown option: $1" ;;
  esac
done

[ "$app" = 0 ] || [ "$appimage" = 0 ] || die "--app and --appimage cannot be combined."
pkg=re-psn
[ "$app" = 0 ] || pkg=re-psn-app

# 1. The system.
os="$(uname -s)"
cpu="$(uname -m)"
case "$os" in
  Linux) ;;
  Darwin) die "macOS is not supported: re-psn has no macOS builds. Build it from source instead (see the README)." ;;
  *) die "$os is not supported: there are release builds for Linux (amd64, arm64) and Windows (amd64) only." ;;
esac
case "$cpu" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "CPU $cpu is not supported: the Linux builds are for amd64 (x86_64) and arm64 (aarch64) only." ;;
esac
[ "$appimage" = 0 ] || [ "$arch" = amd64 ] || die "the AppImage is built for x86_64 only; leave out --appimage on $cpu."
if [ "$(id -u)" = 0 ] && [ "$allow_root" = 0 ]; then
  die "refusing to run as root: run it as the user who will run the server, or pass --allow-root."
fi

[ -n "${HOME:-}" ] || [ -n "$dir" ] || die "HOME is not set; pass --dir DIR."
[ -n "$dir" ] || dir="$HOME/.local/bin"
case "$dir" in /*) ;; *) dir="$(pwd)/$dir" ;; esac
dir="${dir%/}"
[ -n "$dir" ] || die "refusing to install into /."

if [ -n "$version" ]; then
  case "$version" in v*) ;; *) version="v$version" ;; esac
  case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) die "version '$version' is not vMAJOR.MINOR.PATCH" ;; esac
  case "$version" in *[!0-9v.]*|v*v*|*..*|*.|*.*.*.*) die "version '$version' is not vMAJOR.MINOR.PATCH" ;; esac
fi

# The download tool. Only https URLs are fetched, and redirects must stay on https.
if command -v curl >/dev/null 2>&1; then
  fetch() { curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --retry 2 -o "$1" "$2"; }
  fetch_api() { curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL -H 'Accept: application/vnd.github+json' -o "$1" "$2"; }
elif command -v wget >/dev/null 2>&1 && wget --help 2>&1 | grep -q -- '--https-only'; then
  fetch() { wget --https-only --secure-protocol=TLSv1_2 -q -O "$1" "$2"; }
  fetch_api() { wget --https-only --secure-protocol=TLSv1_2 -q --header='Accept: application/vnd.github+json' -O "$1" "$2"; }
elif [ "$dry" = 0 ]; then
  die "needs curl, or a GNU wget that has --https-only (BusyBox wget cannot be limited to HTTPS)."
fi

# The SHA-256 tool, printing only the hash of one file.
if command -v sha256sum >/dev/null 2>&1; then
  hash_of() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  hash_of() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
elif command -v openssl >/dev/null 2>&1; then
  hash_of() { openssl dgst -sha256 -r "$1" | cut -d ' ' -f 1; }
elif [ "$dry" = 0 ]; then
  die "needs sha256sum, shasum or openssl to check the download."
fi

data="${XDG_DATA_HOME:-${HOME:-}/.local/share}"
case "$data" in /*) ;; *) data='' ;; esac
on_path=0
case ":${PATH:-}:" in *":$dir:"*) on_path=1 ;; esac

if [ "$dry" = 1 ]; then
  v="${version:-<newest release>}"
  if [ "$appimage" = 1 ]; then asset="re-psn-$v-x86_64.AppImage"; target=re-psn.AppImage
  else asset="$pkg-$v-linux-$arch.tar.gz"; target=$pkg; fi
  say "Dry run: nothing is downloaded or written."
  say "  system:   Linux $arch"
  if [ -n "$version" ]; then say "  release:  $version (pinned)"
  else say "  release:  the newest non-draft release, read from $api"; fi
  say "  download: $dl/$v/$asset"
  say "            $dl/$v/SHA256SUMS"
  say "  check:    the SHA-256 of $asset against its line in SHA256SUMS; stop on a mismatch or no line"
  say "  install:  $dir/$target (mode 0755, renamed into place)"
  if [ "$app" = 1 ]; then
    say "            $dir/re-psn-host (mode 0755, matching app component)"
    say "            $dir/re-psn-window (mode 0755, the window program, when the archive has it)"
    say "  update:   stop the app and its host first; staged pair replacement is not crash-atomic"
  fi
  if [ "$appimage" = 0 ] && [ -n "$data" ]; then
    if [ "$app" = 1 ]; then say "  extras:   $data/$pkg/$v (LICENSE, NOTICE.md, README.md)"
    else say "  examples: $data/$pkg/$v/examples"; fi
  fi
  [ "$on_path" = 1 ] || say "  PATH:     $dir is not on PATH; the line to add would be printed"
  exit 0
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/re-psn-install.XXXXXX")"
part=''
pair_lock=''
pair_host_changed=0
pair_app_changed=0
pair_complete=0
pair_had_host=0
pair_had_app=0
pair_window_changed=0
pair_had_window=0
cleanup() {
  install_status=$?
  recovery_failed=0
  if [ -n "$pair_lock" ] && [ "$pair_complete" = 0 ]; then
    if [ "$pair_app_changed" = 1 ]; then
      if [ "$pair_had_app" = 1 ]; then mv -f "$pair_lock/old-app" "$dir/re-psn-app" || recovery_failed=1
      else rm -f "$dir/re-psn-app" || recovery_failed=1; fi
    fi
    if [ "$pair_window_changed" = 1 ]; then
      if [ "$pair_had_window" = 1 ]; then mv -f "$pair_lock/old-window" "$dir/re-psn-window" || recovery_failed=1
      else rm -f "$dir/re-psn-window" || recovery_failed=1; fi
    fi
    if [ "$pair_host_changed" = 1 ]; then
      if [ "$pair_had_host" = 1 ]; then mv -f "$pair_lock/old-host" "$dir/re-psn-host" || recovery_failed=1
      else rm -f "$dir/re-psn-host" || recovery_failed=1; fi
    fi
  fi
  if [ -n "$pair_lock" ]; then
    if [ "$recovery_failed" = 0 ]; then rm -rf "$pair_lock"
    else
      say "re-psn install: pair rollback incomplete; keep recovery files in $pair_lock and restore manually." >&2
      install_status=1
    fi
  fi
  [ -z "$part" ] || rm -f "$part"
  rm -rf "$tmp"
  trap - EXIT
  exit "$install_status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

# 2. The release.
if [ -z "$version" ]; then
  fetch_api "$tmp/releases.json" "$api" || die "could not read the release list from $api"
  # Split the JSON at , { } [ ] and take the first tag_name whose release has "draft": false. Every release
  # object has tag_name before draft, neither key appears in its nested objects, and a quote inside a string
  # is escaped, so text in the release notes cannot match.
  version="$(awk '
    { n = split($0, kv, /[][,{}]/)
      for (i = 1; i <= n; i++) {
        k = kv[i]
        if (k ~ /^[ \t]*"tag_name"[ \t]*:/) { sub(/^[^:]*:[ \t]*"/, "", k); sub(/".*$/, "", k); t = k }
        else if (k ~ /^[ \t]*"draft"[ \t]*:[ \t]*false/ && t != "") { print t; exit }
        else if (k ~ /^[ \t]*"draft"[ \t]*:/) t = ""
      } }' "$tmp/releases.json")"
  [ -n "$version" ] || die "no published release found at $api"
  case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) die "the newest release has an unexpected tag" ;; esac
  case "$version" in *[!0-9v.]*|v*v*|*..*|*.|*.*.*.*) die "the newest release has an unexpected tag" ;; esac
  say "Newest release: $version"
else
  say "Release: $version"
fi

if [ "$appimage" = 1 ]; then asset="re-psn-$version-x86_64.AppImage"; target=re-psn.AppImage
else asset="$pkg-$version-linux-$arch.tar.gz"; target=$pkg; fi

# 3. The download.
say "Downloading $dl/$version/$asset"
fetch "$tmp/SHA256SUMS" "$dl/$version/SHA256SUMS" || die "could not download SHA256SUMS for $version"
fetch "$tmp/$asset" "$dl/$version/$asset" || die "could not download $asset (is $version a release with ${app:+app }Linux builds?${app:+ The app archives start after v0.4.0.})"

# 4. The check.
want="$(awk -v n="$asset" '$2 == n || $2 == "*" n { print $1 }' "$tmp/SHA256SUMS")"
case "$want" in
  '') die "SHA256SUMS has no line for $asset; nothing was installed." ;;
  *[!0-9a-fA-F]*) die "SHA256SUMS has more than one line, or a malformed one, for $asset; nothing was installed." ;;
esac
[ "${#want}" = 64 ] || die "SHA256SUMS has a malformed line for $asset; nothing was installed."
got="$(hash_of "$tmp/$asset")"
want="$(printf '%s' "$want" | tr 'A-F' 'a-f')"
got="$(printf '%s' "$got" | tr 'A-F' 'a-f')"
[ "$got" = "$want" ] || die "checksum mismatch for $asset (expected $want, got $got); nothing was installed."
say "Checksum OK: $want"

if [ "$appimage" = 1 ]; then
  src="$tmp/$asset"
  extras=''
else
  mkdir "$tmp/x"
  tar -xzf "$tmp/$asset" -C "$tmp/x"
  extras="$tmp/x/$pkg-$version-linux-$arch"
  src="$extras/$pkg"
  if [ ! -f "$src" ] || [ -L "$src" ]; then
    die "the archive has no $pkg binary where expected; nothing was installed."
  fi
  if [ "$app" = 1 ]; then
    host_src="$extras/re-psn-host"
    if [ ! -f "$host_src" ] || [ -L "$host_src" ]; then
      die "the app archive has no matching re-psn-host component; nothing was installed."
    fi
    # The window program is optional: releases before it was added do not carry it.
    window_src="$extras/re-psn-window"
    if [ -L "$window_src" ] || { [ -e "$window_src" ] && [ ! -f "$window_src" ]; }; then
      die "the app archive has a re-psn-window that is not a regular file; nothing was installed."
    fi
    [ -f "$window_src" ] || window_src=''
  fi
fi

# 5. Stage before replacement. The app pair has rollback for reported move
# failures, but two flat filenames cannot be replaced atomically across a crash.
mkdir -p "$dir"
if [ "$app" = 1 ]; then
  for old_target in "$dir/re-psn-app" "$dir/re-psn-host" "$dir/re-psn-window"; do
    if [ -L "$old_target" ] || { [ -e "$old_target" ] && [ ! -f "$old_target" ]; }; then
      die "app component target is not a regular file: $old_target; nothing was replaced."
    fi
  done
  # mkdir is exclusive. Never remove another installer's or a stale lock by guess.
  candidate_lock="$dir/.re-psn-app.install.lock"
  (umask 077; mkdir "$candidate_lock") || die "app update lock exists or cannot be created: $candidate_lock; stop other installers and inspect a stale lock manually."
  pair_lock="$candidate_lock"
  cp "$src" "$pair_lock/new-app"
  cp "$host_src" "$pair_lock/new-host"
  chmod 0755 "$pair_lock/new-app" "$pair_lock/new-host"
  if [ -n "$window_src" ]; then
    cp "$window_src" "$pair_lock/new-window"
    chmod 0755 "$pair_lock/new-window"
    if [ -f "$dir/re-psn-window" ]; then cp -p "$dir/re-psn-window" "$pair_lock/old-window"; pair_had_window=1; fi
  fi
  if [ -f "$dir/re-psn-app" ]; then cp -p "$dir/re-psn-app" "$pair_lock/old-app"; pair_had_app=1; fi
  if [ -f "$dir/re-psn-host" ]; then cp -p "$dir/re-psn-host" "$pair_lock/old-host"; pair_had_host=1; fi
  # Set rollback intent before each move so an interrupt between commands is covered.
  pair_host_changed=1
  mv -f "$pair_lock/new-host" "$dir/re-psn-host"
  if [ -n "$window_src" ]; then
    pair_window_changed=1
    mv -f "$pair_lock/new-window" "$dir/re-psn-window"
  fi
  pair_app_changed=1
  mv -f "$pair_lock/new-app" "$dir/re-psn-app"
  pair_complete=1
  if [ -n "$window_src" ]; then
    say "Installed $dir/re-psn-app, $dir/re-psn-host and $dir/re-psn-window"
  else
    say "Installed $dir/re-psn-app and $dir/re-psn-host"
    if [ -f "$dir/re-psn-window" ]; then
      say "This release has no window program; the re-psn-window already in $dir is from an older release."
    fi
  fi
  say "The files were staged and replaced together; this is not crash-atomic. No process was stopped or started."
else
part="$dir/.$target.part.$$"
cp "$src" "$part"
chmod 0755 "$part"
mv -f "$part" "$dir/$target"
part=''
say "Installed $dir/$target"
fi

shared=''
if [ -n "$extras" ] && [ -n "$data" ]; then
  shared="$data/$pkg/$version"
  if [ "$app" = 1 ]; then extra_files="LICENSE NOTICE.md README.md"; else extra_files="LICENSE NOTICE.md examples"; fi
  if [ -e "$shared" ]; then
    say "Kept $shared as it was (the files for $version are already there)"
  else
    mkdir -p "$data/$pkg"
    mkdir "$tmp/share"
    for f in $extra_files; do
      [ ! -e "$extras/$f" ] || cp -R "$extras/$f" "$tmp/share/"
    done
    mv "$tmp/share" "$shared"
    if [ "$app" = 1 ]; then say "Copied LICENSE, NOTICE.md and README.md to $shared"
    else say "Copied LICENSE, NOTICE.md and examples/ to $shared"; fi
  fi
fi

# 6. What next.
say ""
if [ "$on_path" = 1 ]; then
  if [ "$app" = 1 ]; then say "Start the app:          $target"; else say "Start the server:       $target"; fi
else
  say "$dir is not on your PATH. To add it, put this line in ~/.profile (or your shell's startup file):"
  say "  export PATH=\"$dir:\$PATH\""
  if [ "$app" = 1 ]; then say "Start the app:          $dir/$target"; else say "Start the server:       $dir/$target"; fi
fi
if [ "$app" = 1 ]; then
  if [ -n "$window_src" ]; then
    say "The app opens in its own window ($dir/re-psn-window beside it); run it with --browser for a browser tab."
  else
    say "The app opens its private local browser interface."
  fi
  say "Its matching host component is $dir/re-psn-host; standalone re-psn was not changed."
  [ -z "$shared" ] || say "Its README:              $shared/README.md"
  exit 0
fi
say "With the status page:   add -http 127.0.0.1:36101 and open http://127.0.0.1:36101/"
say "All flags:              $target -h"
if [ -n "$shared" ]; then
  say "Examples (settings file, systemd unit, proxies): $shared/examples"
else
  say "Examples (settings file, systemd unit, proxies): https://github.com/$repo/tree/$version/deploy"
fi
