# Security

re-psn handles player data: online IDs, and each player's IP address and P2P
port. It hands that data to the other players in the same room, because they
need it to connect to each other. Anyone who joins your room learns your
address, the same as on real PSN P2P.

## What to know before you run it

- **No authentication.** Anyone who can reach TCP 36100 can join, and can claim
  any online ID. Run it for people you'd let onto your network: a LAN, a
  tailnet, or friends.
- **The status page** (`-http`, off by default; `127.0.0.1:36101` is the usual address) lists every
  connected player's online ID. Don't expose it publicly. If you bind it
  beyond localhost, set `-http-token`; the server warns when you don't.
- **The relay** (`-relay`, off by default) carries traffic between players
  in the same room. It can't tell what the traffic is, but it does see how
  much there is and between whom, and it uses the host's bandwidth.
- **Nothing is stored on disk**, apart from the log if you set `-log-file`.
- **Players behind one address share names.** Players who connect from the same public address (carrier-grade NAT, a
  household NAT, or a TCP proxy used without `-proxy-protocol`) can take over each other's online IDs: a HELLO with a name
  another connection holds from that address makes the server close the old connection and accept the new one
  ([docs/api.md](docs/api.md), HELLO). Behind such a proxy, `-max-conns-per-addr` counts every player together, so the
  limit applies to the whole server ([docs/running.md](docs/running.md#behind-a-reverse-proxy)).

## What this fork changes

See [docs/running.md](docs/running.md) for the flags.

- Room blobs are capped at 4096 bytes, a player can be in 8 rooms at once, one address can hold 32 connections, the
  server holds at most 4096 connections in all, and a connection that sends more than 200 requests a second is closed
  (`-max-player-rooms`, `-max-conns-per-addr`, `-max-conns`, `-max-req-rate`; 0 turns a limit off).
- UDP admission defaults to 1000 packets/second per source IP with the same burst, shared by valid and invalid probes
  and relay datagrams. A bounded table holds at most 4096 sources and expires idle entries. Players behind one NAT
  share the allowance; `-max-udp-rate 0` disables it. Aggregate drop counters add no source labels. This does not
  bound relay bytes/streams or protect a saturated network link; see [UDP guardrails](docs/udp-guardrails.md).
- `-http-token` protects the status page and `/api/stats`.
- A UDP probe or relayed datagram must come from the player's TCP address, so a stolen token or a forged source can't
  point other players' traffic at a third party. It can't stop someone who sends from the player's own address, and it
  refuses players whose UDP leaves from another address; `-udp-any-source` turns it off. See [docs/running.md](docs/running.md#udp-source-address).

## Reporting a vulnerability

> [!IMPORTANT]
> To report a vulnerability, use "Report a vulnerability" under this repository's Security tab (a private advisory).
> Please don't use a public issue.

A problem that is also in sp00nznet/psnr should be reported there as well.
