# Local loopback checks

Run a focused check of the native C client and Go app client against temporary servers built from this repository.
The traffic and test identities are synthetic. The command reports success only when all four named tests pass;
missing tests, skips, failed builds and timeouts fail the command.

## Contents

- [Run](#run)
- [What runs](#what-runs)
- [Isolation and cleanup](#isolation-and-cleanup)
- [Limits](#limits)
- [Optional fail2ban filter check](#optional-fail2ban-filter-check)

## Run

From a checkout with Go, Python 3 and a C compiler on PATH:

```sh
python3 tools/loopback.py
python3 tools/loopback.py --race
```

Use the Go version required by `app/go.mod` (currently 1.26). `nix develop` supplies Go and gcc on Linux. The C-client
test selects `cc`, `gcc` or `clang` from PATH. `--race` also needs cgo support; it is optional. The Go module's existing
dependencies may be downloaded if they are not already cached. No additional package is required by the runner.

`--timeout 180` sets the maximum seconds for each phase, including compilation. The default is 180; a slow first
build may need more time. The command can be invoked by its absolute path from another working directory.

## What runs

| Phase | Existing tests | Coverage |
|---|---|---|
| Native client | `TestCClient` | Builds/runs `client/test_psnr.c` and `client/examples/minimal.c` against the in-process TCP/UDP server, including example argument validation and empty/populated searches |
| Go app client | `TestConformanceRoomsAndScores`, `TestConformanceRelayRoute`, `TestConformanceErrors` | Builds the server, starts it on free loopback ports, and exercises two-client room/score operations, route notifications and protocol errors |

CI also requires `TestCheckReleaseAssets`, `TestReleaseAssetListsAgree` and `TestReleaseCheckWorkflow` to run on Linux, so a
missing `sh`, `awk` or `diff` fails the job instead of skipping them.

The runner uses `tools/check_conformance.py` to require the exact package/test names, disables Go's result cache with
`-count=1`, and forwards test output. The app module uses `-mod=readonly`. There is no option for an external target.
The route-notification test does not establish that relayed gameplay data works.

## Isolation and cleanup

The existing fixtures explicitly bind loopback: the C test uses `127.0.0.1`, and the app tests select free TCP/UDP
loopback ports and disable the HTTP status page. The runner removes `RE_PSN_*` and legacy `PSNR_*` settings from child
environments, so personal server settings are not applied. Test files and server binaries use temporary directories.

The fixtures stop their servers on normal completion. Each phase also has a process deadline: on POSIX the runner
creates its own process group and kills remaining group members on completion, timeout or interruption. On Windows it
uses a new process group and `taskkill /T /F` if the command is still running at cleanup. Normal test cleanup remains
necessary on Windows; cancellation behavior there has not been exercised locally.

## Limits

These checks do not run games, prove LAN/Internet reachability, exercise NAT traversal across routers, or establish
Medius or title compatibility. Use the full Go suites and CI for broader regression coverage, and the separate
[NAT lab](../lab/README.md) for its Docker scenarios. Runner checks were executed on Linux; Windows and macOS execution
still need validation.

## Optional fail2ban filter check

For the optional [status-token jail](fail2ban.md), run `python3 tools/check_fail2ban.py --out FRESH_DIRECTORY` with Go and fail2ban-regex
installed. This separately exercises synthetic HTTP/logger fixtures through the real fail2ban filter engine; its
required tools and fixture tests must execute. It makes no live journal, daemon or firewall changes. Normal CI runs
the Go fixture tests but does not install fail2ban. Run the optional checker again after changing the refusal logs,
filter or jail example, then verify the intended host's journal and firewall separately before enabling it.
