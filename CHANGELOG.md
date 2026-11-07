# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- Optional fail2ban instructions and a disabled journal jail/filter for refused HTTP status tokens, with offline
  synthetic log checks. Live host bans are unverified; the example leaves game TCP/UDP outside its scope.
- A recomp developer integration guide with pinned runtime sources, callback/lifecycle and peer-transport guidance.
  A minimal C HELLO/search example runs in the required native-client check; C API comments now clarify blocking
  writes, diagnostic state and reply-wait timeout limits without changing implementation or the wire protocol.
- Native optional HTTP status in the shared Windows/Linux terminal app: server totals, public-room members/age and
  leaderboard summaries, using a local Bearer-token file. Requests and displayed data are bounded, redirects/proxies
  are disabled and failures fall back to the game protocol. Initial refresh and changed-source sequencing are corrected.
- Linux desktop startup for the shared terminal app: open an installed terminal when no TTY is attached, with literal
  arguments and a recursion guard. A desktop entry and the site's favicon artwork are provided as installation inputs;
  no release assets are added, and the Windows build retains its normal console behavior.
- Bounded per-source-IP UDP admission with `-max-udp-rate` / `RE_PSN_MAX_UDP_RATE` (default 1000 packets/second,
  same-size burst; 0 disables it), shared by probes and relay datagrams. Aggregate `udp_drops` counters in the
  opt-in `/api/stats` identify refusal reasons without per-packet logging or source labels. Relay byte/stream budgets
  remain planned; the packet format and token checks are unchanged.
- `/healthz` on the opt-in status listener, protected by the existing HTTP token. It reports game TCP/UDP readiness
  without player data. Shutdown marks the server unready and closes accepted client/relay sockets with bounded cleanup;
  restart behavior and supervisor settings are documented in `docs/restarts.md`.
- A stock Caddy 2 example for the HTTP status page, included with the deployment examples. It defaults to loopback;
  game TCP and UDP stay direct. The example has not been run or validated with Caddy.
- The opt-in server status page now matches the website and terminal app's gruvbox-material palette and monospace
  framing, with Overview/Rooms/Leaderboards tabs, System/Light/Dark, text size, spacing and contrast-checked custom colours.
  Appearance is local to each browser. Live refresh can be paused and retains the last snapshot on failure; without
  JavaScript both tables remain visible and reload as before. The status token and `/api/stats` fields are unchanged.
  Embedded assets normalize line endings before rendering and CSP hashing. Polling preserves full 64-bit score and
  identifier values without changing their JSON types on the server.
- An offline repository check for tracked root/top-level layout, local Markdown links and anchors, and static
  workflow/Dependabot input paths. Run `python3 tools/repo_check.py`; CI runs it with the documentation checks.
- A focused loopback check: `python3 tools/loopback.py` runs the existing native C-client and Go app-client conformance
  tests against temporary loopback servers. Required skips and timeouts fail; `--race` is optional. See `docs/testing.md`.
- Numbered file-style output in the website's terminal view: a header, frame, line-number gutter, syntax-style colours
  and wrapped continuation rows with an empty gutter. Headers and numbers are excluded from text selection. A pager
  trial shows long output by page, with a line-range status, keyboard shortcuts and buttons; `pager off` shows all lines.
  Command typing stays independent of pager shortcuts, and pages remeasure after viewport and theme changes. See
  `docs/website.md` for controls and browser-test instructions.
- Optional install scripts: `scripts/install.sh` (POSIX sh; Linux amd64 and arm64) and `scripts/install.ps1` (Windows amd64;
  written for PowerShell 5.1 and 7, tested on 7 only) install the server for the current user from a GitHub release, the newest one (pre-releases included)
  or a pinned `--version` / `-Version`. They check the download against `SHA256SUMS` and stop before installing anything on a
  mismatch or a missing line, need no sudo or administrator rights, edit no shell profile, and change the user PATH only with
  `-AddToPath` on Windows. macOS is refused (there are no macOS builds). `--dry-run` / `-DryRun` downloads nothing. Tested
  with `scripts/test-install.sh` and `scripts/test-install.ps1` against a fake release. See `docs/installing.md`.

### Changed
- The release carries seven assets: the three archives, the AppImage, `SHA256SUMS`, and the two install scripts as
  `install.sh` and `install.ps1`, byte for byte the tagged `scripts/` files, with their checksums in `SHA256SUMS`. Check one
  with `sha256sum -c SHA256SUMS --ignore-missing` before reading and running it. `scripts/check-release.sh --assets` checks a
  build holds exactly the seven, and `release-check` compares the published scripts with the tag.

