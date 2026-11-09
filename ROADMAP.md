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
- no accounts or passwords, just an unauthenticated name each player sets;
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

Merged into main. Items are in v0.2.0 or v0.3.0 unless a line says it is on main only.

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

[Reverse proxy](docs/features.md#reverse-proxy), [deployment](docs/features.md#deployment) and [settings](docs/features.md#settings)
- [proxy-support] PROXY protocol v1 and v2 and trusted proxies, so the real client address counts.
- [ha-restart] A data-free health endpoint and bounded shutdown of client/relay sockets, with restart documentation; on main, not released. Rooms and scores remain in memory.
- [deploy-examples] A settings file, a systemd unit and a Traefik example, shipped in the release archives.
- [deploy-nginx-haproxy] nginx and HAProxy examples, not yet run against the server.
- [deploy-caddy] A stock Caddy 2 status-page example on main, not released or run/validated with Caddy; game TCP and UDP stay direct.
- [deploy-fail2ban] Disabled fail2ban example for HTTP status-token failures, with offline filter checks; live journal/firewall enforcement remains unverified.
- [deploy-container] A Dockerfile and compose file, run under Podman.
- [settings-env] Every flag as a `RE_PSN_*` variable, with the old `PSNR_*` names as aliases.

[Network](docs/features.md#network) and the [client](docs/features.md#client)
- [net-punch] Address discovery and hole punching over UDP, reproduced in the NAT lab.
- [net-relay] An optional relay for players who cannot reach each other directly.
- [net-udp-guard] UDP packets accepted only from the address of the player's TCP connection.
- [net-guardrails] Partial: bounded per-source UDP rate limits and aggregate drop counters on main, not released. Per-player relay byte/stream budgets, failed-token blocking and UDP fuzz coverage remain planned.
- [client-c] A one-file C client with no dependencies.
- [app-tui] The shared Windows/Linux terminal app: first-run choice, Rooms/Leaderboards, Settings, dark and light. Linux desktop starts can open a supported terminal; Windows uses its console. An optional HTTP status source adds native totals, public room members/age and board summaries, with game-protocol fallback. Partial: no Host screen or joining yet.

[Testing and CI](docs/features.md#testing-and-ci), [release](docs/features.md#release) and [documentation](docs/features.md#documentation)
- [test-fuzz] Fuzz tests for the frame parser, the request handlers and the PROXY header.
- [ci-checks] CI with the race detector, vulnerability and secret scans, the NAT lab and code scanning; a focused local loopback runner for the C and Go app clients.
- [release-archives] Release archives with checksums, built by a workflow; partial: published in the v0.2.0 pre-release; the Windows and arm64 binaries are cross-built and not run.
- [release-process] Written release steps and a dry run, then the first tag (v0.2.0, a pre-release); further pre-releases come with no fixed release day.
- [release-appimage] A Linux x86_64 AppImage beside the archives; partial: published in the v0.2.0 pre-release, unsigned, and run on one machine.
- [install-script] Install scripts for Linux and Windows that check SHA256SUMS; partial: on main and release assets from the next release (none has shipped them yet), PowerShell fake-release tests passed on Linux and Windows CI under PowerShell 7, and PowerShell 5.1 remains untested.
- [docs-features] This features list, generated from one data file and checked in CI.
- [docs-diagrams] Architecture and NAT lab diagrams in Mermaid.
- [docs-markdown] Every doc in Markdown with one layout, and diagrams for deploy and Docker.
- [docs-integration] A recomp developer guide covering ps3recomp and direct C integration, a required-tested minimal example, ownership/lifecycle and peer-transport checks; on main, not released.
- [repo-check] An offline CI guard for tracked root/top-level layout, local Markdown links/anchors and static workflow/Dependabot input paths; available on main.
- [docs-connecting] A page on the ways players connect, with Tailscale and ZeroTier links; neither overlay has been run.
- [docs-readme] A rewritten README with an example from real runs, the features, the settings and credit to upstream.
- [docs-art] A generator for the README banner and terminal images from real runs, in one palette, checked in CI.
- [docs-website] A static project website built from the features list, version, settings table and changelog; partial: published at https://undecuple.github.io/re-psn/ with a terminal-style interface (commands, numbered file output, phone tabs and a System / Light / Dark switch). Browser checks cover command entry, selection, themes and narrow layouts; a real keyboard, screen reader, physical phone, self-hosted font and animated diagrams remain unverified or unfinished. See docs/website.md for controls and contributor checks.

On the ps3recomp side (upstream's work):
- **Sockets:** real host sockets behind `PS3_NET_ONLINE`
  ([ps3recomp#196](https://github.com/sp00nznet/ps3recomp/pull/196), merged).
- **Rooms and leaderboards:** Matching2, Score, Lookup and Avc2 over psnr
  ([ps3recomp#200](https://github.com/sp00nznet/ps3recomp/pull/200), draft).
- **First online match:** Simpsons Arcade, two instances on one machine, lobby
  to Stage 1 in sync, and later across two machines, with either player behind a NAT
  ([ps3recomp#202](https://github.com/sp00nznet/ps3recomp/pull/202)).

## Now

Nothing is in progress at the moment; the items under Next come first.

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
- [client-desktop] A terminal app for players and hosts, with launch profiles and a host mode.
- [client-gui] A graphical app and a local web UI with the same screens.
- [app-themes] Colour presets in the apps, each shipped only once its licence is credited.
- [app-appearance] Dark, light or follow the system.
- [app-colours] Custom colours with a contrast warning.

### Players and rooms
- [rooms-private] Room passwords, private slots and join by room ID.
- [players-friends] Friends, presence and invites for everyone on the same server.
- [net-checks] A connection check that says where a connection fails.

## Later

- [store-postgres] PostgreSQL storage for hosts who bring their own database.
- [store-mariadb] MariaDB storage for hosts who bring their own database.
- [ha-multi] Several servers behind one address, kept on the back burner until one server is not enough.
- [metrics-endpoint] A metrics endpoint for Prometheus, off by default, with no personal data in it.
- [metrics-dashboard] A Grafana dashboard and alert rules built on that endpoint.
- [players-accounts] Optional sign-in, which needs the scope above changed first.
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
