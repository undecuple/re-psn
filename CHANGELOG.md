# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- App: the Play page says what a check found and did not find: a server reached with no title ID or status URL set lists no rooms and now says why, a status page's count of unlisted rooms is shown, a Copy address button copies the saved game server address (selecting it when the browser blocks copying), and a failed check lists a few things that may be the cause, chosen by the kind of failure.
- App: a first-run Getting started checklist in the browser interface, shown after the first workspace choice until you skip or finish it (the `[app] setup_done` key; an existing settings file without the key is treated as done, so nobody who already uses the app sees it). Its buttons only open pages and contact nothing, and it returns on reload until you skip or finish it. The Play page also shows when the last check finished and a fixed next step.
- Docs: `docs/sdk-guide.md`, a developer guide to the SDK in three parts: how the pieces fit, building and the connection lifecycle, blocking and threads, errors and capabilities; rooms, pushes, leaderboards, a mapping from sceNpMatching2-style calls and the Python client; then NAT traversal, the relay, LAN discovery, sign-in passes, the local trophy SDK, testing and troubleshooting. Its C and Python examples were compiled, and the small ones run against a loopback server.
- Docs: `docs/code-map.md`, a maintainer's map of the repository with diagrams: the layers and modules of the server, the C and Python clients, the desktop app, the site and tools, and CI and deploy; the main request, relay, sign-in pass and release flows; the entry points; and a table of which test runs where. It also lists what it does not cover.

### Changed
- Docs: the project text now says the server has no account of its own, instead of "no accounts", because a host may optionally require a sign-in pass from a login service it runs itself; the README tagline, the known issues, the protocol and SDK pages and the SDK guide are changed to match, and the roadmap no longer says re-psn will not grow beyond its scope. `docs/running.md` now says a refused pass is logged whenever the server has pass settings, not only under `-require-pass connect`. The desktop app known issue states the Windows and Wine evidence.
- Docs: the README header is quieter. The seven status badges under the banner are gone, because the banner's strip already shows the same words from the features list; the shield rows shrink to one (CI, release, licence, Discord), and a one-line note says what the strip means. The banner's cursor blinks like a terminal's. `docs/img/banner.png` is now an animated PNG of two frames (cursor on, cursor off) that loops forever; GitHub plays it, and other viewers show the first frame, the cursor on. `tools/art/make_art.py` draws it and its test checks the two frames.
- Docs: six shipped features (the desktop app, its local browser interface, appearance mode, the app framework, portable identities and friends) were marked in progress; they are now `partial` with the release that first had them, so the Desktop app badge reads partial. The ROADMAP lists them, and the 20 features it never named (the status page fields, admin API, capabilities, LAN discovery, connect policy, app archives and sign-in passes), under Done, and says nothing is in progress.
- Docs: the README is drawn in the website's dark palette throughout. The Project Facts label is no longer black on white: it uses the palette's panel, text, accent and frame colours (a test checks that each one is a token of `docs/art/palette.json`), and the CI, status, Discord and DeepWiki badges use the same label and value colours as the others (the CI badge keeps its pass and fail colours).
- Docs: corrections to the SDK and app pages. `docs/integrating.md` says that LAN discovery exists and that the server can check optional sign-in passes (there is still no login service), and its source pin note lists the later changes; `docs/app-framework-api.md` has the spaces back in numbers that ran into words; `docs/client-ui.md` and `app/README.md` say the installer stages the app, the host and, from v0.8.0, `re-psn-window`, and `app/README.md` lists the saved servers file, the `shell/`, `share/` and `internal/servers/` folders and a command example that runs from the repository root.
- Docs: corrections to the entry pages. The README and ROADMAP say the desktop app has shipped (since v0.7.0, with `re-psn-window` from v0.8.0) and list the host and window in the app archives, `docs/installing.md` gives v0.7.0 as the first release `--app` works with (v0.5.0 and v0.6.0 app archives have no `re-psn-host`, which the scripts require), `KNOWN_ISSUES.md` no longer says the app has no Host screen and says that `-require-pass connect` can gate who joins, `CONTRIBUTING.md` counts the required app tests and no longer says a wire change bumps a protocol version number, and `BUILDING.md` and `docs/README.md` mention the C client install and the `--app` install.
- Docs: the README and `docker/README.md` name 0.9.0 as the newest release and its image (they still named 0.8.0), and the features list gives `since` 0.9.0 for the three sign-in pass rows and 0.3.0 for the website row, which were marked `main` although the files ship in those releases.
- Docs: corrections to the hosting pages. `docs/running.md` says that `-http` also serves `/healthz`, that a revoke file is refused when group or others can access it (not only when it is not 0600), and lists its Storage and Status-page views sections in its contents; `docs/public-server.md` lists all nine UDP drop counters; and `docker/README.md` says the public compose file health-checks `/healthz` with the base image's `wget` (the image has no `HEALTHCHECK` instruction of its own) and describes what `Dockerfile.dockerignore` really keeps out of the build context. Prose only.
- Docs: corrections to the protocol pages. `docs/protocol.md` and `docs/admin-api.md` name version 0.9.0 and no longer call the admin API unreleased, `docs/admin-api.md` says when a request with the right token is still delayed (an `Origin` header or an unacceptable `Host`), `docs/udp-guardrails.md` lists its reasons in the order the server checks them and says which probes are not counted and that a failed DISCOVER reply counts as `write_error`, `docs/side-service.md` gives the real `psnr_connect` time limits and lists LAN discovery, and `docs/api.md` lists the Sign-in passes and Peer keys sections in its contents. Prose only.
- Docs: corrections to the developer pages. `docs/testing.md` names the tests the loopback command runs (`TestCClientReportsRTT` is in the full suite only), `docs/website.md` says the site has one dark theme and stores nothing, lists all five generated and six hand-written site files and gives the full Node test command that CI runs, `docs/releasing.md` no longer says the window program is unreleased or not installed by the install scripts and gives the right sample log versions, and `docs/architecture.md` and `docs/developers.md` describe the whole of `client/` and `tools/` and mention the off-by-default `-medius` switch. Prose only.
- Docs: the client quick start no longer says the C client is two files (it is `psnr.c` and `psnr.h`, plus `psnr_app.c` and `psnr_app.h` for the local trophy SDK), the Python section lists what that client lacks (invite-only room requests and GET_RANKING_BY_ID, besides the relay, probes and passes), and the integration guide points to the install routes. The `psnr_call` comment in `client/psnr.h` now says that a server ERROR is a reply (the return is 1 and the type is `PSNR_ERROR`), and the `PEER_CLOSED` comment says that a server that ends a connection for repeated refused passes or a revoked account shows up as one. Comments and prose only.

### Fixed
- Client: `psnr_relay_wrap` returns 0 for a payload over `PSNR_RELAY_MAX_PAYLOAD` (65490 bytes, the most a relayed datagram carries) and checks the length before adding the 17-byte header, so a length near `SIZE_MAX` can no longer wrap the size check and overrun the output buffer. Before, it accepted any length that fit the buffer. `test_relay_wrap.c` covers 0, the maximum, one more and `SIZE_MAX`.
- Server: a room's member IDs no longer repeat. The ID counter wrapped after 65535 joins to a room and could give a new member the ID of a current member, such as the owner, whose ID decides who may change or kick. A new member now gets an ID no current member holds, and 0 stays unused.