## [0.3.0] - 2026-11-06

### Changed
- The website starts in the plain view on a phone (narrow screens); the `terminal view` button switches the interface on, and nothing is stored.
- The website's interface on a phone: the version no longer wraps one letter per line in the title bar, and the menu is a row of tabs that scrolls sideways with the page content shown under it, instead of a full-screen list that hid the content.
- Credits: README, NOTICE.md and the website name undecuple as the maintainer of re-psn and author of the changes since the fork, and LICENSE
  keeps upstream's copyright line with a second line for the fork's changes.
- The status page and `/api/stats` are off unless `-http` / `RE_PSN_HTTP` names an address; they used to listen on
  `127.0.0.1:36101` by default. To keep the old behaviour, set `RE_PSN_HTTP=127.0.0.1:36101` (or `-http 127.0.0.1:36101`).
  `-http-token` and the warning for a page reachable beyond localhost without a token are unchanged.
- The release carries one `SHA256SUMS` instead of a `.sha256` file per download: five assets instead of nine.
  Check a download with `sha256sum -c SHA256SUMS --ignore-missing`.
- A shorter README: its long reference parts fold into dropdowns, and a short list linking to `docs/features.md` replaces the features table.
- The container image moves from Alpine 3.20 (end of life) to Alpine 3.24 and Go 1.26, both bases pinned by digest, and the server
  runs as the non-root user 10001. Tested with Podman and in the NAT lab on CI.

### Added
- `re-psn-app` starts a terminal UI when run with no arguments, built on Bubble Tea v2.1.1 and Lip Gloss v2.0.6 (MIT; the app module's
  first dependencies, pinned in `app/go.mod` and `app/go.sum`; the server module still has none). On first start it asks whether
  this machine plays, hosts or both, then for the online ID and the server, and saves `re-psn.ini`. The home screen has a menu
  (Overview, Rooms, Leaderboards, Settings, Help) beside the content, or one at a time under 70 columns. Rooms and Leaderboards
  are the tabs of a Status screen filled over the game protocol only (HELLO, SEARCH_ROOMS, GET_RANKING for a typed board id);
  the server's version, uptime and connection counts read "not available over the protocol". Settings edits mode, name, server,
  status URL and theme (`[app] theme`, new) and saves with mode 0600. Dark by default, `t` for light, in the website's colours;
  `NO_COLOR` turns colour off. Without a terminal, or with `--no-tui`, it prints one line and exits 2. See `app/README.md`.
- Website: autocomplete in the terminal interface. While you type, the command line shows the rest of the best match dimmed and a list of up to eight matches (command names, `--status` and its values, feature ids, setting names, `theme` values) with a one-line description each. Tab or Right arrow takes the match, Up and Down choose from the list, Enter takes a chosen item, Esc closes the list, and a click or tap takes an item. The input is an ARIA combobox and the list a listbox, with a polite count of the suggestions. Nothing new is requested or stored.
- Website: a visible System / Light / Dark switch in the page header and in the terminal interface (without script it is hidden and the page follows the system), kept in step with the `theme` command and remembered in the browser; and colour from the whole gruvbox-material palette for headings, status tags, command names, the prompt, flags, ids, links and the bars. Every text colour is checked at 4.5:1 or more against its background in both modes; where an accent is too faint as text on the light page it is a border or an underline and the text keeps a passing colour.
- `site/`, the project website: `tools/site.py` generates `site/index.html` and `site/data.json` from `docs/features.tsv`, `VERSION`, the README settings table
  and this file (`--check` compares the committed files and the colour tokens of `site/style.css` with the sources), and `site/style.css` is hand-written in the
  gruvbox-material palette, dark by default and light with the system. No script and no third-party loads (a CSP says so). `.github/workflows/pages.yml`
  deploys `site/` to GitHub Pages on a push to main that touches it, or by hand; the deploy job runs only when the repository variable `PAGES_ENABLED` is `true`.
- `site/terminal.js`, the terminal-style interface of the project website: a menu, a content pane and a command line (`features [--status S] [id]`, `roadmap`, `settings [name]`,
  `install`, `connect`, `version`, `changelog`, `about`, `credits`, `licence`, `docs`, `links`, `theme`, `clear`, `history`, `--help`), Tab completion, history, a dark and a light theme,
  and, on a phone, a row of scrolling tabs over the content with tappable command chips. The page still works without script and a `plain view` link switches the interface off. The only request is for `data.json`
  on the same site (the content security policy allows scripts and requests from this site only). The section bar now stays in view while scrolling, there is a Top link, a real
  page heading and a favicon. Tested with `node --test tools/terminal.test.js`.
