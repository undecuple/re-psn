# Contributing

How to report a problem or send a change to re-psn: the ground rules, what a pull request should hold, and which tests to run.
re-psn began as a fork of [sp00nznet/psnr](https://github.com/sp00nznet/psnr), by sp00nz, and is now a repository of its own. Bug reports, fixes and ideas are welcome here, through
the issue forms (bug report, feature request) or a pull request. Something that is also wrong in upstream may be worth
reporting there too.

## Ground rules

- **No game material, ever:** no disc or update files, decrypted or lifted game code, extracted assets, saves, keys or
  firmware, and no links to them. The server never carries game data; room blobs are capped at 4096 bytes.
- **No secrets or private logs:** remove user names and paths from anything you paste.
> [!IMPORTANT]
> Security problems: see [SECURITY.md](SECURITY.md), not an issue.

## Making a change

- One focused change per pull request, with tests in the same commit. Say what you ran and what you did not.
- A change to the wire format bumps the protocol version and updates [docs/api.md](docs/api.md) in the same commit.
- Commits: a plain subject line, then a short body saying what and why.
- The [pull request template](.github/pull_request_template.md) asks for Why, Change, Tests, Evidence and Overlap.
- Using an AI coding agent is fine; you remain responsible for what it submits, and the pull request should say so. [AGENTS.md](AGENTS.md) has the commands and rules for agents.

## Testing

What CI runs, and what to run first:

```sh
gofmt -l server
go vet ./...
go test -race ./...
python3 tools/repo_check.py
```

The repository check is offline and uses Python's standard library plus Git. It checks tracked root filenames and
top-level directories against `tools/repo_layout.json`, local Markdown links and heading/explicit anchors, and static
repository input paths in workflows and Dependabot. Stage new source files before checking so they appear in the
tracked inventory. Update the layout allowlist deliberately when adding a root file or top-level directory.

It does not fetch external links or interpret arbitrary shell commands or GitHub expressions. Common artifact
directories such as `dist/` are outside the static input scan. A standalone `python -m venv PATH` declaration also
excludes that generated subtree from run references in the same job, unless it overlaps tracked files; undeclared
paths under source directories still require tracked targets. The checker and its tests document the supported
Markdown/config forms; it is not a general YAML validator.
Run it from any directory with `python3 /path/to/checkout/tools/repo_check.py`, or pass `--root /path/to/checkout`.

On Linux the tests also build `client/test_psnr.c` and run it against the server (they skip without a C compiler).
`nix develop` gives a shell with Go and gcc. CI also runs `govulncheck`, a secret scan and the NAT lab (`sh lab/run.sh`, needs Docker).

For a focused native C-client and Go app-client check against temporary loopback servers, run
`python3 tools/loopback.py` (optionally `--race`). It fails if a required test skips and cleans up its owned phase
processes. See [docs/testing.md](docs/testing.md) for prerequisites, deadlines and coverage limits.

CI uses `tools/check_conformance.py` with Go's JSON test output to require the native C-client test on Linux and the three
app client conformance tests on Linux and Windows to pass. A missing, skipped or failed required test fails that job,
even if the Go command otherwise succeeds. Unrelated platform-specific skips remain allowed. Local `go test` commands
keep their existing skip behavior when optional tooling or server source is unavailable. The wrapper forwards test
output and also fails if the Go command fails or its JSON output is invalid; run it from the same directory as `go test`:

```sh
python3 tools/check_conformance.py --require github.com/sp00nznet/psnr/server:TestCClient -- go test -race -json ./...
```
