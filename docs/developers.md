# Developer guide

How to set up a checkout, find your way around it, and send a change. It is the long form of
[CONTRIBUTING.md](../CONTRIBUTING.md), which has the ground rules and wins if the two differ. To ask for a game title to be
checked, see [Requesting a title](title-requests.md).

## Contents

- [Set up in 10 minutes](#set-up-in-10-minutes)
- [Repository map](#repository-map)
- [Add a feature, start to end](#add-a-feature-start-to-end)
- [Generated files](#generated-files)
- [Tests to run](#tests-to-run)
- [A good pull request](#a-good-pull-request)
- [Good first changes](#good-first-changes)
- [What is refused](#what-is-refused)

## Set up in 10 minutes

You need Git, Go, Python 3 and a C compiler. The tools declare no Python minimum; use a current 3.x. The server module
needs Go 1.22 or newer. `app/` is a separate module that needs Go 1.26 (see `app/go.mod`), newer than many distro
packages; Go can download that toolchain, or install it yourself. On Linux, `nix develop` gives a shell with Go, gcc and make; otherwise install them yourself.

```sh
git clone https://github.com/undecuple/re-psn.git
cd re-psn
nix develop            # optional: Go, gcc and make
go test ./server       # the first run compiles everything and takes a few minutes
python3 tools/repo_check.py
```

To send a change: fork the repository on GitHub, clone your fork, create a branch from `main`, commit there, push the
branch to your fork and open a pull request against `main` using the template. The server has no dependency outside the standard library, so there
is nothing to download for it. The desktop app in `app/` has a few; `go test` fetches them on first use.

## Repository map

| Path | What is there |
|---|---|
| `server/` | The Go server (`package main`): protocol, rooms, scores, relay, status page. `server/store/` holds storage backends |
| `client/` | The C client (`psnr.c`, `psnr.h`), its tests and `examples/minimal.c` |
| `app/` | The desktop app, a Go module of its own |
| `docs/` | Documentation. `features.tsv` is the data behind `features.md` |
| `tools/` | Checks and generators, each with a test: `repo_check.py`, `features.py`, `site.py`, `badges.py`, `loopback.py`, `art/` |
| `site/` | The project website; `index.html` and `data.json` are generated |
| `deploy/`, `docker/`, `lab/` | Example settings and proxy configs; container files; the NAT lab |
| `scripts/` | Install, release and deploy-check scripts |

[Architecture](architecture.md) shows how the parts fit. [Protocol](api.md) lists every message.

## Add a feature, start to end

1. **Say what it is.** Open a feature request, or find the row in [ROADMAP.md](../ROADMAP.md). Ask first if the change adds a flag, a message or a dependency, or touches more than one area; the answer comes on that issue. Small fixes need no ask.
2. **Write the server code** in `server/` and a test beside it. The test goes in the same commit.
3. **If the wire format changes,** bump the protocol version and update [api.md](api.md) in the same commit. Then update the C client (`client/`) and the Go client in `app/` so they still match.
4. **If you add a flag,** add its `RE_PSN_` variable, a line in `deploy/re-psn.env.example`, a row in [running.md](running.md) and a row in the README settings table. Never log a setting's value.
5. **Record the status.** Add or edit the row in `docs/features.tsv`. A row may say `available` only with evidence: a path, a test name or a pull request. Tag the item in `ROADMAP.md`.
6. **Regenerate** the generated files (next section) and commit them with the change.
7. **Add a line** under Unreleased in [CHANGELOG.md](../CHANGELOG.md).
8. **Run the tests** below, then open the pull request.

## Generated files

Edit the source, run the generator, commit both. Each generator has a `--check` mode that writes nothing and exits 1 on a
difference; CI runs it.

| Source | Generator | Output |
|---|---|---|
| `docs/features.tsv` (and `ROADMAP.md` tags) | `python3 tools/features.py` | `docs/features.md` |
| `docs/features.tsv` | `python3 tools/badges.py` | `docs/img/badges/*.svg`, the README badges |
| features, `VERSION`, the README settings table, `CHANGELOG.md`, `go.mod` | `python3 tools/site.py` | `site/index.html`, `site/data.json` |
| palette, transcripts, features | `python3 tools/art/make_art.py` | `docs/img/*.png`, `tools/art/art.json` |

Do not edit an output by hand. `site/style.css` and `site/terminal.js` are written by hand. The art script needs Pillow
and a pinned font that `--fetch-fonts` downloads, so run it only when you change the art inputs; CI checks the rest.
Stage new files before running `repo_check.py`: it reads the list of tracked files and checks local links, and every page
in `docs/` must be linked from [docs/README.md](README.md).

## Tests to run

```sh
gofmt -l server                  # prints nothing when formatted
go vet ./...
go test -race ./...
python3 tools/repo_check.py
python3 tools/features.py --check && python3 tools/badges.py --check && python3 tools/site.py --check
python3 -m unittest
```

CI runs these too, plus `tools/check_conformance.py`, `govulncheck`, a secret scan and the NAT lab; [CONTRIBUTING.md](../CONTRIBUTING.md#testing) says how to run the conformance wrapper. Also run `sh scripts/check-deploy.sh` when `deploy/` or a flag changed, and the `app/` checks (`gofmt`, `go vet`,
`go test -race` inside `app/`) when `app/` changed. `python3 tools/loopback.py` runs the C client and the Go app client
against temporary loopback servers ([testing.md](testing.md)). The NAT lab needs Docker. Say in the pull request which of
these you did not run.

## A good pull request

- One focused change, with its tests and docs in the same commit.
- Fill in the [pull request template](../.github/pull_request_template.md): Why, Change, Tests, Evidence, Overlap.
- Plain subject line, then a short body saying what and why.
- Say what you ran and what you did not. A test you could not run is fine to name; a claim without a run is not.
- If an AI coding agent wrote part of it, say so. You remain responsible for it.

## Good first changes

- A missing or unclear sentence in a doc, checked against the code.
- A test for a path that has none, such as an error reply in `server/`.
- A [title table row](title-requests.md#what-the-maintainers-do) for a game you own. Send only the ID, the serials and where you read them, never the files.
- A known issue in [KNOWN_ISSUES.md](../KNOWN_ISSUES.md) that you can reproduce and narrow down.
- A check in `tools/` that fails on a mistake the docs already warn about.

## What is refused

- Game material: disc or update files, decrypted or lifted game code, extracted assets, saves, keys or firmware, and any link to them.
- Secrets or private logs: user names, paths, tokens or addresses in anything you paste.
- A claim that a title works without a run that shows it. See [what tested means](title-requests.md#what-tested-means).
- A new dependency in the server module, or one added in a change that does something else.
- Renames of the names that stay (see [AGENTS.md](../AGENTS.md)): the Go module path, the `psnr_*` C functions and the UDP magic.
- A wire-format change without the version bump and the `api.md` update.
- Anything that reaches a service you do not run yourself. There is no public instance and there are no accounts.
- Security problems as issues or pull requests: use [SECURITY.md](../SECURITY.md).