- `app/`, the start of the desktop app, as a Go module of its own: a client
  for the TCP protocol (HELLO, heartbeat, rooms, leaderboards, and the MEMBER_JOINED, MEMBER_LEFT and ROUTE pushes) with a
  conformance test against the real server, a reader and writer for the per-user settings file `re-psn.ini`, and
  `re-psn-app check HOST:PORT` and `re-psn-app version`, next to the terminal UI described above. See `app/README.md`.
- `docker/docker-compose.test.yml`: an opt-in hardened host-network compose file for a shared test host (non-root, no capabilities,
  read-only filesystem, resource limits). It refuses to start without an explicit `RE_PSN_BIND` address.
- `.github/workflows/container.yml`: builds the image for linux/amd64 and linux/arm64 and, when a release is published, pushes it to
  `ghcr.io/undecuple/re-psn` (tags `X.Y.Z`, plus `X.Y` and `latest` for a release that is not a pre-release). A manual run builds and
  pushes nothing unless asked. The first image, `0.2.0`, was pushed by a manual run (amd64 and arm64, unsigned); the arm64 image was not run.
- `-replace` (`RE_PSN_REPLACE`): when the game port is held by an earlier re-psn of the same user, the server sends it SIGTERM,
  waits up to 10 s for the port and starts in its place (Linux only). It finds the earlier instance through a pidfile in
  `$XDG_RUNTIME_DIR/re-psn/` and signals it only if the pid, owner, start time and program name all check out. The server now
  stops cleanly on SIGINT or SIGTERM, and without `-replace` a busy port names the earlier re-psn holding it. See `docs/running.md`.

## [0.2.0] - 2026-11-06

### Added
- A Linux x86_64 AppImage of the server in the release, `re-psn-vX.Y.Z-x86_64.AppImage`, beside the archives: the same binary and
  examples as one runnable file, built by `scripts/build-appimage.sh` with appimagetool 1.9.1 and the type-2 runtime 20251108
  (both downloaded by the release workflow and checked against pinned SHA-256 hashes), smoke-tested in the workflow, listed in
  `SHA256SUMS`. Unsigned; needs FUSE or `--appimage-extract-and-run`. See `docs/releasing.md`.
- `docs/img/social.png` (1280x640), drawn by the art generator for the repository's social preview. It is uploaded by hand in the
  repository settings and no page uses it.
- `tools/art/make_art.py` draws the README banner and terminal images (`docs/img/`) in the gruvbox-material palette
  (`docs/art/palette.json`) with JetBrains Mono pinned by SHA-256. Terminal images come only from transcripts of real runs
  (`docs/art/transcripts/`, recorded by `tools/art/capture.sh`). CI renders them again and fails on any byte that differs.
- `AGENTS.md`: the commands, layout and rules for AI coding agents working in the repository.
- A `VERSION` file with a test that the code, this file and the README agree on it, `scripts/bump-version.sh` (one bump, every place,
  `--dry-run` shows the diff), `scripts/release-notes.sh` (notes from this file) and
  `scripts/check-release.sh`, run by `scripts/release-build.sh`. See `docs/releasing.md`.
- Scores go through a storage interface, and `-db` / `-db-file` (`RE_PSN_DB`, `RE_PSN_DB_FILE`) pick the backend. Only `off` (the default)
  and `memory` exist, so scores are still kept in memory; database settings stop the server at start, and the setting is never printed.
- `docs/connecting.md`: the ways players reach a server (LAN, port forwarding, a public server with `-relay`, Tailscale, ZeroTier, a
  reverse proxy), with diagrams and links to the overlays' documentation. re-psn has not been run over Tailscale or ZeroTier.
- A features list: `docs/features.tsv` holds one row per feature with its status and evidence, and `tools/features.py` generates
  `docs/features.md` from it. CI runs `python3 tools/features.py --check`, which fails on a stale page, a malformed row, or
  evidence that does not exist.
- Release archives include an `examples/` folder: `re-psn.env.example` with every setting, a systemd unit, and Traefik, nginx and HAProxy
  configs that pass the real client address. `scripts/check-deploy.sh` checks the env file against the server's flags and runs in
  `scripts/release-build.sh`. Only the Traefik config has been run.
- Reverse proxy support: `-trusted-proxies` and `-proxy-protocol` (PROXY protocol v1/v2 on the game TCP port), and forwarded
  headers for the status page log, both off by default. The real client address then counts for `-max-conns-per-addr`, the
  name take-over rule, `public_ip` and the log. UDP is not proxied. See `docs/running.md`.
