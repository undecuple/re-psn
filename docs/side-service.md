# Running re-psn beside a title's own server

Some titles run their own game server for login, lobby and matchmaking. re-psn can sit next to it as a side service for the
platform layer (rooms, leaderboards, peer connection help), or be left out. It never takes over a title's networking: nothing
connects to it until the title's code calls `psnr_connect`, and a title that never calls it sends nothing.

This page states what holds today, from the code at this revision. It adds no feature.

## What the title must be able to reach

| Traffic | Port | When |
|---|---|---|
| TCP to the re-psn server | `36100` by default (`-addr`, or `-tcp-addr`) | Always, for any re-psn use |
| UDP to the re-psn server | the same port by default (`-udp-addr`) | Only for the NAT probe and UDP relay packets |
| UDP between players | the title's own peer port | Only for direct peer traffic |

A title that needs rooms and leaderboards only needs the TCP port. If you filter a title's outgoing traffic, allow the one TCP
address of your re-psn server and nothing else. Add UDP to that server only when the title uses the probe, hole punching or the
relay. The server always listens on UDP; there is no switch to turn that off. A title that sends no UDP to it costs nothing,
and a firewall rule that drops it does not affect rooms or leaderboards.

## If the server is down or refuses the player

`psnr_connect` returns `NULL` when the server cannot be reached or refuses the HELLO. It blocks while it resolves the name,
connects and waits for the reply, with no deadline of its own, so call it from a worker thread and give up on your own timer.
`psnr_connect_error()` holds the last HELLO refusal, for example that another player has the name; after a DNS or TCP failure it
can keep an earlier code, so do not read it as the cause of every `NULL`.

Nothing else in the title depends on the result. Treat `NULL` as "no re-psn", keep the title's own route, and try again only
when the player asks. The client never reconnects by itself: when the connection is lost `psnr_poll` returns `-1`, and the title
decides whether to call `psnr_connect` again. The server drops a connection that sends nothing for 90 seconds, so poll regularly.

## Keeping the title's own route

- The C client opens only its own TCP connection to the server (and one per relay stream). It does not touch the title's
  sockets, and the UDP peer socket stays the title's. The header says exactly what it does to the process, including the
  Windows `WSAStartup` call and the `SIGPIPE` note; see the top of [`client/psnr.h`](../client/psnr.h).
- Give re-psn its own setting for the server address, off by default, so a title never reaches it unless the player or host
  turns it on. If your title also supports another online route, enable only one at a time and refuse to start online play if
  both are on, instead of picking silently.
- Use the title's real communication ID. Rooms are grouped by it, so two titles on one server do not see each other's rooms.
- Names are per server: one player per name, compared without regard to case. A title that has its own account names can pass
  them through, and the server does not check them against anything.
- Tokens that the server issues (UDP probe tokens, invite codes, peer keys) are kept out of the server log at every level;
  a test covers this. The status token is never logged either.

## What re-psn does not do

- It does not log players in for the title's server, and it has no accounts. Identity is the name sent in HELLO.
- Today the probe, hole punching and relay are server-wide settings, not per title. A host who wants a server without the relay
  leaves `-relay` off; the probe and UDP stay available but unused unless the title sends them.
- The server does not report which features it offers beyond the few HELLO_ACK flags documented in [api.md](api.md).
