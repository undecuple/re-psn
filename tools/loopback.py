"""Run the native C and Go app client checks against temporary loopback servers."""

import argparse
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
SERVER = "github.com/sp00nznet/psnr/server"
CLIENT = "github.com/undecuple/re-psn/app/internal/client"
APP_TESTS = ("TestConformanceRoomsAndScores", "TestConformanceRelayRoute", "TestConformanceErrors",
             "TestConformanceHeartbeatReportsRTT")


def clean_env():
    return {key: value for key, value in os.environ.items()
            if not key.upper().startswith(("RE_PSN_", "PSNR_"))}


def run_command(label, command, cwd, timeout):
    print(f"loopback: {label}", flush=True)
    try:
        child = subprocess.Popen(command, cwd=cwd, env=clean_env(), stdin=subprocess.DEVNULL,
                                 start_new_session=(os.name == "posix"),
                                 creationflags=(subprocess.CREATE_NEW_PROCESS_GROUP if os.name == "nt" else 0))
    except OSError as exc:
        print(f"loopback: cannot start {label}: {exc}", file=sys.stderr)
        return 1
    try:
        return 0 if child.wait(timeout=timeout) == 0 else 1
    except subprocess.TimeoutExpired:
        print(f"loopback: {label} exceeded {timeout}s", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130
    finally:
        if os.name == "posix":
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        elif child.poll() is None:
            try:
                subprocess.run(["taskkill", "/PID", str(child.pid), "/T", "/F"],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
            except (OSError, subprocess.TimeoutExpired):
                pass
        if child.poll() is None:
            child.kill()
        child.wait()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--race", action="store_true", help="enable Go's race detector (needs cgo)")
    parser.add_argument("--timeout", type=int, default=180, help="maximum seconds per phase (default: 180)")
    args = parser.parse_args(argv)
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if shutil.which("go") is None:
        print("loopback: Go must be on PATH", file=sys.stderr)
        return 1
    wrapper = ROOT / "tools" / "check_conformance.py"
    if not wrapper.is_file():
        print("loopback: tools/check_conformance.py is required", file=sys.stderr)
        return 1
    go = ["go", "test", "-count=1", f"-timeout={args.timeout}s", "-json"]
    if args.race:
        go.append("-race")
    phases = [
        ("native C client", ROOT, [SERVER + ":TestCClient"],
         go + ["-run", "^TestCClient$", "./server"]),
        ("Go app client", ROOT / "app", [CLIENT + ":" + test for test in APP_TESTS],
         go + ["-mod=readonly", "-run", "^(" + "|".join(APP_TESTS) + ")$", "./internal/client"]),
    ]
    for label, cwd, required, command in phases:
        wrapped = [sys.executable, str(wrapper)]
        for test in required:
            wrapped.extend(["--require", test])
        status = run_command(label, wrapped + ["--"] + command, cwd, args.timeout)
        if status:
            return status
    print("loopback: all four required integration tests passed; synthetic loopback only")
    return 0


if __name__ == "__main__":
    sys.exit(main())
