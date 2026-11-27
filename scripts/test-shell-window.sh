#!/bin/sh
# Window behaviour of the desktop shell, on a virtual X display with a window manager. Run it under xvfb-run, with
# xdotool, wmctrl and openbox installed:
#   xvfb-run -a sh scripts/test-shell-window.sh DIR
# DIR holds the built re-psn-shell and re-psn-app (it adds a copy named re-psn-window). It checks that the shell opens one window onto one app, that a
# second shell shares that app and closing it leaves the app running, that closing the first window stops the app
# and the shell, that an app that stops on its own closes its shell, and that re-psn-app with no arguments opens the window program beside it. It uses a private HOME and never touches
# the real settings.
set -eu

dir="${1:?usage: test-shell-window.sh DIR}"
dir="$(cd "$dir" && pwd)"
for tool in xdotool wmctrl openbox pgrep; do
	command -v "$tool" >/dev/null 2>&1 || { echo "missing tool: $tool" >&2; exit 2; }
done
[ -x "$dir/re-psn-shell" ] && [ -x "$dir/re-psn-app" ] || { echo "re-psn-shell and re-psn-app must be in $dir" >&2; exit 2; }

work="$(mktemp -d)"
chmod 700 "$work"
HOME="$work/home"
XDG_CONFIG_HOME="$HOME/.config"
mkdir -p "$XDG_CONFIG_HOME"
chmod 700 "$HOME" "$XDG_CONFIG_HOME"
export HOME XDG_CONFIG_HOME GDK_BACKEND=x11 WEBKIT_DISABLE_DMABUF_RENDERER=1
unset WAYLAND_DISPLAY

pids=""
cleanup() {
	# shellcheck disable=SC2086
	[ -z "$pids" ] || kill $pids 2>/dev/null || true
	pkill -f "$dir/re-psn-app ui" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

fail() { echo "FAIL: $*" >&2; exit 1; }
apps() { pgrep -f "$dir/re-psn-app ui" | wc -l | tr -d ' '; }
windows() { xdotool search --name 're:psn' 2>/dev/null | wc -l | tr -d ' '; }
alive() { kill -0 "$1" 2>/dev/null; }
# wait_for DESCRIPTION COMMAND...: poll for up to 20 seconds
wait_for() {
	desc="$1"
	shift
	n=0
	while ! "$@"; do
		n=$((n + 1))
		[ "$n" -le 100 ] || fail "timed out waiting for $desc"
		sleep 0.2
	done
}
# shell_window PID: the id of a re:psn window that process PID owns (the last one listed if it has several), empty if none yet
shell_window() {
	for id in $(xdotool search --pid "$1" 2>/dev/null); do
		case "$(xdotool getwindowname "$id" 2>/dev/null)" in *re:psn*) echo "$id" ;; esac
	done | tail -1
}
has_shell_window() { [ -n "$(shell_window "$1")" ]; }
has_window() { [ "$(windows)" -ge "$1" ]; }
two_windows() { has_window 2; }
one_window() { has_window 1; }
no_app() { [ "$(apps)" -eq 0 ]; }
one_app() { [ "$(apps)" -eq 1 ]; }
shell_gone() { ! alive "$1"; }

openbox >/dev/null 2>&1 &
pids="$!"
sleep 1

echo "1. one shell: one app, one window"
"$dir/re-psn-shell" 2>/dev/null &
s1=$!
pids="$pids $s1"
wait_for "the first window" one_window
[ "$(apps)" -eq 1 ] || fail "expected one app, found $(apps)"

echo "2. second shell shares the app"
"$dir/re-psn-shell" 2>/dev/null &
s2=$!
pids="$pids $s2"
wait_for "the second window" two_windows
[ "$(apps)" -eq 1 ] || fail "second shell started another app ($(apps))"

echo "3. closing the second window leaves the app and the first shell"
# The window of the second shell itself: the last window the name search lists is not always the second shell's.
wait_for "the second shell's own window" has_shell_window "$s2"
w2="$(shell_window "$s2")"
wmctrl -i -c "$(printf '0x%08x' "$w2")"
wait_for "the second shell to end" shell_gone "$s2"
sleep 1
[ "$(apps)" -eq 1 ] || fail "closing the second window stopped the app"
alive "$s1" || fail "closing the second window ended the first shell"

echo "4. closing the first window stops the app and the shell"
wmctrl -c 're:psn'
wait_for "the app to stop" no_app
wait_for "the first shell to end" shell_gone "$s1"

echo "5. an app that stops on its own closes its shell"
"$dir/re-psn-shell" 2>/dev/null &
s3=$!
pids="$pids $s3"
wait_for "the app" one_app
wait_for "the window" one_window
pkill -TERM -f "$dir/re-psn-app ui"
wait_for "the shell to end" shell_gone "$s3"

echo "6. re-psn-app with no arguments opens the window program beside it, and closing the window ends both"
# re-psn-app only hands over to a regular file named re-psn-window in its own folder.
cp "$dir/re-psn-shell" "$dir/re-psn-window"
"$dir/re-psn-app" >/dev/null 2>&1 &
a1=$!
pids="$pids $a1"
wait_for "the window" one_window
[ "$(apps)" -eq 1 ] || fail "expected one app, found $(apps)"
wmctrl -c 're:psn'
wait_for "the app to stop" no_app
wait_for "re-psn-app to end" shell_gone "$a1"

echo "ok"