## [0.9.0] - 2026-11-23

Summary: Sign-in passes can now gate who connects (with C and desktop-app clients that send them) and a relay stream ends when its pass stops; a log limit keeps a flood of refused clients out of the log; new docs for hosting on the internet (a public compose file), the wire protocol, a client quick start and the exact pass format with shared test vectors.

### Added
- **Docker:** `docker/docker-compose.public.yml`, a compose file for a host who exposes their own server to the internet, and `docs/public-compose.md`, which says why each setting is there and what the file leaves out. It runs as user 10001 with no capabilities, no new privileges, a read-only root, memory, CPU, process and file limits, a health check on `/healthz`, rotated JSON logs, the status page on localhost with a token from an env file, and the relay, discovery and Medius off. A commented block turns on sign-in passes for a host with their own login service.
- **Clients:** a build and install path for the C client and a single-file Python client. `make -C client PREFIX=... install` installs a static `libpsnr.a`, the headers and a `psnr.pc`; `client/CMakeLists.txt` provides `psnr::psnr` and a `find_package(psnr)` config. `client/python/psnr_client.py` (standard library only) covers HELLO, heartbeat, GET_CAPS, rooms, room data and messages, kick and scores, with loopback tests run in CI. The C sources and headers are unchanged. See the [client quick start](docs/sdk.md).
- **Docs and tooling:** the README shows seven status badges (server, relay, sign-in passes, C client, desktop app, LAN discovery, admin API) in the site's colours, each linking to its table in the features list. `tools/badges.py` generates them in `docs/img/badges/` from the status words in `docs/features.tsv` (the weakest row of a component decides), and CI checks them with `--check`. A badge shows only that status word; "available" means merged with a test or a recorded run, not tried with a game.
- **Server:** optional sign-in passes for the relay. With `-pass-key-file`, `-pass-issuer`, `-pass-audience` and `-require-pass relay`, the relay carries streams and datagrams only between players who both hold a short-lived pass (Ed25519, checked offline, valid for at most an hour plus 60 s of clock skew) signed by a login service the host trusts. New request AUTH (0x05), ERROR 10, GET_CAPS bit 7, the `relay_no_pass` drop counter, `-pass-revoked-file` and `-pass-max-conns-per-sub`. Off by default: a server without these settings behaves as before. No login service or client exists yet, and players' names are still unauthenticated. See [Sign-in passes](docs/api.md#sign-in-passes).
- **Server:** optional `-require-pass connect`, with `-pass-max-unauth-conns` (default 64) and `-pass-max-unauth-per-addr` (default 4). A connection becomes a player only when AUTH accepts a pass with the `connect` scope: until then HELLO is only parked (no name reserved, no UDP token, not listed), every request but AUTH, HEARTBEAT and the pass bit of GET_CAPS gets ERROR 10, and the connection is closed 30 s after accept if it has not signed in (HEARTBEAT does not extend this; a frame before AUTH is at most 2055 bytes). HELLO_ACK flag bit 3 hints that a pass is needed; OK to AUTH carries the UDP token in this mode; the connection is closed 60 s after the pass expires unless a refresh AUTH arrived. Off by default: a server without these settings behaves as before. Passes bound to a client key are not built, no login service or client sends AUTH yet, and players' names are still unauthenticated. A client that predates AUTH is refused on purpose under this setting. See [Connect mode](docs/api.md#connect-mode).
- **Docs:** `docs/public-server.md`: recommended settings for a server that the internet can reach. What you take on (no authentication, no flood protection, no TLS, no moderation tool), a table of each relevant setting with a value and a reason, the relay and its unlimited bandwidth, which ports to publish, running as an unprivileged service, what to watch in `/api/stats`, and what to tell players. It says no public instance exists.
- **Clients:** AUTH from the C client and the desktop app's Go client. C: `psnr_auth(c, pass, len, timeout_ms)` sends AUTH (version 1) with an opaque pass of 1 to 2048 bytes and returns 0 or the ERROR code (10 pass refused or needed, 8 name taken, 1 from a server with no pass settings), plus `PSNR_AUTH`, `PSNR_E_PASS`, `PSNR_CAP_PASS_RELAY`, `PSNR_PASS_MAX` and `psnr_pass_hint` (HELLO_ACK flag bit 3). Go: `Conn.Auth`, `ErrPass`, `HelloAck.PassRequired`. An empty or oversize pass is refused before anything is sent; the UDP token in an OK under `-require-pass connect` replaces the HELLO token. The clients never fetch, store, parse, check or log a pass; they send what they are given. No login service exists yet, so nothing but a test key can mint a pass, and the desktop app has no sign-in screen. Tested on Linux against the real server (`TestCClientAuth`, `TestAuth*`). See [Sending a pass from a client](docs/api.md#sending-a-pass-from-a-client).
- **Docs and tests:** the sign-in pass format is written down exactly in `docs/api.md` (text, base64url without padding, the 2048-byte limit, Ed25519 over the text as sent, integer numbers, the eight fields and the ninth name `cnf` that is always refused, the 60 s skew), with a plain statement of what the server does today: key names are matched without regard to case, when a key is repeated the last one wins, and a `null` or missing `iat` is read as 0. Nothing in the server changes. `server/testdata/pass_vectors.jsonl` holds over a hundred passes, valid and invalid, made with a throwaway test-only key, and `TestPassVectors` runs every one against the server's check. `docs/running.md` has a section for hosts that use passes: rotating the key, clocks, where a refusal reason is logged and how to tell that a SIGHUP reload failed.

