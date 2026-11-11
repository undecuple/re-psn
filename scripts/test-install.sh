#!/bin/sh
# Tests for scripts/install.sh, with no network. Each case runs the installer with `env -i` and a PATH that holds
# only links to the tools it needs plus stub `uname`, `id` and `curl` commands. The stub curl serves a fake
# release from a temporary directory: it maps https://github.com/undecuple/re-psn/releases/download/TAG/NAME to
# a file, answers the release list URL with a fake list, refuses any other URL and any call without
# --proto =https and --tlsv1.2, and logs every URL it was asked for.
#
# usage: sh scripts/test-install.sh [SHELL]    SHELL runs the installer (default: sh), for example dash or bash
set -eu

sh_under_test="${1:-sh}"
sh_under_test="$(command -v "$sh_under_test")" || { echo "no shell '${1:-sh}'" >&2; exit 2; }
root="$(cd "$(dirname "$0")/.." && pwd)"
installer="$root/scripts/install.sh"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT

pass=0
fail=0
ok() { pass=$((pass + 1)); printf 'ok   %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL %s\n' "$1"; [ ! -f "$T/out" ] || sed 's/^/     | /' "$T/out"; }
check() { name="$1"; shift; if "$@"; then ok "$name"; else bad "$name"; fi; }

# The fake release: v9.9.9 is a draft and must be skipped; v0.4.0 is the newest published one.
F="$T/fake"
mkdir -p "$F"
cat > "$F/releases.json" <<'EOF'
[
  {"tag_name": "v9.9.9", "name": "draft", "draft": true, "prerelease": true, "assets": [{"name": "x", "uploader": {"login": "a"}}]},
  {"tag_name": "v0.4.0", "name": "v0.4.0", "draft": false, "prerelease": true, "body": "notes, with \"tag_name\": \"v1.0.0\" in them"},
  {"tag_name": "v0.3.0", "name": "v0.3.0", "draft": false, "prerelease": true}
]
EOF
make_release() { # TAG
  for arch in amd64 arm64; do
    n="re-psn-$1-linux-$arch"
    mkdir -p "$T/build/$n/examples"
    printf '#!/bin/sh\necho "re-psn %s %s"\n' "$1" "$arch" > "$T/build/$n/re-psn"
    chmod 0755 "$T/build/$n/re-psn"
    echo license > "$T/build/$n/LICENSE"
    echo notice > "$T/build/$n/NOTICE.md"
    echo 'RE_PSN_HTTP=' > "$T/build/$n/examples/re-psn.env.example"
    mkdir -p "$F/$1"
    tar -C "$T/build" -czf "$F/$1/$n.tar.gz" "$n"
  done
  printf 'appimage %s\n' "$1" > "$F/$1/re-psn-$1-x86_64.AppImage"
  case "$1" in v0.4.0|v0.3.0) ;; *)   # the app archives start after v0.4.0
    for arch in amd64 arm64; do
      n="re-psn-app-$1-linux-$arch"
      mkdir -p "$T/build/$n"
      printf '#!/bin/sh\necho "re-psn-app %s %s"\n' "$1" "$arch" > "$T/build/$n/re-psn-app"
      chmod 0755 "$T/build/$n/re-psn-app"
      echo license > "$T/build/$n/LICENSE"
      echo notice > "$T/build/$n/NOTICE.md"
      echo 'app readme' > "$T/build/$n/README.md"
      tar -C "$T/build" -czf "$F/$1/$n.tar.gz" "$n"
    done ;;
  esac
  (cd "$F/$1" && sha256sum ./*.tar.gz ./*.AppImage | sed 's# \./# #' > SHA256SUMS)
}
make_release v0.4.0
make_release v0.3.0
make_release v0.5.0   # its SHA256SUMS gets a wrong hash for amd64
sed 's/^[0-9a-f]\{8\}\(.*linux-amd64.tar.gz\)$/00000000\1/' "$F/v0.5.0/SHA256SUMS" > "$F/s" && mv "$F/s" "$F/v0.5.0/SHA256SUMS"
make_release v0.6.0   # its SHA256SUMS has no amd64 line
grep -v linux-amd64 "$F/v0.6.0/SHA256SUMS" > "$F/s" && mv "$F/s" "$F/v0.6.0/SHA256SUMS"

# The tool directory: links to the real tools, and the stubs.
B="$T/bin"
mkdir -p "$B"
for t in awk cat chmod cp cut grep gzip mkdir mktemp mv openssl rm sed sha256sum shasum tar tr; do
  p="$(command -v "$t" 2>/dev/null)" && ln -s "$p" "$B/$t"
done
cat > "$B/uname" <<'EOF'
#!/bin/sh
case "$1" in -s) echo "$FAKE_OS" ;; -m) echo "$FAKE_CPU" ;; *) exit 1 ;; esac
EOF
cat > "$B/id" <<'EOF'
#!/bin/sh
[ "$1" = -u ] && echo "$FAKE_UID"
EOF
cat > "$B/curl" <<'EOF'
#!/bin/sh
out='' url='' proto=0 tls=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    --proto) [ "$2" = '=https' ] && proto=1; shift 2 ;;
    --proto-redir|-H|--retry) shift 2 ;;
    --tlsv1.2) tls=1; shift ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
echo "$url" >> "$FAKE/calls"
[ "$proto" = 1 ] && [ "$tls" = 1 ] || { echo "curl stub: missing --proto =https or --tlsv1.2" >&2; exit 2; }
case "$url" in
  'https://api.github.com/repos/undecuple/re-psn/releases?per_page=20') src="$FAKE/releases.json" ;;
  https://github.com/undecuple/re-psn/releases/download/*/*)
    rest="${url#https://github.com/undecuple/re-psn/releases/download/}"; src="$FAKE/$rest" ;;
  *) echo "curl stub: unexpected URL $url" >&2; exit 2 ;;
