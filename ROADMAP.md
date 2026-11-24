# Roadmap

What re-psn has, what is being worked on, and what comes next. Each item starts with its id in
[docs/features.md](docs/features.md), where its status, evidence and notes are. No dates are promised.

## Scope

> [!NOTE]
> The private self-hosting goal comes from sp00nz's [psnr](https://github.com/sp00nznet/psnr); the narrow Medius
> scope below is specific to re-psn. Upstream's own roadmap, including ps3recomp work, is in
> [psnr's ROADMAP.md](https://github.com/sp00nznet/psnr/blob/main/ROADMAP.md).

re-psn is for a group of players running their own server: a LAN, a tailnet, or
friends connecting to one person's machine. The goal is for that group to have
everything a title's online mode needs: matchmaking, friends, invites,
leaderboards, per-player storage.

It is **not** a public PSN replacement, and won't grow into one:
- no public instance and no server directory;
- the game protocol keeps an unauthenticated name; optional desktop identities and per-host friend accounts
  are separate, explicitly registered services, without a public directory;
- no live Sony infrastructure, impersonation of Sony's servers, real PSN authentication or real PSN tickets;
- no DNAS, DRM, commerce, publisher services, or reliance on real keys, credentials or other secrets.

The existing path uses ps3recomp's NP modules and psnr's own protocol. In addition, explicitly opt-in,
disabled-by-default Medius-compatible endpoints for **named title/build profiles** are within scope for controlled
self-hosted groups. This does not change the existing psnr path or make a general PSN service.

The only TicketLogin envelope in scope is a **locally generated, explicitly unauthenticated synthetic** envelope
for an explicitly selected, named profile. It is not PSN authentication, proof of identity, ownership or entitlement.
Names can be impersonated, and a private network is not authentication. Real tickets and ticket files are not accepted.
Unknown or unsupported profiles, versions, application IDs and messages must fail explicitly in any future
implementation, not fall back to generic success.

SVO is outside re-psn. If it proves necessary, a separate synthetic companion requires its own scope decision and
review; it must not proxy live services. If a title requires an excluded service or secret, that path remains out of
scope.

The candidate is Killzone 3 US `BCUS98234`, update `1.14`, Medius application ID `23044`. **Compatibility is not
tested and not supported.** See the [Medius scope and profile](docs/medius-scope.md) for what is observed, modelled
and still unknown.

## Done

Merged into main. Releases v0.2.0 to v0.9.0 are tagged. Some lines name the release they first appear in; a line that says it is on main only is not in a tagged release.

[Rooms and matchmaking](docs/features.md#rooms-and-matchmaking)
- [rooms-basic] Create, search, join and leave rooms, with room messages, owner kicks and owner handoff.
- [rooms-data] Room and member data, and room flags that searchers see.
- [rooms-codes] Invite-only rooms with an 8-character code, hidden from search and the status page.

[Leaderboards](docs/features.md#leaderboards) and [players](docs/features.md#players)
- [scores-boards] Leaderboards with a best score per player, kept in memory.
- [store-interface] A storage layer with an in-memory backend and a `-db` setting, changing nothing by default.
- [players-names] One player per name on a server; a second player cannot take a name in use.

[Limits](docs/features.md#limits), [logging](docs/features.md#logging) and the [status page](docs/features.md#status-page)
- [limits-requests] Limits on rooms, connections, request rate and room data size, each refusal logged.
- [logs-levels] Log levels, text or JSON output, and an optional log file.
- [status-page] A terminal-style status page and `/api/stats`, off unless `-http` is set, with an optional token. Main adds Rooms/Leaderboards tabs, System/Light/Dark and browser-local text size, spacing and custom colours; see docs/running.md.
- [status-games] `/api/stats` and the status page label rooms and leaderboards with the game's name and serials, from a small table embedded in the server; a game the table does not know keeps its communication ID. In v0.5.0.
- [status-verified] Each public room reports how many members have a verified UDP probe, shown as `(N verified)`; this counts accepted probes, not who the players are. In v0.5.0.
- [status-server-state] `/api/stats` reports the version, relay state, relay payload carried, limits in force and refused TCP connections, as aggregate counts with no address or player. In v0.5.0.
- [status-latency] The server shows each member's own reported round trip, sent in HEARTBEAT once a title opts in; it is reported by the player, not verified, and for display only. In v0.5.0.
- [admin-api] Partial: an experimental opt-in read-only admin listener with one route, `GET /admin/v1/state`, for a read token, first in v0.6.0; it has no action, ban or limit change, and no app view yet.

[Reverse proxy](docs/features.md#reverse-proxy), [deployment](docs/features.md#deployment) and [settings](docs/features.md#settings)
- [proxy-support] PROXY protocol v1 and v2 and trusted proxies, so the real client address counts.
- [ha-restart] A data-free health endpoint and bounded shutdown of client/relay sockets, with restart documentation; in v0.4.0. Rooms and scores remain in memory.
- [deploy-examples] A settings file, a systemd unit and a Traefik example, shipped in the release archives.
- [deploy-nginx-haproxy] nginx and HAProxy examples, not yet run against the server.
- [deploy-caddy] A stock Caddy 2 status-page example, in v0.4.0, not run or validated with Caddy; game TCP and UDP stay direct.
- [deploy-fail2ban] Disabled fail2ban example for HTTP status-token failures, with offline filter checks; live journal/firewall enforcement remains unverified.
- [deploy-container] A Dockerfile and compose file, run under Podman.
- [settings-env] Every flag as a `RE_PSN_*` variable, with the old `PSNR_*` names as aliases.

[Network](docs/features.md#network) and the [client](docs/features.md#client)
- [net-punch] Address discovery and hole punching over UDP, reproduced in the NAT lab.
- [net-relay] An optional relay for players who cannot reach each other directly.
- [net-udp-guard] UDP packets accepted only from the address of the player's TCP connection.
- [net-guardrails] Partial: bounded per-source UDP rate limits and aggregate drop counters, in v0.4.0. Per-player relay byte/stream budgets, failed-token blocking and UDP fuzz coverage remain planned.
- [net-relay-force] `-relay-force`, off by default and needing `-relay`, sends every stream through the relay for testing from one network, using the host's bandwidth. In v0.5.0.
- [net-no-peer-titles] `-no-peer-titles` lists titles that use no peer traffic: their players get no UDP probe reply or relay but still use rooms and scores. In v0.6.0.
- [net-caps] `GET_CAPS` after HELLO returns a bit set of what the server offers that player, plus the server version; the C client can parse it. In v0.6.0.
- [net-peer-keys] Partial: opt-in `PEER_AUTH` gives each pair of members who asked a random key in a `PEER_KEY` push; no client uses the keys yet and they travel over plain TCP, so they give no confidentiality. In v0.5.0.
- [net-lan-discover] `-discover`, off by default, answers a LAN discovery request on the game UDP port from private, link-local and loopback senders; server side only. In v0.5.0.
- [net-lan-public-addr] `-lan-public-addr IP`, off by default, gives players on public addresses the host's address and P2P port for a player who connected from a private address. In v0.5.0.
- [client-lan-discover] `psnr_discover` in the C client broadcasts one UDP request and returns the LAN servers that answer; it finds only servers started with `-discover`. In v0.5.0.
- [client-connect-policy] `psnr_set_connect_policy` in the C client registers one callback asked before each TCP connection the client opens, including relay legs; a refusal skips that address. In v0.6.0.
- [client-c] A one-file C client with no dependencies.
- [app-tui] The released terminal app provides a first-run choice, Rooms/Leaderboards, Settings and dark/light themes. An optional HTTP status source adds version, totals, public room members/age and board summaries, with game-protocol fallback. Since v0.7.0 it is the advanced `re-psn-app tui`, with an owned Host pane; see the client work below. It does not launch or join a game.
- [app-client] Partial: a Go client package for the TCP protocol, used by the desktop app and `re-psn-app check`; room data and messages, kicks, invite-only rooms, relay streams and UDP are not in it yet. In v0.3.0.
- [app-lan-discover] Find on LAN in the desktop app lists servers started with `-discover` and fills the server field with the chosen address; typing host:port still works. In v0.5.0.
- [app-server-view] With a status URL set, the desktop app's Status screen has a read-only Server tab showing version, uptime, relay, limits, refusals and relay payload; fields an older server omits are shown as not reported. In v0.5.0.

[Testing and CI](docs/features.md#testing-and-ci), [release](docs/features.md#release) and [documentation](docs/features.md#documentation)
- [test-fuzz] Fuzz tests for the frame parser, the request handlers and the PROXY header.
- [ci-checks] CI with the race detector, vulnerability and secret scans, the NAT lab and code scanning; a focused local loopback runner for the C and Go app clients.
- [release-archives] Release archives with checksums, built by a workflow; partial: published in the v0.2.0 pre-release; the Windows and arm64 binaries are cross-built and not run.
- [release-process] Written release steps and a dry run, then the first tag (v0.2.0, a pre-release); further pre-releases come with no fixed release day.
- [release-appimage] A Linux x86_64 AppImage beside the archives; partial: published in the v0.2.0 pre-release, unsigned, and run on one machine.
- [app-archives] Release builds make desktop app archives for linux/amd64, linux/arm64 and windows/amd64 with checksums; from v0.7.0 each app archive also holds the matching `re-psn-host`, and from v0.8.0 `re-psn-window`. First in v0.5.0.
- [install-script] Install scripts for Linux and Windows that check SHA256SUMS; partial: release assets since v0.4.0 (the published v0.4.0 `install.sh` ran on Linux amd64 into a temporary directory), PowerShell fake-release tests passed on Linux and Windows CI under PowerShell 7, and PowerShell 5.1 remains untested.
- [docs-features] This features list, generated from one data file and checked in CI.
- [docs-diagrams] Architecture and NAT lab diagrams in Mermaid.
- [docs-markdown] Every doc in Markdown with one layout, and diagrams for deploy and Docker.
- [docs-integration] A recomp developer guide covering ps3recomp and direct C integration, a required-tested minimal example, ownership/lifecycle and peer-transport checks; in v0.4.0.
- [repo-check] An offline CI guard for tracked root/top-level layout, local Markdown links/anchors and static workflow/Dependabot input paths; available in v0.4.0.
- [docs-connecting] A page on the ways players connect, with Tailscale and ZeroTier links; neither overlay has been run.
- [docs-readme] A rewritten README with an example from real runs, the features, the settings and credit to upstream.
- [docs-art] A generator for the README banner and terminal images from real runs, in one palette, checked in CI.
- [docs-website] A static project website built from the features list, version, settings table and changelog; partial: published at https://undecuple.github.io/re-psn/ with a dark terminal-style interface, commands, numbered file output and phone tabs. The site is read-only and has one theme; the local client/status page have separate appearance controls. Browser checks cover command entry, selection and narrow layouts; a real keyboard, screen reader, physical phone, self-hosted font and animated diagrams remain unverified or unfinished. See docs/website.md.

On the ps3recomp side (upstream's work):
- **Sockets:** real host sockets behind `PS3_NET_ONLINE`
  ([ps3recomp#196](https://github.com/sp00nznet/ps3recomp/pull/196), merged).
- **Rooms and leaderboards:** Matching2, Score, Lookup and Avc2 over psnr
  ([ps3recomp#200](https://github.com/sp00nznet/ps3recomp/pull/200), merged on 2026-10-07).
- **First online match:** Simpsons Arcade, two instances on one machine, lobby
  to Stage 1 in sync, and later across two machines, with either player behind a NAT
  ([ps3recomp#202](https://github.com/sp00nznet/ps3recomp/pull/202)).

[Sign-in passes](docs/features.md#network)
- [net-pass-relay] Optional `-require-pass relay` makes the relay carry traffic only for players holding a short-lived signed pass checked offline; off by default. No login service exists yet, so nothing can mint a pass outside tests. In v0.9.0.
- [net-pass-connect] Optional `-require-pass connect` makes a connection a player only after AUTH accepts a pass, with a 30 s sign-in deadline; off by default. No login service exists yet, and names are still not authenticated. In v0.9.0.
- [client-pass-auth] The C client's `psnr_auth` and the desktop app's `Conn.Auth` send AUTH with an opaque pass and return the server's answer. No login service and no app sign-in screen exist yet, so nothing outside tests holds a pass. In v0.9.0.

### Desktop app, trophies and friends (v0.3.0 to v0.8.0)

These have shipped (v0.3.0 to v0.8.0) and work in synthetic local checks:

- [client-desktop] Optional Windows/Linux client with independent Play/Host/Both workspaces, owned host start/stop,
  authenticated reopen and paired app/host archives. Ordinary gameplay and standalone hosting remain independent.
- [client-gui] Embedded local browser interface, now the default launch; advanced `tui` remains available. The
  undecuple/me colors/fonts/framing need no frontend dependencies. Browser close and explicit app shutdown have
  different outcomes; see [client operation](docs/client-ui.md).
- [app-appearance] Browser Dark/Light/System, advanced terminal dark/light. Extra presets and terminal system-following remain.
- [app-framework] Working app-managed offline store, private title-scoped C/C++ trophy reporting and encrypted recovery.
  Real-title adapters, Sony trophy-save parsing, automatic platinum, trophy verification and remote sync are not supplied.
- [players-accounts] Portable re-psn identities and explicit authenticated per-host registration. These authenticate key
  control, not gameplay names or provider ownership; restoring a profile does not restore another host's authority.
- [players-friends] Per-host mutually accepted requests, removal and blocks in the client and companion. Live presence,
  game-facing NPBasic integration, invitations and join-a-friend remain absent.

Windows/Linux binaries are built. Linux synthetic browser/owned-host/SDK runs and Windows controller/profile/SDK/reopen
checks under Wine are separately labeled. Wine rejects the server UDP setup with WSAIoctl error 10045; those runs do
not establish native Windows hosting. Native physical Windows/ARM64 execution and real-title support remain unverified.
App installers refuse archives without `re-psn-host` (before v0.7.0); paired replacements are not crash-atomic.

## Now

Nothing is in progress right now; no feature is tagged in-progress.

## Next

In about this order.

### Storage, off unless a host turns it on
- [store-sqlite] SQLite storage, so leaderboards survive a restart.
- [store-backup] Backup and restore steps for each database.

### Running a server
- [ha-failover] Docs and examples for a standby server that takes over the address.
- Finish relay budgets and remaining UDP guardrails; see [Network](docs/features.md#network).
- [ci-more] More CI checks, such as the C client outside Linux.

### Medius profiles
- [medius-transport] Opt-in, profile-specific transport and discovery, beginning with synthetic local tests.
- [medius-lobby] Profile-specific logical sessions and lobby work, including only the synthetic local login above.
- [medius-data] A profile-specific data path, only after its requirements and authorization are evidenced.

### Apps
- Per-game launch profiles and native platform/runtime qualification beyond synthetic Linux/Wine checks.
- [app-themes] Colour presets in the apps, each shipped only once its licence is credited.
- [app-colours] Custom colours with a contrast warning.

### Players and rooms
- [rooms-private] Room passwords, private slots and join by room ID.
- Game-facing friend integration, live presence, invitations and join-a-friend beyond the current opt-in consent lists.
- [net-checks] A connection check that says where a connection fails.

## Later

- [store-postgres] PostgreSQL storage for hosts who bring their own database.
- [store-mariadb] MariaDB storage for hosts who bring their own database.
- [ha-multi] Several servers behind one address, kept on the back burner until one server is not enough.
- [metrics-endpoint] A metrics endpoint for Prometheus, off by default, with no personal data in it.
- [metrics-dashboard] A Grafana dashboard and alert rules built on that endpoint.
- [release-signing] Signed Windows binaries and signed checksums.
- [net-upnp] Opening a player's P2P port on their router automatically.
- [medius-multititle] Consider further Medius title/build profiles separately, with evidence for each.

## Not planned

- [ha-handover] Live room handover: rooms that survive the loss of a server.
- **A public PSN service or directory**, or access to live Sony infrastructure.
- **Real PSN authentication and tickets**, including issuing, verifying or accepting real tickets. TicketLogin is in
  scope only for the explicitly unauthenticated synthetic local envelope described above.
- **SVO in re-psn.** A synthetic companion, if needed, is a separate decision and must not proxy live services.
- **Third-party game servers:** Demonware (Guitar Hero III), GameSpy (MUA,
  Saints Row 2), EA, Quazal. Each would be its own project.
- **DNAS, publisher services, store, commerce, DRM, or real secrets.** No emulation or fake success for DNAS, and no
  dependence on real credentials, keys, tickets or other secrets.
