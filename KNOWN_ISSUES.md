# Known issues

What is known not to work, or not to have been checked, in the newest release. Each line points to the page that says it in full;
the [features list](docs/features.md) gives every feature's status and evidence. Problems you find belong in an
[issue](https://github.com/undecuple/re-psn/issues/new/choose).

## Early software

- re-psn is an alpha pre-release. No shipped game is claimed to work with it yet: a game has to be recompiled to use the
  [client](client/psnr.c) first ([README](README.md#quick-start-host-a-server)).
- The server has no accounts. A name is not authenticated, and anyone who can reach TCP 36100 can join ([README](README.md#security-and-privacy)).

## Not run or not checked

- **Windows and arm64 release binaries** are cross-built and have not been run by us. The server's tests do run on Windows in CI
  ([README](README.md#quick-start-host-a-server)).
- **`install.ps1`** has been tested on PowerShell 7 only, not on Windows PowerShell 5.1 ([installing.md](docs/installing.md#windows)).
- **The AppImage** is x86_64 only and unsigned.
- **The desktop app:** the terminal interface has synthetic startup tests; physical desktops and emulators have not been
  checked. It has no Host screen, no room joining and no launch profiles yet ([app/README.md](app/README.md)).
- **Reverse-proxy examples** for nginx, HAProxy and Caddy, and the **fail2ban** jail, have not been run against a live
  setup ([running.md](docs/running.md#behind-a-reverse-proxy), [fail2ban.md](docs/fail2ban.md)).
- **The admin API** is experimental and read-only. It has not been run behind a reverse proxy, on Windows or with a container
  secret mount ([admin-api.md](docs/admin-api.md)).

## Dependency advisories

- **`glib` in the window program (Linux only).** GitHub's Dependabot reports [GHSA-wrw7-89jp-8q8g](https://github.com/advisories/GHSA-wrw7-89jp-8q8g)
  (medium, unsoundness in the `Iterator` implementations of `glib::VariantStrIter`) for `glib` 0.18.5 in `app/shell/Cargo.lock`. The
  advisory covers `glib` 0.15.0 up to but not including 0.20.0 and is fixed in 0.20.0; the window's Tauri 2 Linux dependencies
  (`gtk` 0.18, `webkit2gtk` 2.0) require the 0.18 line, so the fix waits on Tauri and no update is available to us. The window's own
  code (`app/shell/src`) has no reference to `glib` or `VariantStrIter` (checked by search and by reading the files); the crates it
  depends on were searched for the one `glib` function that returns that type (`Variant::array_iter_str`) and none calls it, but they
  have not been read in full. On Windows the dependency tree has no `glib` (`cargo tree --locked --target x86_64-pc-windows-msvc`),
  so the Windows window is not affected. The server, the terminal app and the browser interface are Go and do not use it.
  The advisory has not been dismissed on GitHub.

## Limits by design

- The relay is off by default (`-relay`). Without it, two players who cannot reach each other directly cannot play; they need a
  port forward or a shared VPN ([connecting.md](docs/connecting.md)).
- Peer keys (server side) travel over plain TCP, so they give no confidentiality, and no client uses them yet
  ([features list](docs/features.md)).
- The UDP guardrails are a first step: a packet-rate limiter that players behind one NAT share. Per-player relay budgets
  and UDP fuzz coverage are planned ([features list](docs/features.md)).
- The Go client package (`app/`) does not yet cover room data and messages, kicks, invite-only rooms, relay streams or UDP.

Newer than this file: check the [CHANGELOG](CHANGELOG.md) and the [releases page](https://github.com/undecuple/re-psn/releases).