- Every flag can be set as a `PSNR_<FLAG>` environment variable (a flag wins), and `-tcp-addr` / `-udp-addr` give the two
  game sockets their own addresses or ports. A bad value stops the server at start. See `docs/running.md`.
- Invite-only rooms (protocol 0.4): `SET_ROOM_ACCESS`, `JOIN_BY_CODE` and `GET_ROOM_ACCESS` give a room an 8-character code and take it
  out of search and the status page (which counts "unlisted rooms"). Wrong codes are rate-limited. HELLO_ACK flags bit 1 says the server
  has them. See `docs/api.md`.
- Logging with levels: `-log-level` (debug, info, warn, error), `-log-format` (text or json) and `-log-file`. Every refusal
  (connection or request limit, name taken, status page token) is now logged at warn level; `-v` is the same as
  `-log-level debug`. Tokens and query strings are never logged. See `docs/running.md`.
- One player per name on a server, compared case-insensitively. A HELLO with a
  name someone else holds gets ERROR 8 (name taken); from the same IP address
  it is the same player reconnecting, and the old connection is closed. The C
  client reports the refusal through `psnr_connect_error()`.
- Firewall: `Setup.cmd` offers to add a Windows Firewall rule for the server
  (TCP 36100, one UAC prompt); `setup.sh` prints the ufw/firewalld command when
  a firewall is on. `docs/running.md` covers the server's and players' rules.

- NAT traversal. The server listens on UDP 36100 too. A player's P2P socket
  sends it a PROBE (with a token from HELLO_ACK), and the server records the
  public endpoint the probe came from. Member entries now give each viewer the
  address that works from where they are: that public endpoint from another
  network, the member's LAN address from the same one. Clients built against
  0.2 still work; without a probe nothing changes. The C client builds and
  reads the packets (`psnr_probe_packet`, `psnr_punch_packet`,
  `psnr_probe_reply`, `psnr_is_control`); the caller sends them from its own
  P2P socket. Protocol in `docs/api.md`, "UDP".
- The relay (`-relay`, off by default), for players who can't reach each other
  directly. Streams into a player behind a router go through the server
  (STREAM_CONNECT / STREAM_OFFER / STREAM_ACCEPT / STREAM_READY), and ROUTE
  pushes say which members need it. Datagrams go through the server's UDP
  port (RELAY) when punching doesn't get through, as with a symmetric NAT.
  HELLO_ACK gains a flags byte saying whether the relay is on. The C client
  wraps and unwraps relayed datagrams and opens and accepts relayed streams.
  In the lab, the symmetric-NAT scenario now passes, and every scenario also
  runs a host-to-joiner stream.
- `lab/`: a NAT lab in Docker. A psnr server on a pretend internet, headless
  players behind NATing routers, and three scenarios: two homes (passes), a
  symmetric NAT (fails until psnr has a relay), and two players in one home
  (passes, over LAN addresses). CI runs it.

### Fixed
- `docs/running.md` said the P2P port is UDP only; titles use TCP on it too.

### Changed
- The release workflow builds its notes with `scripts/release-notes.sh` from the CHANGELOG.md entries for the version in `VERSION`
  (ending "Next release: when ready"), and on a tag fails before building unless the tag is `v` + `VERSION` and
  `scripts/check-release.sh --tag` passes. A manual run still builds everything and publishes nothing.
- `README.md` is rewritten: a fork notice crediting sp00nz, an overview with a session diagram, an example from real local runs,
  the available features, install, a settings table checked against the flags, and the `PSNR_*` migration note.
- `ROADMAP.md` is rewritten around the feature ids (Done, Now, Next, Later, Not planned), keeping upstream's scope section; the
  features list gains planned and later rows for storage, high availability, metrics, the apps' themes and the docs, and
  `tools/features.py --check` also fails when a roadmap item sits under a section that does not match its status.
- `deploy/README.txt` is now `deploy/README.md` (`examples/README.md` in the release archives); the docs share one layout, and the deploy and Docker READMEs gain Mermaid diagrams.
- The docs use the fork's name, re-psn, and credit sp00nz as the author of the original psnr. `client/psnr.c` keeps its name.
- Settings are read from `RE_PSN_<FLAG>` variables. The old `PSNR_<FLAG>` names still work as aliases: a flag wins, then
  `RE_PSN_<FLAG>`, then `PSNR_<FLAG>`. An old name logs a warning at start naming the old and new variable (never the value), and
  if both are set and differ the new one is used, with a warning. `deploy/psnr.env.example` is now `deploy/re-psn.env.example`,
  with the old names in a comment block; `scripts/check-deploy.sh` checks both lists and refuses a live `deploy/re-psn.env` or
  `deploy/psnr.env`. The compose file, Docker README and proxy examples use the new names.