### Changed
- **Server:** a client that is not a player yet can no longer fill the log with warn lines. Each kind (message and reason) of these is written at most once a second, ending in `skipped_since_last_line=N` for the lines of that kind left out since the previous one, and a kind that stops with lines left out reports them in a `log lines left out` line a second later and at shutdown. The kinds: a connection refused for the per-address, total or not-signed-in limit (these two used `refused_since_last_line` before) or for no valid PROXY header, a HELLO refused for a taken name, and, under `-require-pass connect`, a pass refused and a connection closed for refused passes, for no sign-in in time, for a frame too long before sign-in or for its request rate before sign-in. Without `-require-pass connect` a connection has no sign-in state, so its request-rate close is still always logged and its connect, HELLO and close lines are bounded only by the per-address limit. A disconnect or failed read of a connection that never became a player (HELLO accepted, or AUTH accepted in connect mode) is logged at debug instead of info. Lines about players are unchanged, and no flag, wire message or fail2ban filter changes. The `/api/stats` counters are unchanged too: only the `tcp_refusals` counters (`conns`, `conns_per_addr`, `req_rate`) count refusals, so the other events are counted by the `skipped_since_last_line` numbers. See [Logging](docs/running.md#logging).
- **Docs:** `docs/protocol.md`, a protocol specification: byte layouts for every TCP frame and UDP packet, the connection state machine, the order of checks and the error code each request can return, limits, versioning rules, security notes and the tests that pin each area, with the behavior no test pins marked. `docs/api.md` is corrected where it disagreed with the server (the largest relayed datagram is 65490 bytes; a pass can open the relay for about 62 minutes; the kicker also gets MEMBER_LEFT; HEARTBEAT before HELLO gets ERROR 7; capability bit 7 also needs `-relay`; ROUTE bit 0 follows the `local_ip` a client sends and is sent once, at join; an unknown room is ERROR 5 for every room request but JOIN_ROOM) and gains a link to the specification.
- **Server:** a relay stream that is already open now ends when a pass stops covering it. It is closed at SIGHUP when either player's account is revoked or, where `-require-pass` is set, when the key that signed either pass is removed, and 60 s after either pass expires unless that player's own connection sends a refresh AUTH. A stream is tracked from its offer, so a reload during setup cannot miss it, and it leaves nothing behind when it ends normally.
- **Docs:** the comments in `client/psnr.h` and `client/psnr_app.h` are now Doxygen blocks (`@file`, `@defgroup` groups, `@brief`, `@param`, `@return`, `@note`), and every public declaration has text. The existing behaviour text is moved, not rewritten. Comments only: the preprocessed headers have the same tokens as before. No Doxygen configuration or CI job is added yet.
- **Docs and tooling:** the README banner (`docs/img/banner.png`) has a status strip along the bottom: one cell for each component of the README badges (Server, Relay, Sign-in passes, C client, Desktop app, LAN discovery, Admin API) with its status word in the badge colours. `tools/art/make_art.py` reads the words from `docs/features.tsv` through `tools/badges.py` (the weakest row decides), so the image cannot say more than the list. `tools/art/art.json` pins both files, so `--check` also fails when the strip's source changes without a new render. The name is a little smaller to make room, and the repository address moved to the bottom row. `social.png` and the terminal images are unchanged.
- **Docs:** `docs/README.md`, an index of every page in `docs/` and the app, deploy, docker and lab READMEs, grouped by what you want to do (play and connect, host, develop, operate, maintain, reference). The README's "More" section links to it, and `tools/repo_check.py` now fails when a `docs/*.md` page is not linked from the index.
- **Docs:** the README no longer points to the GitHub wiki.
- **Docs:** the README's first screen now follows the website's look: a centred title and one-line description that says it is unofficial fan work, two badge rows in the site's colours (with the Discord invite badge), and a divider image between sections.
- **Docs:** the README settings table (now 43 flags) and the website's settings list include the two sign-in limit flags for connect mode.
- **Docs:** the README settings table and the website's settings list now include the six sign-in pass flags (`-pass-key-file`, `-pass-issuer`, `-pass-audience`, `-require-pass`, `-pass-revoked-file`, `-pass-max-conns-per-sub`), and the README says 41 flags, not 35. The sign-in pass change had left both stale.
- **Docs:** the "Project Facts" label on the README is wider and larger (shown at 624 pixels, not 420), with sharper edges and more text on each line, and the Purpose column no longer crowds the right edge.
- **Docs and tooling:** the README and `docker/README.md` name v0.8.0 and its container image as the newest (its manifest lists amd64 and arm64 and is readable without a login), and the notes about `re-psn-window` say it is in the app archives from v0.8.0 on. No row of the features list changes its `since` value: the website row stays at `main`, as the site is deployed from `main`, not released.

## [0.8.0] - 2026-11-18

Summary: The app keeps a list of saved servers, can open in a window of its own and explains its words in plain language; the status page is lighter on busy servers; new notes for games that need fast peer-to-peer play.

### Added
- **Docs:** a "Project Facts" label on the README, set out like the label on a medicine box: what the server does, the warnings, directions, and other information. `docs/project-facts.svg` is generated by `tools/project_facts.py` (black on white, no font or outside address; its `--check` is a unit test, so CI runs it) and states the version, so a bump regenerates it.
- **Docs:** a section in `docs/api.md` for titles that run a real-time peer-to-peer session such as rollback netcode: one P2P socket, a room flag and an `external` record to agree on the mode, roles, direct, hole-punched and relayed paths, and what the relay carries (any payload that fits a UDP datagram, 1000 packets a second per source by default). Two server tests (`server/relay_rollback_test.go`) show 60 datagrams a second in each direction and payloads up to 60000 bytes passing through the relay. The server is unchanged.
- **Website:** a Discord badge in the page footer, linking to the re-psn Discord invite and described as unofficial fan work. It is a file stored in the site (`site/discord-badge.svg`), drawn in the site's own palette and generated by `tools/site.py`, so the page still loads nothing from another site.
- **Desktop app:** the Saved servers list can import a list from a file (the file Export list writes). A confirmation shows how many entries will be added and how many skipped; a file that is not a valid list is refused whole; imported entries stay "Not checked" and import contacts no server.
- **Desktop app:** a Saved servers list on the Play screen: add, remove, check one or all (4 at a time, 5 seconds each, after a confirmation), use an entry as the Play target, and export the list. It is a list you keep yourself in `servers.json` (private, at most 64 entries); nothing is contacted until you press a check, no list is fetched from anywhere, and a check never sends the Play status token.
- **Desktop app:** `re-psn-app` opens the app in `re-psn-window` when that program is beside it, and `--browser` opens a browser tab instead; it passes its `ui` flags on to the window, and falls back to the browser when the window ends with an error within 5 seconds. `install.sh --app` and `install.ps1 -App` install `re-psn-window` beside the app and its host when the release has it, replacing and restoring the three files together. Run on Linux; not run on Windows. See `docs/client-ui.md` and `docs/installing.md`.
- **Desktop app:** a native window for the app, `re-psn-window` (a small Tauri program in `app/shell/`): it starts `re-psn-app`, shows its private launch link in a window of its own and stops the app when the window closes. `re-psn-app ui` gets `--exit-with-parent`, which stops the app cleanly when its standard input closes, so a parent program can stop it on any system. The release builds it natively for Linux (x86_64 and arm64) and Windows and puts it in the app archives. Dependencies are pinned in `app/shell/Cargo.lock`. Linux needs WebKitGTK 4.1 installed to run it, Windows the WebView2 runtime. Run on Linux with a virtual display; not run on Windows, and the install scripts do not install it yet. See `docs/client-ui.md`.

### Changed
- **Server:** the status page and `/api/stats` now share one snapshot per second instead of building one for every request. A snapshot takes the lock every game request takes (about 0.2 ms at 1,000 players, 1.2 ms at 5,000), and in a load test four status clients asking flat out cut game throughput by 59%; now the cost to the game is one build a second however often the status address is asked. The numbers can be up to a second old. Embedding the server in tests or other code is unchanged: with no cache set, every request is current.
- **Desktop app:** a "What do these words mean?" section at the bottom of the app explains Play target, game server, status URL, Reachable, saved servers, owned host, Both, the relay on a host and the friend relay in plain words, written from what the app does.
- **Docs and tooling:** `scripts/release-notes.sh` accepts an optional first line `Summary: ...` in a version's CHANGELOG section and prints it first as `**Summary:** ...`, before the pre-release notice, so the start of the release notes (which a chat post shows) says what changed. A Summary with no text, or with no entries under it, is refused. Sections without one print as before.
- **Server status page:** each number at the top now has a one-line caption saying what it counts (for example, "connections total" is every connection accepted since the server started, including closed ones), and a "What do these words mean?" section explains the room, member, leaderboard and host terms. It needs no JavaScript.
- **Docs and tooling:** the README's server status screenshot is retaken (it showed v0.3.0), and five screenshots of the app (first run, Overview, Play, Host, Profile) are added to the README and the client guide. `tools/art/capture-app.sh` retakes them from the real app on loopback with synthetic data; like the status screenshot they are taken by hand and are not part of `make_art.py --check`.
- **Local browser interface:** the page now rewrites a piece of text only when it changes, so a refresh every 4 seconds no longer clears a text selection or makes screen readers announce the status line again (the clock moved to its own silent element), and the Cancel and Continue buttons of the confirm popup close it themselves instead of through a form submission. Checked in Chromium 156 against the real app; not run in Edge, WebView2 or WebKitGTK, and the Edge popup report is still not reproduced.
- **Docs and tooling:** the file list in the release notes (`.github/workflows/release.yml`) says that the `re-psn-app` archives also hold the matching `re-psn-host` server binary and that `install.sh --app` and `install.ps1 -App` install both; before, it described the app archive as one binary. Only the notes text changes: no asset, name or check.
- **Docs and tooling:** the README and `docker/README.md` name v0.7.0 and its container image as the newest (its manifest lists amd64 and arm64 and is readable without a login). No row of the features list changes its `since` value: the website row stays at `main`, as the site is deployed from `main`, not released.

### Fixed
- **Server status page:** the page no longer reloads itself every 5 seconds. Its no-JavaScript refresh tag was removed by the script too late to stop the reload the browser had already scheduled, so even with JavaScript on the whole page reloaded and lost its state; the tag now sits inside `<noscript>`. Checked in Chromium 156 (4 page loads in 18 seconds before, 1 after; with JavaScript off the page still refreshes); not run in other browsers.

## [0.7.0] - 2026-11-15

### Added
- **Desktop app:** an optional local browser interface, opened by default (`tui` and the command-line actions stay). It has Play, Host and Both views and keeps the remote play target apart from local hosting: services start only when asked. It wires catalog approval, game grants for the trophy SDK, encrypted profile backup and restore, and the friend actions to its screens. The browser launch is private and one-use, reopening is authenticated, and a per-settings controller lock allows one controller per settings directory. Stopping the app stops only the host it started; a separately started server keeps running. This is a large change: the embedded screens, the authenticated controller, process ownership on Linux and Windows, the paired packaging and the regression tests belong to one workflow. See `docs/client-ui.md`. Not checked: hosting and the default-browser launch on native Windows, ARM hardware and a live game.
- **Desktop app:** profiles, trophies and friends in the desktop: Profile, Trophies and Friends panes, explicit profile, trophy and friend command-line actions, and host-local companion administration. One desktop session owns its profile store, renews the game grant for its title and cancels social operations when it closes; an expired grant says it needs reissuing. The new native SDK and two-host tests are required in Linux CI and a Windows compile and link check of the SDK is added. See `docs/app-framework.md`.
- **Desktop app:** an app framework (`app/framework`): a bounded, atomically written local profile and trophy store, a portable identity with encrypted backup and restore (AES-GCM, key from a passphrase of at least 12 characters), a loopback broker that gives each game title a scoped, revocable grant, and an opt-in TLS 1.3 social service and client with explicit host admission, per-host registration, consent-based friendships, blocking and host revocation. The C and C++ SDK is `client/psnr_app.h` and `client/psnr_app.c` with an example (`client/examples/app_trophy.c`). The game protocol and the server do not change: game names stay unauthenticated, a trophy report records only when it was received and proves neither an earned achievement nor a PSN account, and live presence, invitations, remote trophy sync and provider verification are not there. `ROADMAP.md` and `SECURITY.md` state the changed scope; see `docs/app-framework-api.md`.
- **Server:** the read-only status page has Play, Host and Both views (a preference kept in the browser; Host grants no permission). Host shows the existing counters and limits, and Both adds the player tables. Appearance controls, keyboard use, narrow layouts and the tables without JavaScript are kept.
- **Docs and tooling:** `CITATION.cff`: citation metadata that GitHub shows as "Cite this repository" (title, maintainer, repository and website, MIT licence, and the upstream psnr project as a reference); the repository layout check allows the file in the root.
- **Docs and tooling:** `BUILDING.md`: a short page that points to where each part is built and tested (the server, the desktop app, the C client, the container image, release archives and the AppImage); the README's document list links it, and the repository layout check allows the file in the root.
- **Docs and tooling:** `KNOWN_ISSUES.md`: what is known not to work or not to have been checked (early software, binaries and scripts not run on every platform, the experimental admin API, the relay off by default and other limits), each with a link to the page that says it in full; the README's document list links it, and the repository layout check allows the file in the root.

### Changed
- **Desktop app:** the app archives now contain the matching `re-psn-host` (the server, under that name) next to `re-psn-app`, and `install.sh` and `install.ps1` install both with `--app` / `-App`. The pair is staged and rolled back if a move fails, but two files are not replaced atomically across a crash: stop the app and its host before updating. An older app archive without `re-psn-host` is refused, and an installed standalone `re-psn` is not touched. The Windows app is built as a windowed program (`-H=windowsgui`).
- **Docs and tooling:** the issue chooser's link to the upstream issues says re-psn began as a fork of psnr, instead of "This is a fork." (`.github/ISSUE_TEMPLATE/config.yml`; the link and its target are unchanged).
- **Docs and tooling:** the README's longest sentences are split (the fork history, the overview, the session steps, the desktop app and build paragraphs, the security list, the roadmap line and the closing paragraph); no fact, link or command changed.
- **Docs and tooling:** `docs/installing.md` starts with "Install, step by step": numbered steps for Linux and for Windows, each with what the script prints, what to do when the folder is not on `PATH`, and how a failure shows. A few of the longest sentences in that page are split; no command, flag or behaviour changed.
- **Docs and tooling:** the wording that called re-psn "a fork" in `CONTRIBUTING.md`, `AGENTS.md`, `NOTICE.md`, `SECURITY.md` and the website now says it began as a fork of psnr and is a repository of its own; the credit to upstream and the licence lines are unchanged. The security page heading "What this fork changes" is now "What re-psn changes".
- **Docs and tooling:** the README's first screen is centred (banner, one-line description, badges) with a links line (Download, Quick start, Features, Install, Configuration, Documentation, Website, Report an issue), and its main sections are separated by horizontal rules. The "About this fork" section is now "About this project": it says re-psn began as a fork of psnr and is now a repository of its own, and the credits keep the upstream work and licence line as before.
- **Docs and tooling:** the README and `docker/README.md` name v0.6.0 and its container image as the newest, and the features list says `since` 0.6.0, not `main`, for four rows that shipped in it (`-no-peer-titles`, the capability report, the C client's connect policy and the experimental admin API).

### Fixed
- **Desktop app:** a protocol request now honors its context while it waits for and uses the connection: acquiring the writer respects cancellation, cancellation is checked before a socket write, and a blocked write is interrupted. A frame interrupted half way closes the connection; cancelling a request that is only queued keeps it open. A request that already completed is not undone.
- **Client:** the whole setup of `psnr_stream_connect` and `psnr_stream_accept` now runs under one monotonic deadline: an interrupted wait is retried with the time left (also for `psnr_connect_timeout`), partial reads and writes during setup are handled, a negative relay timeout still means no limit, and a successful call returns a blocking stream. The setup-deadline, interrupted-connect and native relay-pair tests are required in Linux CI.
- **Server:** replies and pushes to a connection now leave in the order the room changed. Each control connection has one output queue, written outside the room lock, so a join, a leave, a disconnect clean-up and a relay offer can no longer overtake each other. A queue holds at most 2048 frames and 128 KiB; a connection that overflows it or fails to write is closed. The wire format and settings are unchanged.

## [0.6.0] - 2026-11-12

### Added
- **Docs and tooling:** website: the plain pages (index, in action, discuss) are laid out like the undecuple page: under the banner a box of key and value rows with dotted leaders (project, version, features, repository) beside a box of links, double-framed tables with alternate-row shading, and section headings with a block of four greys. The sticky title bar and the framed section legends are gone, and the menu entries are now lower case (`in action`, `discuss`). The terminal view keeps its layout and hides the new block; its buttons (`[F1 help]`, `plain view`, the command chips) and the menu tabs now have a visible outline, as have the four entries of the legend on the "in action" page. The "in action" and "discuss" pages offer a "Main page" link at the top and the bottom and, when the reader came from another page of this site, a "Previous page" button (`site/nav.js`, same origin, nothing stored or sent). The font list is now the undecuple page's (local fonts only, ligatures off), so the two sites look alike on one machine; line spacing stays at 1.5. The change is in `site/style.css`, `tools/site.py`, the new `site/nav.js` and the generated pages; the Content-Security-Policy of every page is unchanged (`nav.js` is a same-origin script).
- **Docs and tooling:** website: the terminal view has a `discuss` command and a "Discuss" menu entry. `discuss` says what the discussion page is and links it (`discuss.html`), the Discussions on GitHub and the issues; like the page, the command loads nothing from another site.
- **Docs and tooling:** website: in the terminal view the name in the header of an answer now ends in `.md` (`about.md`, `features/rooms-basic.md`, `settings/addr.md`), and the newest answer fills the pane, with the line-number gutter's rule running to the bottom, like a file open in a viewer. Styles and file names only; no new command and no change to what an answer says.
- **Docs and tooling:** website: a discussion page (`site/discuss.html`, "Discuss" in the menu): one GitHub Discussions thread (repository `undecuple/me`, category Announcements, term `re-psn`) shown with giscus. Nothing is requested from giscus.app or GitHub until the reader presses "Load the discussion"; `site/discuss.js` then adds the giscus script. This is the only page whose Content-Security-Policy allows another site (`https://giscus.app` for scripts, styles and frames); the other pages keep the strict policy, and a test checks both. The settings of the thread are in `tools/site.py`; no setting of the repository was changed by this.
- **Docs and tooling:** website: the terminal view has an `action` command and an "In action" menu entry. `action` lists the four simulated scenarios; `action direct`, `action punch`, `action relayed` or `action blocked` prints that scenario's numbered steps with the message names, and every answer links to the animated page. The text comes from the same data as `site/action.html`, so nothing is added to the page's data.
- **Docs and tooling:** website: a "see it in action" page (`site/action.html`, linked from the page's menu as "In action"): four simulated scenarios (direct, hole punch, relayed, relay off), each a diagram and numbered steps that name the real messages (`HELLO`, `PROBE`, `PUNCH`, `ROUTE`, `STREAM_CONNECT` and so on). The script is `tools/site_action.py`: documentation addresses only, no live traffic, no script on the page, and a test checks every message name against `docs/api.md`. With script (`site/action.js`, same origin, no library) one scenario shows at a time and a packet moves along the diagram: Play/Pause, Back, Step, Restart, a speed choice, a clickable step list and the keys Space, Right, Left and R; with a reduced-motion setting nothing moves by itself and Step and Back show each step at once.
- **Client:** `psnr_set_connect_policy(fn, user)` (`client/psnr.h`): the host registers one callback that the C client asks before it opens a TCP connection, so a runtime that restricts where a game may connect can refuse it. It is asked for the server connection (once per resolved address, with the host as typed) and for each relay leg; it returns nonzero to allow and 0 to refuse. If every address is refused, `psnr_connect` returns NULL and `psnr_connect_error()` is the new `PSNR_E_POLICY` (1000, set by the client, not sent by a server); a refused relay leg returns -1. No callback, the default, changes nothing. `psnr_discover` and the title's own P2P socket are not covered.
- **Server:** a capability report (#77, item 3): `GET_CAPS` (0x04, no body, after HELLO) is answered with `CAPS` (0x94): `bits u32` and the server version as a blob. The bits say which features the server offers this player: rooms, scores, room codes, peer keys, the UDP probe, the relay and LAN discovery; the probe and relay bits are per title, so a title in `-no-peer-titles` sees them clear. A server before this answers `GET_CAPS` with error 1. The C client has `PSNR_GET_CAPS`, `PSNR_CAPS`, the `PSNR_CAP_*` bits and `psnr_caps_parse`. See `docs/api.md`, "Capabilities".
- **Deploy:** `docker/docker-compose.yml` and `docker/docker-compose.host.yml` now run the container as user 10001 with all capabilities dropped, `no-new-privileges` and a read-only root filesystem with a 16 MB tmpfs on `/tmp`, the lines `docker-compose.test.yml` already had. A log file (`RE_PSN_LOG_FILE`) inside the container needs a volume, and a listen port below 1024 cannot be bound. Memory, CPU and process limits stay in the test file only.
- **Deploy:** `deploy/re-psn.service` sets `LimitNOFILE=16384`, so a server at the default `-max-conns` (4096, a relayed stream counting two sockets) stays under the file-descriptor limit. `scripts/check-deploy.sh` checks the line. An install that copied the unit earlier keeps its old limit until the file is copied again.
- **Network:** `-relay-idle` (`RE_PSN_RELAY_IDLE`, default 600 seconds; 0 for never) closes a relay TCP stream that has carried no bytes in either direction for that long, and a write to a stream leg that the peer does not read is dropped after 30 seconds. Before, a relay stream cleared its deadlines and stayed open as long as both legs did, so idle or stalled streams held connections (now counted by `-max-conns`) indefinitely. A stream that keeps moving bytes is not limited in time or size.
- **Docs and tooling:** `docs/integrating.md` has a "Mapping the NP calls" section: suggested NP call families to re-psn message tables, a list of what the protocol does not cover (world lists included), and a recommended NP error category for each protocol error code. It is suggested, not tested in a title, and cites sections instead of line numbers.
- **Server:** `-max-conns` (`RE_PSN_MAX_CONNS`, default 4096; 0 for no limit) caps the open TCP connections in total, players and relay legs together. Until now only `-max-conns-per-addr` (32) bounded them, so many addresses could open connections until the process ran out of file descriptors. A connection over the cap is closed without an answer, counted in `/api/stats` (`limits.max_conns`, `tcp_refusals.conns`) and in the admin state, and logged at warn level at most once a second. A server that really holds more than 4096 players at once needs a higher value (and a file-descriptor limit above it).
- **Server:** `-no-peer-titles` (`RE_PSN_NO_PEER_TITLES`, off by default): a comma-separated list of communication IDs (1 to 12 printable ASCII bytes each; anything else stops startup) of titles that use no peer traffic. For a player of a listed title the server answers no UDP probe and records no UDP endpoint, drops relay datagrams from or to the player (counted under `relay_disabled`), answers a relay stream with the "no relay" error (9), pushes no relay route even with `-relay-force`, and clears the relay flag (bit 0) in HELLO_ACK. Rooms, scores and every other title on the same server are unaffected. One list turns off the probe and the relay together; LAN discovery is not covered. See `docs/running.md` and `docs/side-service.md`.
- **Server:** Experimental, read-only admin API, off by default: `-admin ADDR` (`RE_PSN_ADMIN`) opens a second listener with one route, `GET /admin/v1/state`, for a read token kept in a file (`-admin-read-token-file`; at least 32 characters, mode 0600, read again on SIGHUP). An address that is not loopback needs `-admin-tls-cert` and `-admin-tls-key` (TLS 1.2 or newer). The state is aggregate only (version, uptime, relay, limits, refusal counts, the ban count, which is 0 for now): no address, player or token. A wrong or missing token gets one identical 401 after a growing, capped delay and one log line that carries only the socket's remote address, at most 8 of the 32 connections go to one source, an `Origin` header, or a name in `Host` over plain HTTP, is refused. There are no actions yet, and the status page and `/api/stats` are unchanged. See docs/admin-api.md.
- **Server:** Medius compatibility switch, off by default: `-medius` (`RE_PSN_MEDIUS`), `-medius-port` (default 10071) and `-medius-addr` (default `127.0.0.1`; a loopback, private or link-local address, or `0.0.0.0`/`::` in a container). **No Medius protocol is implemented**: with the switch on, the server opens that port and closes every connection to it without reading or writing, and says so in a start-up warning. A port or address other than the defaults is refused without the switch. Commented examples in both Docker compose files, `deploy/re-psn.env.example` and `docs/running.md`.
- **Client:** constants for `PEER_AUTH`, `SET_ROOM_ACCESS`, `JOIN_BY_CODE`, `GET_ROOM_ACCESS`, `ROOM_ACCESS` and `PEER_KEY`, and
  bounds-checked decoders for ROOM_JOINED, member entries, ROOM_LIST and RANKING (`psnr_room_joined_parse` and friends).
- **Client:** `psnr_connect_timeout(...)` (`client/psnr.h`): `psnr_connect` with a time limit. The TCP connect is non-blocking and the wait for HELLO_ACK is bounded, both by one `timeout_ms` budget; a stalled server gives NULL instead of blocking the caller. DNS is still blocking and not covered. `psnr_connect` is unchanged.
- **Client:** `psnr_dead_reason(c)` and `psnr_last_os_error(c)`: why the C client's connection was lost (peer closed, I/O error, out of memory, send timeout) and the OS error at that point. The first failure is kept.
- **Client:** `PSNR_SEND_TIMEOUT_MS` (5000, overridable before including `psnr.h`): the most time one write may wait for the socket to accept data, in total.
- **Docs and tooling:** CI compiles the C client (`client/psnr.c`, `client/examples/minimal.c`) with MSVC on Windows and does not run it there; the build instructions in `docs/integrating.md` give the MSVC command.

### Fixed
- **Server:** wrong or malformed invite codes are now also counted by address, so opening a new connection no longer starts the count again: 20 misses in 60 seconds from one address (an IPv6 /64 counts as one) close the connection, besides the existing 5 per connection. The higher limit allows for players sharing a router.
- **Server:** a player's connection no longer reads its title without the server lock while a new connection from the same address takes over its name. The race detector flagged the unlocked read; the takeover itself is unchanged.
- **Server:** a connection that sends a frame header declaring a long payload and then stops no longer costs the server the declared size (up to 64 KB). The payload buffer starts at 2 KB and grows with the bytes that arrive, so memory held by idle half-sent frames is bounded by what was sent.
- **Server:** a relay stream whose accepting side arrived just as the connecting side's 10 second wait ended no longer leaves the accepting connection open. The accepting leg is now used if it had already claimed the stream, and the connection is closed otherwise; before, the accepting player could be told the stream was ready while the connecting player was told it was not.
- **Server:** the in-memory score store now keeps boards for at most 1000 titles, next to its existing 1000 boards per title and 10000 players per board. A RECORD_SCORE that would start a board for a 1001st title gets ERROR 6 and is not kept; titles that already have boards, and players already on a board, are unaffected. Before, the number of titles was not limited. The limit is fixed, not a flag. See `docs/api.md`, Leaderboards.
- **Server:** `-max-conns-per-addr` now counts an IPv6 address as its /64 (an IPv4-mapped address as the IPv4 address), as the UDP limit and the admin failure delay already did. Before, it counted each exact IPv6 address, so connections from different addresses of one network each had a full allowance. The address a player is known by (rooms, peers, logs) is unchanged. Players behind one IPv6 /64, such as one LAN, now share the limit, as players behind one IPv4 address always did; raise `-max-conns-per-addr` if that is too tight.
- **Server:** the `-http` status page now closes a connection that does not finish its request header within 5 seconds, its request within 10 seconds, does not read its answer within 10 seconds, or sits idle for 30 seconds. Before, the status server had no such limits.
- **Server:** `SET_ROOM_DATA` no longer lets the owner make the room's own join reply too big for one packet: a write to the external or internal blob that would is refused with error 6 and changes nothing, so a member who asks for the room again always gets an answer. Before, such a write was accepted and later joins and re-joins were refused.
- **Server:** A search no longer disconnects the player when the rooms' data is large: the reply lists only as many rooms as fit in one packet, and the player pages on from there.
- **Server:** A player whose connection stops reading no longer freezes the other players in its room: the server drops it after 10 seconds of a stuck write (`writeTimeout`), so its room mates' requests get their replies and the stuck player is removed from the room.
- **Server:** A second HELLO on one connection gets error 1 and changes nothing, and a name is held by the player's full IPv6 address: before, all IPv6 players shared one address, so any of them could take another's name.
- **Server:** A room whose members' data, with the room's blobs, would make its join reply too big for one packet no longer breaks the joiner: JOIN_ROOM and JOIN_BY_CODE answer error 6 and the joiner is not added, instead of the connection being closed after the other members were told of it.
- **Server:** a player with no IPv4 address (an IPv6 connection) is no longer treated as being on the same network as every other such player. Before, two IPv6 players both had the address 0.0.0.0, so each was handed the other's TCP-derived address instead of its probed UDP endpoint, and a relay stream between them was never chosen. Now they are never on one network, so the probed endpoint is announced and, with `-relay`, the relay can be chosen for them. This only shows when such a player has a recorded UDP probe, which needs an IPv4 UDP source and `-udp-any-source`, since a probe from an address other than the TCP one is otherwise ignored.
- **Server:** A score server no longer slows down or grows without bound under many boards: the memory store keeps at most 1000 boards per title and 10000 players per board, and a score that would pass either gets ERROR 6 (an existing player can still improve their score). Ranks are read from a sorted list instead of sorting the board on every request, and a GET_RANKING_BY_ID with more than 100 ids is refused before its ids are read.
- **Client:** The C client has `psnr_relay_unwrap_from`, which accepts a relayed datagram only from the server's UDP address. A title that
  reads relayed datagrams with `psnr_relay_unwrap` can be sent a forged sender by any room member; `psnr_relay_unwrap` is kept
  unchanged for compatibility and documented as not checking the source.
- **Docs and tooling:** a comment in `server/rooms.go` that pointed at the missing `docs/room-codes.md` now names the section of `docs/api.md` that holds the content; SECURITY.md states that players behind one address can take over each other's names, and the docs say that `-max-req-rate` does not bound the in-memory score store (which has its own fixed limits per title and board), and that peer keys give no replay protection on their own.
- **Client:** the receive path checks its allocations (a failure now ends the connection instead of dereferencing NULL), a reply
  frame too short to hold its request id is dropped instead of being delivered as a push, and `psnr_connect` refuses a
  `comm_id` over 12 or an `online_id` over 16 bytes instead of cutting it.
- **Client:** in the desktop app, `r` (refresh) on Rooms, Leaderboards and Server does nothing while a fetch is running and for one second after its answer. Every fetch opens a new game connection to the server and says HELLO under a throwaway name, so holding or repeating `r` used to open one connection per press and inflate the server's connection counts. A second refresh right after an answer needs a second press.

### Changed
- **Docs and tooling:** the NAT lab's player (`lab/natcheck.c`) unwraps relayed datagrams with `psnr_relay_unwrap_from` and the source address `recvfrom` reported, instead of `psnr_relay_unwrap`, which does not check the source and which `client/psnr.h` says titles should not use on the P2P socket. The lab's `symmetric` scenario is the one that sends pings through the relay.
- **Docs and tooling:** `docs/releasing.md` no longer says the ten-asset release and the first run of the app-archive upload and `release-check.yml` are still to come: v0.5.0 is that release, and both ran on it and passed. The Windows and arm64 app binaries are still listed as not run.
- **Docs and tooling:** `scripts/bump-version.sh` now also sets the `var version` default of the desktop app (`app/cmd/re-psn-app/main.go`), and `TestVersionAgrees` checks it against `VERSION`; before, a bump left the app's source default at the old number. Release builds set the app version with `-ldflags` and were not affected.
- **Docs and tooling:** the website, the README images, the favicon, the README badges, the desktop app's dark colours and the status page's dark colours now follow the colours of https://undecuple.github.io/me/ (`docs/art/palette.json`); the website has one dark theme (the System/Light/Dark switch and the `theme` command are gone) and a pixel-letter banner in a double frame above the title bar. The light colours of the app and the status page are unchanged. The colours credit guts.nvim through that page; guts.nvim has no licence file, so NOTICE.md names it as the source and states no licence. `docs/img/social.png` must be uploaded to the repository settings by hand to change the social preview. The banner reads "re:psn" in pixel letters and sits at the top of the page, above the terminal view; on screens at least 1400 px wide a tall copy of it stands at the left edge instead.
- **Docs and tooling:** the features list says `since` 0.5.0, not `main`, for the twelve rows that shipped in v0.5.0 (LAN discovery in the server, the C client and the app, game names, verified player counts, server state and reported latency in the status page, `-relay-force`, peer keys, `-lan-public-addr`, the app Server tab and the app archives), and `docs/releasing.md` has the step that sets this column at each release.
- **Docs and tooling:** the README settings table now lists all 35 flags (it lacked `-admin`, `-admin-read-token-file`, `-admin-tls-cert`, `-admin-tls-key`, `-discover`, `-name`, `-lan-public-addr`, `-medius`, `-medius-port` and `-medius-addr`, and said 21), and the Flags table in `docs/running.md` lists `-max-player-rooms`, `-max-conns`, `-max-conns-per-addr` and `-max-req-rate`.
- **Docs and tooling:** `docs/side-service.md` says re-psn offers none of the platform social services (sign-in ticket, friends, presence, block list, recent players, messages, lookup by online ID, account region and age) and that a title with its own server answers them locally; the planned friends, presence and invites feature is not built.
- **Docs and tooling:** README no longer says the desktop app has no release binary: from v0.5.0 a release carries the app archives (the "Release archives" section already said so).
- **Docs and tooling:** README and ROADMAP no longer call ps3recomp#200 a draft: GitHub shows it merged into ps3recomp's `master` on 2026-10-07.
- **Client:** The C client's writes give up after `PSNR_SEND_TIMEOUT_MS` in total instead of waiting without a limit. The connection is then dead with `PSNR_DEAD_SEND_TIMEOUT`, as a failed write already was.

## [0.5.0] - 2026-11-11

### Added
- The desktop app's Status screen has a read-only Server tab when a status URL is set: version, uptime, relay on or off, the limits in
  force (0 shown as none), TCP refusals and UDP drop counters, and the payload the relay has carried with a bytes-per-second rate
  between two refreshes (not network bandwidth). A counter going down is shown as a server restart. A server that omits the fields
  shows each as not reported, never as 0. It changes nothing on the server.
- The release build also makes three archives of the desktop app, `re-psn-app-vX.Y.Z-linux-amd64.tar.gz`, `-linux-arm64.tar.gz` and
  `-windows-amd64.zip`, each with the static `re-psn-app` binary (no cgo, version set from the tag), `LICENSE`, `NOTICE.md` and the
  app's README, and listed in `SHA256SUMS`. A release now has ten assets; `scripts/check-release.sh --assets`, both release workflows
  and their tests expect them (v0.4.0, with seven, is still accepted by the release check). The install scripts do not install the app unless asked (see below).
- `install.sh --app` and `install.ps1 -App` install the desktop app (`re-psn-app`) from a release instead of the server, with the same checksum check, no sudo and no profile edits. They need a release that has the app archives (after v0.4.0) and say so when the download is missing. Without the flag the scripts behave as before.
- `-lan-public-addr IP` (`RE_PSN_LAN_PUBLIC_ADDR`, off by default, IPv4): when a player connected from a private, carrier-grade NAT or
  link-local address, players on public addresses are given IP and that player's P2P port instead of the unreachable LAN address
  (the host must forward that port). Players on private addresses and players with public addresses are unaffected.
- LAN discovery on the server, opt-in: `-discover` (`RE_PSN_DISCOVER`, off by default) answers a 64-byte DISCOVER sent to the game UDP
  port with a 52-byte DISCOVER_REPLY (TCP and UDP ports, relay flag, player count, name, version), only to private, carrier-grade NAT,
  link-local or loopback senders, under the existing UDP rate limit. `-name` (`RE_PSN_NAME`, at most 24 printable ASCII bytes) sets the
  name shown. `/api/stats` `udp_drops` gains `discover_refused`. With the flag off the server behaves as before. No client helper yet.
- The C client can find servers on the local network: `psnr_discover(port, timeout_ms, out, max)` (`client/psnr.h`) broadcasts one UDP request from its own temporary socket (never a title socket) and returns each server that answers. Only servers started with discovery on reply; it does not cross subnets or client-isolated Wi-Fi, so keep a manual host:port path. On Windows it broadcasts to 255.255.255.255 only; elsewhere it also tries each IPv4 interface's directed broadcast.
- `/api/stats` reports the server's version, whether the relay is on, the payload the relay has carried, the limits in force and
  how many TCP connections were refused (too many from one address, request rate). Aggregate only; same access rules as the rest
  of `/api/stats`. Older servers omit the fields.
- HEARTBEAT may carry the player's own last round trip (`rtt_ms`, 2 bytes). The server shows it next to each member on the status
  page and in `/api/stats` as reported by the player, unverified and for display only; a HEARTBEAT without it, and older servers,
  behave as before. The C client sends it once a title opts in with `psnr_report_rtt(c, 1)` (recommended; off by default), from the
  round trips of its own calls (`psnr_rtt_ms`, `psnr_heartbeat`). The app's Go client does the same with `Conn.ReportRTT(true)`.
- `/api/stats` and the status page count verified players: each public room has `verified` (members whose UDP probe the server
  has accepted on this connection) and the top level has `verified_players`. The page shows "(N verified)" after a room's
  member count when N is above zero. The protocol is unchanged.
- `client/psnr.h` now says what the C client touches: only the sockets it makes itself (the server connection and relay stream connections, which the caller owns), never the title's; connecting and disconnecting are explicit and nothing reconnects by itself; the process-wide effects (`WSAStartup` on Windows, one global connect-error value, plain `send()` so a closed connection can raise `SIGPIPE`, IPv4 only). A comment only; no code changes.
- A test checks that no secret reaches the log: it drives a session at debug level, in both log formats, and looks for each player's UDP token, a room's invite code (right and wrong), a peer key and the status token (right and wrong). The server's behaviour is unchanged.
- Peer keys, for players who connect directly: a client may send PEER_AUTH (0x03, after HELLO, when HELLO_ACK's flag bit 2 says the server issues keys), and then each pair of members who both asked gets one new random 16-byte key from the server in a PEER_KEY push (0xA8) when they share a room. It is the groundwork for tagging packets between peers; no client uses it yet. A client that does not ask is unaffected, and the keys are not confidential on the wire (they travel over the plain TCP connection). HELLO_ACK's flags gain bit 2.
- `-relay-force` (`RE_PSN_RELAY_FORCE`, off by default, needs `-relay`): every stream goes through the relay, even between players
  who could connect directly, so the relay can be tested from one network. The server logs a warning at start and refuses to
  start with it and without `-relay`. It sets the existing ROUTE flag, so clients need no change; datagrams (UDP RELAY) are not
  forced. For testing only: it uses the host's bandwidth.
- The desktop app can find servers on the local network: Find on LAN (`f` in Settings, Ctrl+F while typing the server, also on the
  first start) broadcasts one 64-byte UDP datagram (`PSNR`, type 0x04, a random nonce) from a temporary socket, lists the replies
  (name, address:port, players, relay, version) and puts the chosen address in the server field; typing `host:port` still works.
  The Go client package has `Discover` for it. A server answers only when it was started with `-discover`, and only inside its own subnet; otherwise the list says none found.

### Added
- The status page and `/api/stats` show known games by name: rooms and leaderboards carry a `label` such as
  `[Killzone 3 (BCUS98234, BCES01007)]`, with `game` and `serials` fields, and are sorted by it. A game the table does not
  know keeps its communication ID. The table is embedded in the server (four games so far), never fetched, and the
  `title` field is unchanged. The protocol is unchanged.

### Fixed
- `deploy/re-psn.env.example` now lists `RE_PSN_DISCOVER`, `RE_PSN_NAME` and `RE_PSN_LAN_PUBLIC_ADDR`, and the old names of those and of `-relay-force`, so `scripts/check-deploy.sh` (run by the release build) passes again. A test now runs that check, so a flag added without its example line fails in CI.
- The start log line for LAN discovery ("lan discovery on") no longer shows the server name quoted twice; the name is logged as a plain
  attribute, quoted once by the log format. A test checks it in text and JSON.
- The advice for the UDP PROBE (`client/psnr.h`, `docs/api.md`) is now concrete: send one about every 15 seconds while the title is
  online, from its own P2P socket, because some home gateways drop an idle UDP mapping after 30 seconds. Comments and docs only.
- `re-psn -h` no longer prints the status token. The `-http-token` default was read from `RE_PSN_HTTP_TOKEN` (or the old
  `PSNR_HTTP_TOKEN`), so the usage text showed it, and a pasted `-h` or a CI log could leak it. The flag's own default is now
  empty, as for `-db`, and the variable is read after the command line is parsed. The token is still taken from either
  variable or the flag, the flag still wins, and nothing else about the status page changes. If you ever ran `-h` with the
  token set and kept the output, rotate the token.

## [0.4.0] - 2026-11-09

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
- Bounded per-source UDP admission (per IPv4 address, per IPv6 /64) with `-max-udp-rate` / `RE_PSN_MAX_UDP_RATE` (default 1000 packets/second,
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
  JavaScript both tables remain visible and reload as before. The status token is unchanged; the fields of `/api/stats`
  are unchanged by this page (the `udp_drops` counters are a separate addition).
  Embedded assets normalize line endings before rendering and CSP hashing. Polling preserves full 64-bit score and
  identifier values without changing their JSON types on the server.
- An offline repository check for tracked root/top-level layout, local Markdown links and anchors, and static
  workflow/Dependabot input paths. Run `python3 tools/repo_check.py`; CI runs it with the documentation checks.
- A focused loopback check: `python3 tools/loopback.py` runs the existing native C-client and Go app-client conformance
  tests against temporary loopback servers. Required skips and timeouts fail; `--race` is optional. See `docs/testing.md`.
- Numbered file-style output in the website's terminal view: a header, frame, line-number gutter, syntax-style colours
  and wrapped continuation rows with an empty gutter. Headers and numbers are excluded from text selection. See
  `docs/website.md` for controls and browser-test instructions.
- Optional install scripts: `scripts/install.sh` (POSIX sh; Linux amd64 and arm64) and `scripts/install.ps1` (Windows amd64;
  written for PowerShell 5.1 and 7, tested on 7 only) install the server for the current user from a GitHub release, the newest one (pre-releases included)
  or a pinned `--version` / `-Version`. They check the download against `SHA256SUMS` and stop before installing anything on a
  mismatch or a missing line, need no sudo or administrator rights, edit no shell profile, and change the user PATH only with
  `-AddToPath` on Windows. macOS is refused (there are no macOS builds). `--dry-run` / `-DryRun` downloads nothing. Tested
  with `scripts/test-install.sh` and `scripts/test-install.ps1` against a fake release. See `docs/installing.md`.
- `.github/FUNDING.yml`, which gives the repository a sponsor link (Ko-fi).
- Tests for room kick behavior, contributed by Tzeentchnet.

### Changed
- The website's pager trial is removed: the `pager` command, its keys, buttons and status bar, and the saved pager
  setting are gone, and long output is shown in full in the scrolling output pane. Numbered file output is unchanged.
- The README install section recommends the release archives and shows an optional quick install: the installer is
  fetched from a pinned revision of the repository, its hash is checked, and it is read before it is run.
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