esac
[ -f "$src" ] || { echo "curl: (22) 404 $url" >&2; exit 22; }
cat "$src" > "$out"
EOF
chmod 0755 "$B/uname" "$B/id" "$B/curl"

# run OS CPU UID [args...]: a fresh TMPDIR each time, and a fresh HOME unless KEEP_HOME is set; the exit code goes to $rc, the output to $T/out.
run() {
  os="$1" cpu="$2" uid="$3"; shift 3
  [ -n "${KEEP_HOME:-}" ] || rm -rf "${T:?}/home"
  rm -rf "$T/tmp" "$F/calls"
  mkdir -p "$T/home" "$T/tmp"
  rc=0
  env -i PATH="${TEST_PATH:-$B}" HOME="$T/home" TMPDIR="$T/tmp" FAKE="$F" FAKE_OS="$os" FAKE_CPU="$cpu" \
    FAKE_UID="$uid" ${EXTRA_ENV:+"$EXTRA_ENV"} "$sh_under_test" "$installer" "$@" > "$T/out" 2>&1 || rc=$?
}
has() { grep -qF -- "$1" "$T/out"; }
empty() { [ -z "$(ls -A "$1" 2>/dev/null)" ]; }
nothing_written() { empty "$T/home" && empty "$T/tmp"; }
bin="$T/home/.local/bin"

run Linux x86_64 1000
check "amd64: the newest non-draft release is installed" \
  test "$rc" = 0 -a "$("$bin/re-psn")" = "re-psn v0.4.0 amd64"
check "amd64: says which release" has "Newest release: v0.4.0"
check "amd64: mode 0755" test -n "$(find "$bin/re-psn" -perm 0755)"
check "amd64: examples, LICENSE and NOTICE.md copied" \
  test -f "$T/home/.local/share/re-psn/v0.4.0/examples/re-psn.env.example" -a -f "$T/home/.local/share/re-psn/v0.4.0/NOTICE.md"
check "amd64: prints where the examples are" has "$T/home/.local/share/re-psn/v0.4.0/examples"
check "amd64: temporary directory removed" empty "$T/tmp"
check "amd64: no temporary file left in the install dir" test "$(ls -A "$bin")" = re-psn
check "amd64: only release URLs fetched" test "$(cat "$F/calls")" = "https://api.github.com/repos/undecuple/re-psn/releases?per_page=20
https://github.com/undecuple/re-psn/releases/download/v0.4.0/SHA256SUMS
https://github.com/undecuple/re-psn/releases/download/v0.4.0/re-psn-v0.4.0-linux-amd64.tar.gz"
check "PATH hint: the exact line when the dir is not on PATH" has "  export PATH=\"$bin:\$PATH\""

run Linux aarch64 1000 --version v0.3.0
check "arm64: pinned version installed" test "$rc" = 0 -a "$("$bin/re-psn")" = "re-psn v0.3.0 arm64"
check "arm64: pinned version skips the release list" test "$(grep -c api.github.com "$F/calls")" = 0
check "arm64: says which release" has "Release: v0.3.0"

EXTRA_ENV="RE_PSN_VERSION=0.3.0" run Linux arm64 1000
check "RE_PSN_VERSION pins the release (v added)" test "$rc" = 0 -a "$("$bin/re-psn")" = "re-psn v0.3.0 arm64"

run Linux x86_64 1000 --version v0.5.0
check "checksum mismatch: exits non-zero" test "$rc" != 0
check "checksum mismatch: says so" has "checksum mismatch"
check "checksum mismatch: nothing installed, temp removed" nothing_written