- The server binary is `re-psn` (`re-psn.exe` on Windows): release archives are `re-psn-vX.Y.Z-<os>-<arch>` (.tar.gz, .zip) with
  `re-psn-*.sha256` files and SHA256SUMS, and the release workflows look for those names. No `psnr` copy is shipped. The image, the
  compose service and the `Setup.cmd` / `setup.sh` launchers (`Start re-psn.cmd`, `start-re-psn.sh`) and Windows firewall rule
  (`re-psn`; the old `psnr` rule is removed) use the new name. `deploy/psnr.service` is now `deploy/re-psn.service`: it starts
  `/usr/local/bin/re-psn` and reads `/etc/psnr/psnr.env`, then `/etc/re-psn/re-psn.env` (both optional, the later one wins); the
  state directory stays `/var/lib/psnr`. `scripts/check-deploy.sh` checks the unit's binary and both env paths. The Go module path,
  the C API, the UDP magic and the wire format are unchanged.
- The architecture and NAT lab diagrams are Mermaid (GitHub renders them), and `docs/architecture.md` gains a diagram of the optional
  reverse proxy path and diagrams of a relayed connection (a VPS example) and of how a relayed stream is set up. The text there that said
  there is no hole punching and no relay is corrected.
- PROXY protocol v2 headers are now checked for family and transport: unassigned values, and a datagram or unspecified transport on the
  TCP game port, close the connection (a LOCAL header still carries no client and is consumed to its declared length). The nginx example
  is labelled v1 (`proxy_protocol on;` sends v1), and `docs/running.md` says Cloudflare Spectrum's PROXY protocol is an Enterprise feature.
- The Dockerfile, compose file and `Dockerfile.dockerignore` moved into `docker/`, with a README. Build from the repository root with
  `docker build -f docker/Dockerfile .`, or run `docker compose -f docker/docker-compose.yml up -d`. `lab/run.sh` and Dependabot follow.
- The Docker image no longer forces `-http 0.0.0.0:36101` without a token: the status page stays on localhost inside the
  container until `PSNR_HTTP` and `PSNR_HTTP_TOKEN` are set (the compose file shows how).
- Protocol additions that Simpsons Arcade's Matching2 use needs. Clients
  built against 0.1.0 no longer match.
  - CREATE_ROOM and JOIN_ROOM carry the member's own data, and every member
    entry returns it.
  - SET_ROOM_DATA `which` 2 sets the room's flags, which searchers see.
  - ROOM_MSG pushes say which member the message was sent to.
- `docker/docker-compose.host.yml`: an opt-in compose file for host networking on Linux, for runtimes that rewrite source addresses. `docker/docker-compose.yml` keeps its ports and defaults and gains a commented `RE_PSN_RELAY` example; `docker/README.md` says what relay is and when to use host networking.
- The README status line is now a row of badges in the gruvbox-material colours, and `docs/status.png` is a new screenshot of the real
  status page (`tools/art/capture-status.sh`; it is not part of `tools/art/make_art.py --check`, a browser screenshot is not reproducible).

### Security
- UDP PROBE and RELAY packets are accepted only from the IP address of the player's TCP connection, so a stolen or guessed
  token, or a forged source address, can no longer point a player's endpoint at a third party. A probe's `local_ip` is kept
  only if it is private, carrier-grade NAT, link-local or the probe's own source. Players whose UDP leaves from another address
  (some multi-WAN or carrier NAT setups) are refused; `-udp-any-source` (`PSNR_UDP_ANY_SOURCE`) turns the check off. See
  `docs/running.md`.

## [0.1.0] - 2026-09-29

### Added
- Server (`server/`), speaking a big-endian TCP protocol with request ids so
  several asynchronous NP calls can be in flight at once.
- Rooms modelled on sceNpMatching2:
  - create, search, join, leave and kick;
  - external and internal room data, and room messages;
  - owner handoff when the owner leaves;
  - member pushes that carry each peer's address and P2P port.
- Leaderboards modelled on sceNpScore:
  - each player's best score kept, ties to whoever scored first;
  - higher-is-better and lower-is-better boards;
  - ranking by range and by online ID.
- A read-only status page and `/api/stats` JSON on a separate localhost
  listener.
- C client (`client/`) with no threads: `psnr_send` returns a request id, and
  `psnr_poll` hands out replies and pushes without blocking.
- Tests:
  - the Go end-to-end tests run over real sockets;
  - `TestCClient` builds the C client test and runs it against the server.
- Docker image and compose file. `Setup.cmd` / `setup.sh` quick start.
