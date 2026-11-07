"""Fail-closed checker tests; actual regex-engine coverage is check_fail2ban.py."""
import json
import contextlib
import io
from pathlib import Path
import sys
import tempfile
import unittest

from tools import check_fail2ban as check


class Fail2banCheckerTests(unittest.TestCase):
    def test_missing_skipped_or_failed_required_go_test_is_rejected(self):
        base = {"Package": check.PACKAGE, "Test": check.TEST}
        check.require_fixture_pass(json.dumps(dict(base, Action="pass")))
        for events in ([], [dict(base, Action="skip")], [dict(base, Action="fail")],
                       [dict(base, Test="DifferentTest", Action="pass")],
                       [dict(base, Action="pass"), dict(base, Action="skip")]):
            with self.subTest(events=events), self.assertRaises(ValueError):
                check.require_fixture_pass("\n".join(map(json.dumps, events)))

    @staticmethod
    def dump(action=None, backend="systemd"):
        commands = [["add", "re-psn-status", backend]]
        for suffix in (["usedns", "no"], ["addjournalmatch", "_SYSTEMD_UNIT=re-psn.service"],
                       ["maxretry", 10], ["findtime", "10m"], ["bantime", "10m"],
                       ["addignoreip", "127.0.0.1/8", "::1"], ["datepattern", "{NONE}"]):
            commands.append(["set", "re-psn-status", *suffix])
        values = {"type": "multiport", "port": "36101", "protocol": "tcp", "name": "re-psn-status",
                  "chain_hook": "input", "actionstart": "nft add rule tcp dport 36101"}
        values.update(action or {})
        commands.append(["multi-set", "re-psn-status", "action", "nftables", list(values.items())])
        return "\n".join(map(repr, commands))

    def test_shipped_jail_cannot_be_enabled_and_dump_cannot_select_another_backend(self):
        self.assertEqual(check.validate_dump("['set', 'loglevel', 'INFO']", False), {})
        with self.assertRaisesRegex(ValueError, "remain disabled"):
            check.validate_dump(self.dump(), False)
        with self.assertRaisesRegex(ValueError, "backend"):
            check.validate_dump(self.dump(backend="polling"), True)

    def test_native_resolved_action_must_select_status_port_and_protocol(self):
        self.assertEqual(check.validate_dump(self.dump(), True)["port"], "36101")
        for change in ({"port": "ssh"}, {"port": "36100"}, {"protocol": "udp"}, {"type": "allports"},
                       {"chain_hook": "forward"}, {"actionstart": "nft add rule udp dport 36100"}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                check.validate_dump(self.dump(change), True)

    def test_logged_commands_fail_and_keep_output_instead_of_silently_passing(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            with self.assertRaisesRegex(ValueError, "failed"):
                check.run_logged([sys.executable, "-c", "print('fixture'); raise SystemExit(2)"], "failure", out, 5)
            self.assertEqual((out / "failure.stdout").read_text(), "fixture\n")

    def test_output_directory_cannot_replace_or_pollute_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for out in (root, root / "new-output"):
                with self.subTest(out=out), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    check.main(["--root", str(root), "--out", str(out)])


if __name__ == "__main__":
    unittest.main()
