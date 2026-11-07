"""Check real HTTP log fixtures with Fail2ban; dump an isolated jail without starting it."""
import argparse
import ast
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
TEST = "TestHTTPStatusFail2banFixtures"
PACKAGE = "github.com/sp00nznet/psnr/server"
SOURCE_FILES = ("deploy/fail2ban/re-psn-status.conf", "deploy/fail2ban/re-psn-status.local",
                "server/fail2ban_test.go", "server/api.go", "server/logging.go", "server/proxy.go",
                "tools/check_fail2ban.py")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def hashes(root):
    return {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in SOURCE_FILES}


def run_logged(argv, name, out, timeout, cwd=None, env=None):
    """Keep evidence and bound each owned command/process group."""
    stdout, stderr = out / (name + ".stdout"), out / (name + ".stderr")
    with stdout.open("wb") as output, stderr.open("wb") as errors:
        child = subprocess.Popen(list(map(str, argv)), cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                                 stdout=output, stderr=errors, start_new_session=True)
        try:
            status = child.wait(timeout=timeout)
        finally:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            child.wait()
    require(status == 0, f"{name} failed; see {stdout.name} and {stderr.name}")
    return stdout.read_text(encoding="utf-8")


def require_fixture_pass(stream):
    passed = False
    for line in stream.splitlines():
        event = json.loads(line)
        require(isinstance(event, dict) and isinstance(event.get("Action"), str), "invalid Go test output")
        if event.get("Package") == PACKAGE and event.get("Test") == TEST:
            require(event["Action"] not in ("skip", "fail"), "required HTTP fixture test did not pass")
            passed = passed or event["Action"] == "pass"
    require(passed, "required HTTP fixture test was not executed successfully")


def validate_dump(stream, enabled):
    """Inspect native client's parsed commands; never execute the dumped actions."""
    commands = [ast.literal_eval(line) for line in stream.splitlines() if line.strip()]
    require(all(isinstance(row, list) for row in commands), "unexpected native configuration dump")
    additions = [row for row in commands if row[:1] == ["add"]]
    if not enabled:
        require(not additions, "shipped jail must remain disabled")
        return {}
    require(additions == [["add", "re-psn-status", "systemd"]], "isolated dump enabled an unexpected jail/backend")
    for suffix in (["usedns", "no"], ["addjournalmatch", "_SYSTEMD_UNIT=re-psn.service"],
                   ["maxretry", 10], ["findtime", "10m"], ["bantime", "10m"],
                   ["addignoreip", "127.0.0.1/8", "::1"], ["datepattern", "{NONE}"]):
        require(["set", "re-psn-status", *suffix] in commands, "jail policy differs from the checked example")
    actions = [row for row in commands if row[:4] == ["multi-set", "re-psn-status", "action", "nftables"]]
    require(len(actions) == 1 and len(actions[0]) == 5, "missing or ambiguous native nftables action")
    action = dict(actions[0][4])
    require(all(action.get(key) == value for key, value in
                {"type": "multiport", "port": "36101", "protocol": "tcp", "name": "re-psn-status",
                 "chain_hook": "input"}.items()), "resolved action is not limited to direct-host TCP 36101")
    start = action.get("actionstart", "")
    require("36101" in start and "tcp" in start and "36100" not in start and "udp" not in start,
            "native action command does not preserve status-only scope")
    return {key: action[key] for key in ("type", "port", "protocol", "name", "chain_hook")}


def adversarial_cases(line):
    """Mutate a real JSON record; expected addresses are checked by the native engine."""
    record = json.loads(line)
    rows = []
    def add(name, changes):
        rows.append({"name": name, "line": json.dumps(dict(record, **changes), separators=(",", ":")), "ip": ""})
    for level in ("INFO", "DEBUG", "ERROR", "warn"):
        add("level-" + level, {"level": level})
    add("other-event", {"msg": "hello"})
    add("other-reason", {"reason": "rate limit"})
    add("dns-hostname", {"addr": "attacker.example"})
    add("empty-real-address-forged-path", {"addr": "", "path": line.replace(record["addr"], "203.0.113.99")})
    add("forged-event-in-path", {"msg": "hello", "path": line.replace(record["addr"], "203.0.113.99")})
    add("extra-field", {"extra": "203.0.113.99"})
    for name, value in (("arbitrary-prefix", "untrusted text " + line),
                        ("other-service", "host other-service[1]: " + line),
                        ("trailing-json", line + '{"addr":"203.0.113.99"}'),
                        ("bad-json-escape", line.replace('"path":"/api/stats"', '"path":"\\q"'))):
        rows.append({"name": name, "line": value, "ip": ""})
    return rows


