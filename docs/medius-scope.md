# Medius scope and candidate profile

This is a scope and evidence record, not an implementation or a compatibility claim. The first candidate is **Killzone
3 US `BCUS98234`, update `1.14`, Medius application ID `23044`**. Compatibility is **not tested and not supported**.
No Medius profile is implemented or supported in re-psn.

## Source observations

- The existing re-psn path uses the psnr protocol for rooms and leaderboards; the proposed Medius frontend is separate
  (see [architecture](architecture.md#planned-title-profile-specific-medius-frontend)).
- [Project Helghast's network evidence at a pinned revision](https://github.com/undecuple/project-helghast/blob/221f690330b2761e4cd2b7052737b680921f2348/docs/NETWORK-EVIDENCE.md)
  names the candidate and app ID. Its game-free host tests use generated inputs and substituted sockets and transport,
  not a game, real ticket, certificate, capture or server. The reported earlier multiplayer menu was not reproduced
  there and did not identify a title version. These are source reports, not a re-psn interoperability test.
- That evidence describes a diagnostic with modelled responses, not a verified exchange with a 1.14 client. The app ID
  is named by the source, not established from a verified 1.14 network exchange.

## Model and scope assumptions

- A selected title/build profile could use separate discovery, session/login and lobby sockets, possibly with a data
  connection. A logical session may span them; service order, state transfer and data-plane requirements need evidence.
- The [roadmap scope](../ROADMAP.md#scope) permits only explicitly enabled, private self-hosted Medius-compatible
  endpoints. Initial synthetic tests are loopback-only. A future host must explicitly select reachable local/private
  endpoints and reject invalid or unapproved addresses; loopback cannot be advertised as reachable from another host.
  Existing psnr behavior stays unchanged.
- Only a locally generated, explicitly unauthenticated synthetic TicketLogin envelope for a named, selected profile is
  permitted. It is not a real PSN ticket or authentication, and a participant can claim another name. A synthetic marker
  cannot prove origin; controlled inputs are required. Do not accept real tickets or ticket files. The Medius path must
  not log or persist ticket contents, session grants, player identifiers, packet bodies or their hashes.
- SVO is outside re-psn. If evidence shows it is needed, a synthetic companion needs a separate scope decision and
  review; neither component may contact or proxy live Sony or publisher services.
- DNAS-bearing requests, including mixed or compound requests, must be rejected in full before any operation or state
  change; there is no DNAS emulation or fake success.
- Future implementations must explicitly fail for unknown or unsupported profiles, versions, application IDs and
  messages. Where a wire layout is not established, do not claim to validate its version or identity.

## Unknowns and gates

- The candidate's real-client protocol version, app-ID encoding, handshake, encryption, service order and TicketLogin
  request layout have not been independently established. Synthetic tests cannot establish any of them.
- Whether 1.14 needs SVO, DNAS, genuine PSN authentication, a publisher service or real secrets is unknown. If an
  excluded item is required, the real-client path is out of scope, not something to fake or bypass.
- The data plane, world and membership semantics, authorization, NAT and any DME or peer-to-peer requirement are
  unknown. Do not equate Medius worlds with psnr rooms or assume reuse of psnr NAT traversal or relay.
- A controlled, authorized exact-build run and separately evidenced two-client lifecycle, isolation and data path
  would be needed before any compatibility claim. Until then, the candidate remains not tested and not supported.