run Linux x86_64 1000 --version v0.6.0
check "missing SHA256SUMS line: exits non-zero" test "$rc" != 0
check "missing SHA256SUMS line: says so" has "SHA256SUMS has no line for re-psn-v0.6.0-linux-amd64.tar.gz"
check "missing SHA256SUMS line: nothing installed" nothing_written

run Linux x86_64 1000 --version v0.7.0
check "release without the asset: exits non-zero, nothing installed" test "$rc" != 0 -a -z "$(ls -A "$T/home")"

run Darwin arm64 1000
check "Darwin: exits 1 and says macOS is not supported" test "$rc" = 1
check "Darwin: message" has "macOS is not supported"
check "Darwin: nothing fetched or written" test ! -e "$F/calls" -a -z "$(ls -A "$T/home")"
run FreeBSD amd64 1000
check "FreeBSD: exits 1" test "$rc" = 1
run Linux riscv64 1000
check "riscv64: exits 1 and names the CPU" test "$rc" = 1
check "riscv64: message" has "CPU riscv64 is not supported"

run Linux x86_64 1000 --dry-run
check "dry run: exits 0" test "$rc" = 0
check "dry run: no download, nothing written" test ! -e "$F/calls"
check "dry run: nothing written" nothing_written
check "dry run: prints the install path" has "$bin/re-psn"
run Linux x86_64 1000 --dry-run --version v0.4.0 --dir "$T/home/opt"
check "dry run with --version and --dir: URLs and path" \
  has "https://github.com/undecuple/re-psn/releases/download/v0.4.0/re-psn-v0.4.0-linux-amd64.tar.gz"
check "dry run with --dir: nothing written" test ! -e "$T/home/opt" -a ! -e "$F/calls"

run Linux x86_64 1000 --dir "$T/home/custom/bin"
check "--dir: installed there" test "$rc" = 0 -a -x "$T/home/custom/bin/re-psn" -a ! -e "$bin"
EXTRA_ENV="RE_PSN_INSTALL_DIR=$T/home/envdir" run Linux x86_64 1000
check "RE_PSN_INSTALL_DIR: installed there" test "$rc" = 0 -a -x "$T/home/envdir/re-psn"

TEST_PATH="$B:$bin" run Linux x86_64 1000
check "PATH hint: none when the dir is on PATH" test "$rc" = 0
check "PATH hint: no export line" test "$(grep -c 'export PATH' "$T/out")" = 0

run Linux x86_64 0
check "root: refused" test "$rc" = 1
check "root: message" has "refusing to run as root"
check "root: nothing written" test ! -e "$F/calls" -a -z "$(ls -A "$T/home")"
run Linux x86_64 0 --allow-root
check "root with --allow-root: installs" test "$rc" = 0 -a -x "$bin/re-psn"

# An existing binary is replaced by a rename: new content, a new inode, no temporary file left.
rm -rf "$T/home2"; mkdir -p "$T/home2/bin"
printf 'old\n' > "$T/home2/bin/re-psn"
# shellcheck disable=SC2012 # ls -i is the portable way to read an inode number
old_inode="$(ls -i "$T/home2/bin/re-psn" | cut -d ' ' -f 1)"
run Linux x86_64 1000 --dir "$T/home2/bin"
# shellcheck disable=SC2012
new_inode="$(ls -i "$T/home2/bin/re-psn" | cut -d ' ' -f 1)"
check "existing binary: replaced" test "$rc" = 0 -a "$("$T/home2/bin/re-psn")" = "re-psn v0.4.0 amd64"
check "existing binary: renamed into place (new inode)" test "$old_inode" != "$new_inode"
check "existing binary: no temporary file left" test "$(ls -A "$T/home2/bin")" = re-psn
# The same release again, in the same HOME, leaves an edited examples directory as it is.
run Linux x86_64 1000
echo edited > "$T/home/.local/share/re-psn/v0.4.0/examples/re-psn.env.example"
KEEP_HOME=1 run Linux x86_64 1000
check "second install: examples kept as edited" \
  test "$rc" = 0 -a "$(cat "$T/home/.local/share/re-psn/v0.4.0/examples/re-psn.env.example")" = edited

run Linux x86_64 1000 --appimage
check "--appimage: installs re-psn.AppImage" test "$rc" = 0 -a "$(cat "$bin/re-psn.AppImage")" = "appimage v0.4.0"
check "--appimage: mode 0755" test -x "$bin/re-psn.AppImage"