def check(args, out):
    root = args.root.resolve()
    source = hashes(root)
    regex = shutil.which(args.fail2ban_regex)
    require(regex is not None, "fail2ban-regex executable is required")
    client = args.fail2ban_client or str(Path(regex).with_name("fail2ban-client"))
    require(shutil.which(client) is not None, "fail2ban-client executable is required for offline configuration dumps")
    configdir = args.fail2ban_configdir
    if configdir is None:
        bundled = Path(regex).parent.parent / "etc/fail2ban"
        configdir = bundled if bundled.is_dir() else Path("/etc/fail2ban")
    configdir = configdir.resolve()
    version = run_logged([regex, "-V"], "version", out, args.timeout).strip()
    fixtures_path = out / "http-fixtures.json"
    env = {key: value for key, value in os.environ.items()
           if not key.upper().startswith(("RE_PSN_", "PSNR_"))}
    env["RE_PSN_FAIL2BAN_FIXTURES"] = str(fixtures_path)
    go = [args.go, "test", "-count=1", f"-timeout={args.timeout}s", "-json", "-run", "^" + TEST + "$", "./server"]
    if args.race:
        go.insert(2, "-race")
    require_fixture_pass(run_logged(go, "go-fixtures", out, args.timeout, cwd=root, env=env))
    require(not fixtures_path.is_symlink() and fixtures_path.stat().st_mode & 0o777 == 0o600,
            "fixture file must be a fresh private regular file")
    cases = json.loads(fixtures_path.read_text())
    require(isinstance(cases, list) and cases, "fixture list is empty")
    require(all(isinstance(row, dict) and all(isinstance(row.get(key), str) for key in ("name", "line", "ip"))
                and "\n" not in row["line"] and "\r" not in row["line"] for row in cases), "invalid HTTP fixture records")
    require(len({row["name"] for row in cases}) == len(cases), "duplicate HTTP fixture names")
    example = next(row["line"] for row in cases if row["name"] == "missing-v4")
    cases.extend(adversarial_cases(example))
    # Build a minimal owned configuration, not a copy of an operator's jails,
    # .local overrides or global settings. Only the packaged action is read.
    config = out / "config"
    config.mkdir()
    (config / "filter.d").mkdir()
    (config / "action.d").mkdir()
    shutil.copyfile(root / "deploy/fail2ban/re-psn-status.conf", config / "filter.d/re-psn-status.conf")
    action_file = configdir / "action.d/nftables.conf"
    shutil.copyfile(action_file, config / "action.d/nftables.conf")
    (config / "fail2ban.conf").write_text("[Definition]\nlogtarget = STDERR\ndbfile = None\n"
                                         f"socket = {config / 'unused.sock'}\npidfile = {config / 'unused.pid'}\n")
    (config / "jail.conf").write_text("[DEFAULT]\n")
    prefixes = ("", "re-psn: ", "re-psn[42]: ", "relay-host re-psn[42]: ", "relay-host re-psn: ")
    results = []
    for index, row in enumerate(cases):
        for variant, prefix in enumerate(prefixes):
            name = f"case-{index:02d}-{variant}"
            log = out / (name + ".log")
            log.write_text(prefix + row["line"] + "\n" if row["line"] else "")
            output = run_logged([regex, "--usedns=no", "--config", config, "-o", "ip", log,
                                 root / "deploy/fail2ban/re-psn-status.conf"], name, out, args.timeout)
            actual = [str(ipaddress.ip_address(value)) for value in output.splitlines()]
            expected = [str(ipaddress.ip_address(row["ip"]))] if row["ip"] else []
            require(actual == expected, f"native engine result differs for {row['name']} prefix {variant}: {actual}")
            results.append({"name": row["name"], "prefix": prefix, "expected": expected, "actual": actual,
                            "log": log.name})
    # No socket, daemon or firewall command is opened or executed by -d.
    jail = (root / "deploy/fail2ban/re-psn-status.local").read_text()
    require(jail.count("enabled = false") == 1, "disabled example setting changed")
    (config / "jail.local").write_text(jail)
    validate_dump(run_logged([client, "-c", config, "-d"], "jail-disabled", out, args.timeout), False)
    (config / "jail.local").write_text(jail.replace("enabled = false", "enabled = true", 1))
    action = validate_dump(run_logged([client, "-c", config, "-d"], "jail-enabled-dump", out, args.timeout), True)
    require(hashes(root) == source, "source files changed during validation")
    return {"status": "ok", "fail2ban_version": version, "source_sha256": source,
            "executables": {"go": shutil.which(args.go), "fail2ban_regex": regex, "fail2ban_client": client},
            "packaged_action_sha256": hashlib.sha256(action_file.read_bytes()).hexdigest(),
            "race": args.race,
            "required_test": PACKAGE + ":" + TEST, "cases": results, "native_action": action,
            "live_journal_or_firewall_exercised": False}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--out", type=Path, required=True, help="fresh directory outside the source checkout")
    parser.add_argument("--fail2ban-regex", default="fail2ban-regex")
    parser.add_argument("--fail2ban-client", help="defaults to fail2ban-client beside fail2ban-regex")
    parser.add_argument("--fail2ban-configdir", type=Path, help="directory containing packaged action.d/nftables.conf")
    parser.add_argument("--go", default="go")
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--race", action="store_true")
    args = parser.parse_args(argv)
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if os.name != "posix":
        parser.error("this Fail2ban checker requires a POSIX host")
    out = args.out.absolute()
    if out.resolve().is_relative_to(args.root.resolve()) or out.exists():
        parser.error("--out must be fresh and outside the source checkout")
    out.mkdir(parents=True, mode=0o700)
    try:
        result = check(args, out)
    except (ValueError, OSError, subprocess.SubprocessError, StopIteration) as exc:
        result = {"status": "error", "error": str(exc)}
    (out / "report.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    if result["status"] != "ok":
        print("fail2ban: " + result["error"], file=sys.stderr)
        return 1
    print(f"fail2ban: required HTTP fixtures, {len(result['cases'])} native regex cases and offline jail dumps passed")
    print("fail2ban: no live journal, daemon or firewall was exercised")
    return 0


if __name__ == "__main__":
    sys.exit(main())