# --app: the desktop app instead of the server. v0.8.0 is a release with the app archives.
make_release v0.8.0
run Linux x86_64 1000 --app --version v0.8.0
check "--app: installs re-psn-app, not the server" \
  test "$rc" = 0 -a "$("$bin/re-psn-app")" = "re-psn-app v0.8.0 amd64" -a ! -e "$bin/re-psn"
check "--app: mode 0755" test -n "$(find "$bin/re-psn-app" -perm 0755)"
check "--app: LICENSE, NOTICE.md and README.md copied" \
  test -f "$T/home/.local/share/re-psn-app/v0.8.0/README.md" -a -f "$T/home/.local/share/re-psn-app/v0.8.0/NOTICE.md"
check "--app: no server folder created" test ! -e "$T/home/.local/share/re-psn"
check "--app: only the app archive and SHA256SUMS fetched" test "$(cat "$F/calls")" = "https://github.com/undecuple/re-psn/releases/download/v0.8.0/SHA256SUMS
https://github.com/undecuple/re-psn/releases/download/v0.8.0/re-psn-app-v0.8.0-linux-amd64.tar.gz"
check "--app: says how to start it" has "Start the app:"
check "--app: no server hints" test "$(grep -c 'status page' "$T/out")" = 0
check "--app: temporary directory removed" empty "$T/tmp"
run Linux aarch64 1000 --app --version v0.8.0
check "--app on arm64: installs the arm64 app" test "$rc" = 0 -a "$("$bin/re-psn-app")" = "re-psn-app v0.8.0 arm64"
run Linux x86_64 1000 --app --version v0.4.0
check "--app on a release without app archives: refused, nothing installed" \
  test "$rc" != 0 -a -z "$(ls -A "$T/home")"
check "--app on an old release: says the app archives start after v0.4.0" has "start after v0.4.0"
run Linux x86_64 1000 --app --appimage
check "--app with --appimage: refused before any download" test "$rc" = 1 -a ! -e "$F/calls"
run Linux x86_64 1000 --app --dry-run --version v0.8.0
check "--app dry run: exits 0, no download, nothing written" test "$rc" = 0 -a ! -e "$F/calls"
check "--app dry run: nothing written" nothing_written
check "--app dry run: app asset URL" has "re-psn-app-v0.8.0-linux-amd64.tar.gz"
check "--app dry run: install path" has "$bin/re-psn-app"
run Linux aarch64 1000 --appimage
check "--appimage on arm64: refused" test "$rc" = 1 -a -z "$(ls -A "$T/home")"

# Other SHA-256 tools: shasum, then openssl, when sha256sum is missing.
for keep in shasum openssl; do
  [ -e "$B/$keep" ] || { echo "skip  $keep fallback ($keep not installed)"; continue; }
  rm -rf "$T/bin2"; mkdir "$T/bin2"
  for t in "$B"/*; do
    n="${t##*/}"
    case "$n" in sha256sum|shasum|openssl) [ "$n" = "$keep" ] || continue ;; esac
    ln -s "$(readlink "$t" 2>/dev/null || echo "$t")" "$T/bin2/$n"
  done
  TEST_PATH="$T/bin2" run Linux x86_64 1000
  check "$keep fallback: installs" test "$rc" = 0 -a -x "$bin/re-psn"
  TEST_PATH="$T/bin2" run Linux x86_64 1000 --version v0.5.0
  check "$keep fallback: mismatch refused" test "$rc" != 0 -a ! -e "$bin"
done

# No curl and no GNU wget: a clear refusal.
rm -rf "$T/bin3"; mkdir "$T/bin3"
for t in "$B"/*; do [ "${t##*/}" = curl ] || ln -s "$(readlink "$t" 2>/dev/null || echo "$t")" "$T/bin3/${t##*/}"; done
printf '#!/bin/sh\necho "BusyBox wget"\n' > "$T/bin3/wget"; chmod 0755 "$T/bin3/wget"
TEST_PATH="$T/bin3" run Linux x86_64 1000
check "no curl, BusyBox wget: refused" test "$rc" = 1
check "no curl, BusyBox wget: message" has "needs curl, or a GNU wget"

run Linux x86_64 1000 --version v1.2
check "bad --version: refused" test "$rc" = 1 -a ! -e "$F/calls"
run Linux x86_64 1000 --version 'v1.2.3;x'
check "odd --version: refused" test "$rc" = 1 -a ! -e "$F/calls"
run Linux x86_64 1000 --bogus
check "unknown option: refused" test "$rc" != 0
run Linux x86_64 1000 --help
check "--help: exits 0 and prints usage" test "$rc" = 0
check "--help: text" has "usage: sh install.sh"

printf '%s passed, %s failed (installer run with %s)\n' "$pass" "$fail" "${1:-sh}"
[ "$fail" = 0 ]
