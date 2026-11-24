# Protocol specification

This page specifies the wire protocol between a game client and a re-psn server, precisely enough to write a second
client or server from it. [api.md](api.md) is the guide to the same protocol: it explains why and gives advice. This page
is the reference: byte layouts, the order things happen in, what is refused and with which code. The two are meant to
agree. If either disagrees with what the server in `server/` does, that is a defect in the page: please open an issue.

re-psn is unofficial fan work. There is no public instance and the server has no account of its own: you run your own server. Nothing
here is a Sony protocol, and the protocol is not related to any title's own online services.

## Contents

- [1. Status and scope](#1-status-and-scope)
- [2. Conventions](#2-conventions)
- [3. Framing](#3-framing)
- [4. Message catalogue](#4-message-catalogue)
- [5. Connection state machine](#5-connection-state-machine)
- [6. Session messages](#6-session-messages)
- [7. Rooms](#7-rooms)
- [8. Scores](#8-scores)
- [9. UDP](#9-udp)
- [10. Relay](#10-relay)
- [11. Errors](#11-errors)
- [12. Limits](#12-limits)
- [13. Versioning and compatibility](#13-versioning-and-compatibility)
- [14. Security considerations](#14-security-considerations)
- [15. Conformance](#15-conformance)
- [16. What this page does not cover](#16-what-this-page-does-not-cover)
- [Appendix A. Worked bytes](#appendix-a-worked-bytes)

## 1. Status and scope

This page describes the server in this repository at version 0.9.0 (`VERSION`, `server/main.go`). It covers the game
protocol: TCP frames, the UDP control packets that share the game port, and the relay. It does not cover the HTTP
status page, `/api/stats` or the [admin API](admin-api.md), the PROXY protocol header, the desktop app's local broker
([app-framework-api.md](app-framework-api.md)), or the command-line flags except where a flag changes what is on the
wire.

The words MUST, MUST NOT, SHOULD, SHOULD NOT and MAY are used as in RFC 2119. A requirement on "a client" applies to any
program that connects to a server. A requirement on "a server" applies to anything claiming to answer as re-psn does.
Where this page says what the reference server does without a requirement word, that is observed behavior, and a
compatible server SHOULD do the same.

Marks used below:

- **(untested)** the behavior is read from the source and no test in the repository pins it, see [section 15](#15-conformance).
- A flag in backticks, such as `-relay`, is a server setting. See [running.md](running.md).

## 2. Conventions

- Every integer is big-endian. `u8`, `u16`, `u32` and `u64` are unsigned; `s64` is two's complement in 8 bytes.
- `[n]` is a fixed field of `n` bytes, padded with NUL (0x00). A reader takes the `n` bytes and cuts at the first NUL. A
  value that fills all `n` bytes has no terminator. The server checks no character set. A client SHOULD send printable
  ASCII only; the C client replaces bytes outside 0x20 to 0x7E with `?` when it copies a name out of a reply.
- `blob` is `u16 length` followed by that many bytes. A blob may be empty.
- `ip [4]` is an IPv4 address in network order. The protocol carries no IPv6 address (see [section 9.5](#95-ipv6)).
- `req` is a `u32` request id. The client chooses it and the server echoes it in the reply. The server does not check
  that ids are unique and accepts 0. A client SHOULD NOT use 0, because the C client reserves it to mean "push".
- A "title" is the 12-byte `comm_id` a client gives in HELLO. Rooms, scores and relay permission are all scoped to it.
- A "player" is a registered connection. It has a `user_id` and a `token` (see [6.1](#61-hello-0x01)). The server takes
  the `user_id` from a counter over every accepted connection, from 1, for the life of the process: connections that
  never say HELLO and stream legs use up ids too. Ids are unique and nonzero but not dense, and a player's first id is
  not necessarily 1.

## 3. Framing

One TCP connection carries a stream of frames:

```text
u8 type | u16 length | payload (length bytes)
```

`length` counts the payload only. The largest payload is 65535 bytes, so the largest frame is 65538 bytes. A sender
MUST NOT send more; a `u16` cannot declare it.

Requests (client to server) and replies (server to client) start their payload with `req u32`. Pushes (server to client,
types 0xA0 to 0xAF) have no `req` and start with their own fields. The "after req" columns below do not repeat the
`req`; the "payload" columns do count it.

A server reads `req` first. A request whose payload is shorter than 4 bytes is dropped: no reply, no error, the
connection stays open, and the frame still counts toward the request rate limit (untested). A client SHOULD drop a
reply frame (type 0x80 or higher, not 0xA0 to 0xAF) whose payload is shorter than 4 bytes; a server MUST NOT send one.

Bytes after the last field of a request are ignored by every handler except AUTH, which refuses them. A client MAY
therefore rely on a server ignoring extra bytes at the end of a request, but SHOULD NOT send any.

A client MUST ignore push types it does not know. A client MUST ignore bits it does not know in HELLO_ACK `flags`, ROUTE
`flags` and CAPS `bits`.

## 4. Message catalogue

### 4.1 Requests

The first column is the type byte. Replies are listed in [4.2](#42-replies).

| Type | Name | After `req` | Reply |
|---|---|---|---|
| 0x01 | HELLO | `comm_id [12]` `online_id [16]` `p2p_port u16` | HELLO_ACK |
| 0x02 | HEARTBEAT | optional `rtt_ms u16` | none |
| 0x03 | PEER_AUTH | none | OK |
| 0x04 | GET_CAPS | none | CAPS |
| 0x05 | AUTH | `version u8` `pass blob` | OK |
| 0x10 | CREATE_ROOM | `max u8` `flags u32` `external blob` `internal blob` `member_data blob` | ROOM_JOINED |
| 0x11 | SEARCH_ROOMS | `start u16` `max u16` | ROOM_LIST |
| 0x12 | JOIN_ROOM | `room_id u64` `member_data blob` | ROOM_JOINED |
| 0x13 | LEAVE_ROOM | `room_id u64` | OK |
| 0x14 | SET_ROOM_DATA | `room_id u64` `which u8` `data blob` | OK |
| 0x15 | ROOM_MESSAGE | `room_id u64` `to u16` `data blob` | OK |
| 0x16 | KICK_MEMBER | `room_id u64` `member_id u16` | OK |
| 0x17 | SET_ROOM_ACCESS | `room_id u64` `mode u8` | ROOM_ACCESS |
| 0x18 | JOIN_BY_CODE | `code [8]` `member_data blob` | ROOM_JOINED |
| 0x19 | GET_ROOM_ACCESS | `room_id u64` | ROOM_ACCESS |
| 0x20 | RECORD_SCORE | `board u32` `order u8` `score s64` `comment blob` | SCORE_RECORDED |
| 0x21 | GET_RANKING | `board u32` `start u32` `count u16` | RANKING |
| 0x22 | GET_RANKING_BY_ID | `board u32` `n u16` then `n` x `online_id [16]` | RANKING |
| 0x30 | STREAM_CONNECT | `user_id u32` `token u32` `to_user u32` `vport u16` | STREAM_READY (first frame of a new connection only) |
| 0x31 | STREAM_ACCEPT | `user_id u32` `token u32` `stream_id u32` | STREAM_READY (first frame of a new connection only) |

Any failed request is answered with ERROR (0x8F) with the same `req`, except where [section 3](#3-framing) or the
request's own section says there is no reply.

### 4.2 Replies

| Type | Name | After `req` | Payload bytes |
|---|---|---|---|
| 0x81 | HELLO_ACK | `user_id u32` `public_ip [4]` `token u32` `flags u8` | 17 |
| 0x8F | ERROR | `code u32` | 8 |
| 0x90 | ROOM_JOINED | `room_id u64` `my_member u16` `owner u16` `max u8` `flags u32` `external blob` `internal blob` `count u8` then `count` x member | 26 + blobs + members |
| 0x91 | ROOM_LIST | `total u16` `count u16` then `count` x entry | 8 + entries |
| 0x93 | OK | none; in answer to an AUTH under connect mode also `token u32` | 4 (8 for that AUTH) |
| 0x94 | CAPS | `bits u32` `version blob` | 10 + length of version |
| 0x96 | STREAM_READY | none | 4 |
| 0x97 | ROOM_ACCESS | `mode u8` `code [8]` | 13 |
| 0xB0 | SCORE_RECORDED | `rank u32` | 8 |
| 0xB1 | RANKING | `total u32` `count u16` then `count` x row | 10 + rows |

`member` (inside ROOM_JOINED and MEMBER_JOINED): `member_id u16` `user_id u32` `online_id [16]` `ip [4]` `p2p_port u16`
`owner u8` `data blob`. It is 29 bytes plus the blob, 31 with the blob's length field.

`entry` (ROOM_LIST): `room_id u64` `owner online_id [16]` `cur u8` `max u8` `flags u32` `external blob`.

`row` (RANKING): `rank u32` `online_id [16]` `score s64` `comment blob` `recorded_unix u64`.

Types 0x92 and 0x95 are unassigned.

HELLO_ACK `flags`:

| Bit | Value | Set when |
|---|---|---|
| 0 | 0x01 | `-relay` is on and the title is not in `-no-peer-titles` |
| 1 | 0x02 | always |
| 2 | 0x04 | always: the server issues peer keys |
| 3 | 0x08 | `-require-pass connect` is on; `token` is then 0 |
| 4 to 7 | | never set today |

A server with no optional settings answers 0x06.

### 4.3 Pushes

Sent by the server with no request. They carry no `req`.

| Type | Name | Layout | Payload bytes |
|---|---|---|---|
| 0xA1 | MEMBER_JOINED | `room_id u64` `member` | 8 + member |
| 0xA2 | MEMBER_LEFT | `room_id u64` `member_id u16` `owner u16` | 12 |
| 0xA3 | ROOM_DATA | `room_id u64` `which u8` `data blob` | 11 + data |
| 0xA4 | ROOM_MSG | `room_id u64` `from u16` `to u16` `data blob` | 14 + data |
| 0xA5 | KICKED | `room_id u64` | 8 |
| 0xA6 | STREAM_OFFER | `stream_id u32` `from_user u32` `vport u16` | 10 |
| 0xA7 | ROUTE | `room_id u64` `user u32` `flags u8` | 13 |
| 0xA8 | PEER_KEY | `room_id u64` `user u32` `key [16]` | 28 |

ROUTE `flags` bit 0: a stream to that user goes through the relay. Types 0xA0 and 0xA9 to 0xAF are unassigned. A client
SHOULD treat any type from 0xA0 to 0xAF as a push with no `req`, and any other type as a reply with one.

### 4.4 Type map

| Range | Use |
|---|---|
| 0x01 to 0x05 | session requests |
| 0x10 to 0x19 | room requests |
| 0x20 to 0x22 | score requests |
| 0x30 to 0x31 | stream legs |
| 0x81, 0x8F | HELLO_ACK, ERROR |
| 0x90 to 0x97 | room, caps and stream replies |
| 0xA1 to 0xA8 | pushes |
| 0xB0 to 0xB1 | score replies |

A request type the server does not know, and a reply or push type sent to it as a request, gets ERROR 1 after HELLO and
ERROR 7 before HELLO. Under connect mode, before AUTH, it gets ERROR 10. The server does not close the connection.

## 5. Connection state machine

```text
accept
  |  caps at accept: -max-conns, -max-conns-per-addr (IPv6 counts as its /64),
  |  and under connect mode the unsigned caps. Over a cap the connection is closed, no reply.
  |  A PROXY header comes first when the server is configured for one.
  v
[NEW]
  | first frame is 0x30 or 0x31 ......... [STREAM LEG], see section 10
  | HEARTBEAT, or any request but HELLO ... ERROR 7, state unchanged
  | HELLO, comm_id empty ................. ERROR 1, state unchanged (HELLO may be retried)
  | HELLO, name held from another address  ERROR 8, state unchanged (normal mode; see 6.5 for connect mode)
  v
  +-- normal mode ----------------------------+   +-- connect mode (-require-pass connect) --+
  | HELLO registers the player, HELLO_ACK     |   | HELLO parks the names and port only;     |
  |  [REGISTERED]                             |   | HELLO_ACK has token 0 and flag bit 3.    |
  |                                           |   |  [HELLO SEEN]                            |
  |                                           |   |  only AUTH, HEARTBEAT, GET_CAPS answered;|
  |                                           |   |  anything else ERROR 10; 30 s from accept|
  |                                           |   | AUTH ok: name check, register, listed,   |
  |                                           |   |  OK carries token                        |
  |                                           |   |  [REGISTERED]                            |
  +-------------------------------------------+   +------------------------------------------+
  v
[REGISTERED]  all requests; refresh AUTH allowed; closes on any condition in 12.2
  v
[CLOSED]  leaves every room (MEMBER_LEFT to the others), drops UDP endpoint and account slot
```

- HELLO is sent once. A second HELLO gets ERROR 1 and changes nothing, in both modes.
- Under connect mode, a request before HELLO still gets ERROR 7, AUTH included.
- The control connection and a stream leg are different kinds of connection. 0x30 and 0x31 are legs only as the first
  frame on a new connection. Later on a control connection they are unknown types and get ERROR 1.

## 6. Session messages

### 6.1 HELLO (0x01)

Request after `req`: `comm_id [12]` `online_id [16]` `p2p_port u16`. The payload is 30 bytes after `req` (34 counting it). A shorter one gets ERROR 1.

Rules:

- `comm_id` MUST NOT be empty (ERROR 1). `online_id` MAY be empty; the server accepts it. The Go client refuses to send
  an empty one and the C client does not.
- `p2p_port` is the UDP port of the title's own P2P socket. The server hands it to other members as part of their
  member entry.
- Name check: another connection with the same `online_id`, compared case-insensitively, from another address gets
  ERROR 8. From the same address (IPv4, or IPv6 as the exact address) the old connection is closed and the new one is
  accepted. An empty name is compared like any other.
- Score boards key on the exact `online_id` string, not the case-folded one ([section 8](#8-scores)).
- A second HELLO on a connection gets ERROR 1.

Reply: HELLO_ACK, 17 bytes. `user_id` is nonzero and unique among live connections (see [section 2](#2-conventions)). `public_ip` is the address the server sees the TCP connection come
from; it is 0.0.0.0 for an IPv6 client. `token` is a random `u32` ORed with 1, so it is never 0 outside connect mode; in
connect mode it is 0 until AUTH succeeds. The token is the secret a client later shows in UDP PROBE and RELAY datagrams
and in stream legs.

A client MUST NOT assume the C client's tolerance of short HELLO_ACKs: the C client reads `token` and `flags` only if
the body is long enough, and the Go client needs all 13 bytes after `req`. A server MUST send all 17 payload bytes.

### 6.2 HEARTBEAT (0x02)

After `req`: optional `rtt_ms u16`. No reply after HELLO. Any frame resets the 90 s idle timer, so HEARTBEAT is the
cheapest way to do it; the C client sends one every 20 s.

A body of 2 or more bytes starts with `rtt_ms`, the client's own measure of round-trip time. A value of 0xFFFF, or over
60000, clears the report; otherwise the server keeps it and shows it on the status page for 3 minutes. A 1-byte body is
ignored. Bytes after the first two are ignored.

Before HELLO, HEARTBEAT gets ERROR 7 like any other request (in connect mode too).

### 6.3 GET_CAPS (0x04)

Allowed after HELLO, and in connect mode before AUTH. Reply CAPS: `bits u32`, `version blob`. `version` is the server's
software version text (here `0.9.0`).

| Bit | Value | Name | Set when |
|---|---|---|---|
| 0 | 0x01 | rooms | always |
| 1 | 0x02 | scores | always |
| 2 | 0x04 | room codes | always |
| 3 | 0x08 | peer keys | always |
| 4 | 0x10 | probe | the title is not in `-no-peer-titles` |
| 5 | 0x20 | relay | `-relay`, the title is not in `-no-peer-titles`, and, under `-require-pass relay`, this player's pass has the `relay` scope and has not lapsed |
| 6 | 0x40 | discover | `-discover` |
| 7 | 0x80 | pass needed for relay | `-relay`, `-require-pass relay`, and the title is not in `-no-peer-titles` |

Bit 7 needs `-relay` as well as `-require-pass relay`. Under connect mode, before AUTH, `bits` is 0x80 or 0 and the
version blob is empty; the whole set is returned after AUTH.

Only bit 5 says whether this player can use the relay now. A client MUST NOT treat any capability bit, or an ERROR 1 in
answer to GET_CAPS, as proof of anything: both can be forged on a plain TCP connection.

### 6.4 PEER_AUTH (0x03) and PEER_KEY

A client sends PEER_AUTH (no body) after HELLO if it wants peer keys, only if HELLO_ACK flag bit 2 is set. The reply is
OK. Sending it twice changes nothing.

When two members who have both sent PEER_AUTH are in a room together, each gets a PEER_KEY push with the same new random
16-byte key about the other: when the second one joins, or when the second one asks. A member with several peers gets a
different key for each pair. The key is new for every join. The server does not keep it or use it for anything else.
Sending is queued after MEMBER_JOINED and ROUTE for that join. See [14.6](#146-peer-keys) for what this does and does
not give.

### 6.5 AUTH (0x05)

After `req`: `version u8` (1; 2 is reserved and refused) and `pass blob` (1 to 2048 bytes). The payload MUST contain
nothing after the blob; extra bytes refuse the pass.

Reply: OK (4 payload bytes; 8 under connect mode on the first AUTH, with the `token u32` appended; a refresh returns the
same token), or ERROR 10, 1, 8, 7. The third refusal in a row is the exception: the server queues the ERROR and closes the
connection at once, so a client may read the ERROR or only end of stream, and MUST be ready for the close (in testing
no ERROR was read).

Order of checks:

1. A server with no pass settings answers ERROR 1. It is not counted as a refusal.
2. The body must be exactly `version`, `pass blob`; `version` must be 1; the pass must verify (format, signature,
   `iss`, `aud`, `sub`, scopes, `iat` and `exp`, revoked file). Otherwise ERROR 10, counted as a refusal.
3. Under connect mode the pass must carry the `connect` scope, also on a refresh; otherwise ERROR 10.
4. A refresh (a second AUTH on the same connection) MUST name the same `sub` as the first; otherwise ERROR 10.
5. On the first AUTH under connect mode: the name check (ERROR 8 on conflict, also counted as a refusal), then the
   per-account connection limit `-pass-max-conns-per-sub` (ERROR 10), then registration.
6. OK.

A refused AUTH changes nothing. Refusals count whichever code answers them (10 or 8). Three in a row close the
connection and the reply to the third is not guaranteed; a good AUTH resets the count. A refresh
replaces the first pass's scopes and expiry.

The pass format and its checks are in [section 14.3](#143-the-sign-in-pass).

## 7. Rooms

### 7.1 General rules

- A room belongs to its creator's title. Every room request is scoped to the caller's title. Another title's room id
  answers ERROR 2 to JOIN_ROOM and ERROR 5 to the other room requests.
- Room ids count up from 1 for the life of the process and are not reused. Member ids count up from 1 in the room as a
  `u16` and are not reused in it; the counter has no overflow check, so after 65535 joins to one room a new id wraps.
- A player MAY be in several rooms at once, up to `-max-player-rooms` (default 8). Every reply and push names the room.
- The owner is the creator. When the owner leaves or disconnects, the first remaining member in join order becomes
  owner. An empty room closes and frees its code.
- A blob a player puts in a room (CREATE_ROOM `external`, `internal` and `member_data`, JOIN_ROOM and JOIN_BY_CODE
  `member_data`, SET_ROOM_DATA `data`, ROOM_MESSAGE `data`) is at most 4096 bytes; more is ERROR 1.
- A ROOM_JOINED reply MUST fit 65535 payload bytes: 26 + `external` + `internal` + 31 + data per member. A JOIN or a
  SET_ROOM_DATA that would make it larger gets ERROR 6.

### 7.2 Requests

**CREATE_ROOM.** `max` is 1 to 255; 0 is ERROR 1. `flags` is stored and shown to searchers. The creator is member 1 and
the owner. A new room is public. ERROR 6 at the room limit or per-player room limit.

**SEARCH_ROOMS.** `start` is a 0-based offset into the list of the caller's title's public rooms that are not full,
oldest first. `max` is the most entries wanted; 0 returns none. `total` in the reply is the length of the whole list, not
of the page. The reply stops adding entries when the next would take it past 65535 bytes, so `count` can be under `max`
and under what remains; the caller continues at `start + count`. A first entry too big to fit gets ERROR 6.

**JOIN_ROOM.** ERROR 2 for an unknown room, another title's room, or an invite-only room the caller is not in. ERROR 3
when members equal `max`. Joining a room the caller is already in returns ROOM_JOINED again with the old member data,
and sends nothing new. ERROR 6 if the room has grown too large to describe.

**LEAVE_ROOM**, **SET_ROOM_DATA**, **ROOM_MESSAGE**, **KICK_MEMBER**, **SET_ROOM_ACCESS**, **GET_ROOM_ACCESS.** A room
that does not exist, or that the caller is not in, gets ERROR 5. These never answer ERROR 2.

**SET_ROOM_DATA.** Owner only. `which` 0 is `external`, 1 is `internal`, 2 is `flags` (a 4-byte blob). `which` over 2, or a
`flags` blob that is not 4 bytes, is ERROR 1. Checks in order: body, in room (5), owner (4), size (6). Every member
except the caller gets ROOM_DATA with the same `which` and blob.

**ROOM_MESSAGE.** Any member. `to` 0 sends to every member except the sender; a member id sends to that member only if it
is present. A message to a member id that is not there, or to oneself, still gets OK and delivers nothing. Recipients get
ROOM_MSG with `from`, `to` and the data.

**KICK_MEMBER.** Owner only (ERROR 4 otherwise). The target gets KICKED. Every remaining member, the kicker included,
gets MEMBER_LEFT. A member id that is not in the room, or the owner naming itself, is ERROR 2. An owner cannot be
kicked.

**SET_ROOM_ACCESS.** Owner only. `mode` 1 gives the room a code and removes it from SEARCH_ROOMS and the status page; if
a code is already set the same code returns. `mode` 0 removes the code. A mode over 1 is ERROR 1. No push goes to the
other members, so they learn of the change only by asking. Reply ROOM_ACCESS: `mode u8`, `code [8]` (all NUL when there
is none).

**GET_ROOM_ACCESS.** Any member. Reply ROOM_ACCESS.

**JOIN_BY_CODE.** `code [8]` is 8 symbols from `0123456789ABCDEFGHJKMNPQRSTVWXYZ`, read case-insensitively with `O` as 0
and `I` and `L` as 1. Otherwise ERROR 1 (not counted as a miss when the body is too short to hold a code; counted when
it holds 8 bytes with a bad symbol). An unknown code is ERROR 2 and counts as a miss: 5 misses on one connection or 20
from one address (IPv6 counts as its /64) within 60 s close the connection. A code that names a room of the connection's
title resets that connection's count, even if the join is then refused (room full, or the player's room limit). ERROR 3 for a full room.

### 7.3 Order of messages around a join

The joiner's ROOM_JOINED is queued first. Then, in this order: MEMBER_JOINED to each existing member; with `-relay`, ROUTE
for each pair in both directions; and PEER_KEY for each pair that both sent PEER_AUTH, to both sides. The position of
PEER_KEY relative to the rest is read from the source (untested).

### 7.4 Member endpoint

For viewer V and member M, the `ip` and `p2p_port` in M's entry are chosen in this order:

1. `-lan-public-addr` is set, M's TCP address is private, carrier-grade NAT or link-local, and V's is not: that
   address with M's HELLO port.
2. M has probed and V is not on M's public address: M's probe source address and port.
3. M's probe gave a usable `local_ip`: that with M's HELLO port.
4. Otherwise M's TCP address with M's HELLO port.

"On the same public address" is IPv4 equality of the TCP addresses; an IPv6 client is never on the same network as
anyone. A usable `local_ip` is private, carrier-grade NAT, link-local, or equal to the probe's source address.

## 8. Scores

Scores are scoped to the title and the board, and use the title and name from HELLO.

A board is keyed by (title, `board`). Each player keeps their best score only, and ties go to whoever set the score first.

**RECORD_SCORE.** `board u32`, `order u8`, `score s64`, `comment blob`. `order` 1 means lower is better; any other value
behaves as 0, higher is better, and is not validated. The first RECORD_SCORE on a board sets its order. The comment is at
most 64 bytes, else ERROR 1. Reply SCORE_RECORDED with `rank u32`, the player's rank after recording, 1 for the best (their
best stands if this one is worse). Memory store limits: 1000 titles, 1000 boards per title, 10000 players per board; past
them ERROR 6.

**GET_RANKING.** `board`, `start u32` (a 1-based rank; 0 is ERROR 1), `count u16` (at most 100; more is ERROR 1).
Reply RANKING with `total` (the number of players on the board) and `count` rows. A `count` of 0, or a `start` past the
end, is a RANKING with zero rows, not an error.

**GET_RANKING_BY_ID.** `board`, `n u16` (at most 100, ERROR 1 otherwise, and ERROR 1 if the body holds fewer than `n`
names), then `n` names of 16 bytes. The reply has one row per name asked, in request order. A name with no score returns the name
as asked, rank 0, score 0, an empty comment and time 0.

**Names.** Boards key on the exact `online_id` as sent. A name that differs only by case is a different player on a
board, although the HELLO name check treats the two as one name. A client SHOULD send the same capitalization every time.

## 9. UDP

The server listens for UDP on the same host as TCP. The default port is 36100 for both; `-tcp-addr` and `-udp-addr` may
set different ones, and a client that is not told the UDP port SHOULD assume the TCP port. Every packet starts with the
four bytes `PSNR` and a type byte. The UDP socket is separate from the title's own P2P socket; a client sends from the
P2P socket so the server sees that socket's public mapping.

### 9.1 Packets

| Type | Name | Direction | Layout after `PSNR` + type | Bytes |
|---|---|---|---|---|
| 0x01 | PROBE | P2P socket to server | `user_id u32` `token u32` `local_ip [4]` | 17 |
| 0x81 | PROBE_REPLY | server to P2P socket | `public_ip [4]` `public_port u16` | 11 |
| 0x02 | PUNCH | peer to peer | `user_id u32` | 9 |
| 0x03 | RELAY (up) | P2P socket to server | `from_user u32` `token u32` `to_user u32` then payload | 17 + payload |
| 0x03 | RELAY (down) | server to P2P socket | `from_user u32` then payload | 9 + payload |
| 0x04 | DISCOVER | client to server | `nonce u32` then zeros | exactly 64 |
| 0x84 | DISCOVER_REPLY | server to client | `nonce u32` `tcp_port u16` `udp_port u16` `flags u8` `players u16` `name [24]` `version [12]` | 52 |

### 9.2 PROBE

1. After HELLO_ACK, the client sends PROBE with its `user_id` and `token`, and `local_ip` set to its own LAN address. It
   repeats until a PROBE_REPLY arrives, and SHOULD repeat about every 15 s while online to keep the router mapping
   open. This interval is guidance, not a protocol rule.
2. The server accepts the probe when: it is at least 17 bytes (bytes after 17 are ignored), the user is registered, the
   token matches, the title is not in `-no-peer-titles`, and the source IPv4 address equals the player's TCP address
   (unless `-udp-any-source`). It then records the probe's source address and port and `local_ip` for the player and
   answers PROBE_REPLY with the source address and port it saw.
3. Anything else is dropped without a reply and counted under a reason (`unauthorized`, `source_mismatch`,
   `malformed`, and so on). A PROBE from an IPv6 source is counted `malformed`. Except that a probe from a player of a
   title in `-no-peer-titles` is dropped silently and counted under no reason.
4. The server does not expire the record.

A PUNCH sent to the server is counted `malformed` and not answered. PUNCH is for peers: a few of them sent to the address
in a member entry open the sender's router for the peer. Both sides do it. The server never sends one.

### 9.3 DISCOVER

Only with `-discover`. The server answers only a datagram of exactly 64 bytes (the header, `nonce`, then filler; a client SHOULD send zeros, and
the server does not check them), and only to a
source that is private, carrier-grade NAT, link-local or loopback. The reply echoes `nonce`, and is never larger than the
request. `flags` bit 0: the server relays. `players` is the connected count, capped at 65535. `name` is at most 24
printable ASCII bytes; `version` is cut at 12 bytes. With `-discover` off, or for any other length, type 0x04 is unknown:
dropped and counted `malformed`.

### 9.4 Control packets and the title

A client runtime MUST drop any datagram of 5 or more bytes that starts with `PSNR` before the title sees it. Later versions may add packet types.

### 9.5 IPv6

TCP connections over IPv6 are accepted and counted by /64. The protocol is otherwise IPv4. For an IPv6 client,
`public_ip` in HELLO_ACK and `ip` in member entries are 0.0.0.0, a UDP probe from IPv6 is dropped as `malformed`, and
the "same address" checks for probes and relays compare IPv4 only. A client that needs peers MUST use IPv4.

## 10. Relay

The relay needs `-relay`. It carries game traffic between two players that share a room, in two forms.

### 10.1 ROUTE

On join, the server sends ROUTE for each pair, both ways. Bit 0 is set for a pair when the title of neither is in
`-no-peer-titles` and either `-relay-force` is on, or the target has probed, the probe's source address differs from the
`local_ip` it sent, and the two are not on one public address. A missing or unusable `local_ip` counts as different.
ROUTE is computed at join time only and is not sent again: a member who has not probed yet when someone joins gets bit 0
clear. A title that needs a current answer MUST work it out itself.

### 10.2 Streams

```text
A: opens a new TCP connection, first frame STREAM_CONNECT(req, A.user, A.token, B.user, vport)
server: A known, token matches, B registered, A and B share a room, no -no-peer-titles,
        (under -require-pass relay) both passes allow the relay
server -> B on B's control connection: STREAM_OFFER(stream_id, A.user, vport)
B: opens a new TCP connection, first frame STREAM_ACCEPT(req, B.user, B.token, stream_id)
server -> A's leg and B's leg: STREAM_READY, each echoing that leg's own req
then raw bytes are copied both ways, no framing, until either side closes, which closes both
```

Payloads are 18 bytes for STREAM_CONNECT and 16 for STREAM_ACCEPT. STREAM_READY is 4 bytes. On failure the server
writes one ERROR on the leg and closes it. Its `req` is that of the leg's frame, or 0 if the frame was too short to hold
one.

| Code | Leg failure |
|---|---|
| 1 | frame too short |
| 2 | everything else: bad token, not a room mate, unknown or already taken `stream_id`, B did not accept in 10 s. Under connect mode an unsigned connection is not listed, so a leg from it gets 2 |
| 9 | a well-formed leg on a server without `-relay`; or, with `-relay`, a STREAM_CONNECT that every other check allowed but whose title (the sender's or the target's) is in `-no-peer-titles`. A listed title's leg that fails an earlier check gets that check's code, 2 for example |
| 10 | the pass `-require-pass relay` needs is missing or lapsed, or a pass of either player no longer counts (revoked account, removed key) |

A stream from a player to themselves is not rejected when they share a room with themselves (untested). The idle limit is
`-relay-idle` (default 600 s, 0 for never); a write stalled for 30 s closes the stream.

A stream that is open also ends when a pass stops covering it. With `-require-pass relay` or `connect`, it ends 60 s after
the earlier `exp` of its two players' passes unless that player's own connection sends a refresh AUTH (a closed
connection cannot extend it), and at SIGHUP when the key that signed either pass leaves the key file. In every mode it
ends at SIGHUP when either account is revoked. Both legs are closed without an ERROR.

### 10.3 Datagrams

A datagram `PSNR 03 from_user token to_user payload` sent to the server's UDP port is forwarded when both players are
registered, the token matches, they share a room, the title allows peers, passes allow it (if required), the target has
probed, and, unless `-udp-any-source`, the sender's source IPv4 address equals its TCP address. The forward is `PSNR 03
from_user payload`, sent from the server's UDP port to the target's probe endpoint. An empty payload is forwarded as 9
bytes. Drops are counted by reason: `malformed`, `relay_disabled`, `relay_no_pass`, `unauthorized`, `source_mismatch`,
`write_error`, `rate_limit`, `source_capacity`, `discover_refused`.

On IPv4 an UDP datagram holds at most 65507 bytes, so the largest payload a client can relay is 65490. A client SHOULD
keep relayed datagrams well below the path's MTU.

A client that accepts relayed datagrams MUST check that they came from the server's address and port; the relay is the
only voucher for who sent them.

## 11. Errors

ERROR (0x8F): `req u32` `code u32`.

| Code | Name | Used for |
|---|---|---|
| 1 | bad request | a malformed or short body; an unknown request type; a second HELLO; an empty `comm_id`; CREATE_ROOM `max` 0 or an over-limit blob; SET_ROOM_DATA `which` over 2, or `flags` not 4 bytes; SET_ROOM_ACCESS `mode` over 1; JOIN_BY_CODE with a code that is not 8 valid symbols; RECORD_SCORE comment over 64; GET_RANKING `start` 0 or `count` over 100; GET_RANKING_BY_ID `n` over 100 or with fewer names than `n`; AUTH on a server with no pass settings; a stream leg with a short frame |
| 2 | not found | JOIN_ROOM for an unknown id, another title's room, or an invite-only room the caller is not in; JOIN_BY_CODE with an unknown code; KICK_MEMBER for a member that is not there or for oneself; any stream failure that is not 1, 9 or 10 |
| 3 | room full | JOIN_ROOM or JOIN_BY_CODE when members equal `max` |
| 4 | not owner | SET_ROOM_DATA, KICK_MEMBER, SET_ROOM_ACCESS by a member who is not the owner |
| 5 | not in room | LEAVE_ROOM, SET_ROOM_DATA, ROOM_MESSAGE, KICK_MEMBER, SET_ROOM_ACCESS, GET_ROOM_ACCESS for a room that does not exist or that the caller is not in |
| 6 | limit reached | a room limit or a per-player room limit; a join or SET_ROOM_DATA that would not fit one ROOM_JOINED; a failed room-code random source; a score store limit or failure |
| 7 | no hello | any request, HEARTBEAT, AUTH, GET_CAPS and unknown types included, before HELLO; AUTH in connect mode if the connection was taken over |
| 8 | name taken | HELLO (or AUTH under connect mode) when another address holds the name, case-insensitively |
| 9 | no relay | a stream leg on a server without `-relay`, or a STREAM_CONNECT for a title in `-no-peer-titles` that passed every other check (see [10.2](#102-streams)) |
| 10 | pass | AUTH refused (not guaranteed for the third refusal in a row, see [6.5](#65-auth-0x05)); a stream leg without the pass `-require-pass relay` needs; any request but HELLO, AUTH, HEARTBEAT, GET_CAPS before sign-in under connect mode |

0 and 11 and above are unassigned. A client MUST treat an unknown code as a general failure. The C client also has
`PSNR_E_POLICY` (1000), which is local to the client and never on the wire.

Several codes depend on the request, not only on the problem: an unknown room is 2 for JOIN_ROOM and 5 for every other
room request.

### 11.1 Silence and closes

The server sends no ERROR for: a payload under 4 bytes; HEARTBEAT after HELLO; PUNCH; a UDP packet it drops.

It closes the connection without an ERROR for: 90 s without a frame; more than 200 frames a second; an outbound queue
over 2048 frames or 128 KiB; a frame that declares too long a payload before sign-in; no sign-in in time; a pass that
has expired (connect mode); the third refused AUTH in a row (an ERROR may be queued first, but none is guaranteed); too
many wrong room codes; the connection caps at accept; both legs of a relay stream that a pass has stopped covering.

## 12. Limits

The values are the defaults of the reference server. A different server MAY use others; a client SHOULD NOT depend on them
being exactly these.

### 12.1 Sizes

| What | Value |
|---|---|
| Frame payload | 65535 |
| Room blob | 4096 |
| Score comment | 64 |
| Rows in GET_RANKING, names in GET_RANKING_BY_ID | 100 |
| `comm_id`, `online_id` | 12, 16 |
| AUTH pass | 1 to 2048 |
| Frame before sign-in (connect mode) | 2055 |
| UDP read buffer | 65536 |
| Largest relayed payload, IPv4 | 65490 |
| C client `psnr_send` body | 65531 |

### 12.2 Rates, counts and times

| What | Value |
|---|---|
| Default game port, TCP and UDP | 36100 |
| Idle timeout | 90 s |
| Write timeout for one frame | 10 s |
| Outbound queue per connection | 2048 frames or 128 KiB |
| Request rate | 200 a second per connection (`-max-req-rate`) |
| Connections | 4096 total (`-max-conns`), 32 per address (`-max-conns-per-addr`) |
| Rooms | 1000 total (`-max-rooms`), 8 per player (`-max-player-rooms`) |
| Wrong room codes | 5 per connection or 20 per address in 60 s |
| Refused AUTH in a row | 3 |
| Sign-in time (connect mode) | 30 s from accept |
| Unsigned connections (connect mode) | 64 total, 4 per address |
| Connections per account | 2 (`-pass-max-conns-per-sub`) |
| Reported round trip | kept up to 60000 ms, shown for 3 min |
| Stream accept wait | 10 s |
| Stream idle | 600 s (`-relay-idle`), write stall 30 s |
| UDP rate | 1000 a second per source and burst (`-max-udp-rate`), 4096 tracked sources |
| Score store | 1000 titles, 1000 boards each, 10000 players per board |
| C client | heartbeat 20 s; a send stalled 5 s ends the client; connect and HELLO wait 5 s |

## 13. Versioning and compatibility

1. **There is no protocol version number on the wire.** HELLO carries none. The only version byte is AUTH's, and it
   versions the pass format. The server's software version string is returned in CAPS, in DISCOVER_REPLY (cut to 12
   bytes) and on `/api/stats`.
2. **Capabilities are the feature handshake.** A client SHOULD send GET_CAPS after HELLO, use the bits it knows, and
   ignore the rest. A clear bit means the server does not offer it.
3. **A server from before GET_CAPS answers ERROR 1 to it.** This is how api.md describes servers from 0.5.0 or before; no server
   from that time was run for this page (untested). A client MAY send a new request type to an older server and read
   ERROR 1 as "not supported". The server never closes the connection for an unknown type.
4. **A server MAY add push types in 0xA0 to 0xAF, and new reply types, without a version change.** A client MUST ignore
   unknown pushes. The Go client hands them on as raw bytes.
5. **Requests can grow at the end.** A server ignores trailing bytes on every request except AUTH, so a later version can
   add a field there. Replies are different: the C client's parse functions (`psnr_room_joined_parse`, `psnr_room_list_parse`,
   `psnr_ranking_parse` and `psnr_caps_parse`) return failure when bytes follow the last entry, so a server MUST NOT grow
   those replies without a capability bit a client has to ask for. This was read from `client/psnr.c`, not run
   (untested).
6. **Flags and bits.** Ignore the ones you do not know. HELLO_ACK bits 4 to 7, ROUTE bits 1 to 7 and CAPS bits 8 and up
   are reserved.
7. **Names are title-scoped.** Two titles never see each other's rooms or boards.
8. The release marks in api.md ("0.3 on", "after 0.4") name released versions of this server, not protocol numbers.

A change that breaks a requirement in this page MUST come with a new capability bit or a new type, not a silent change.

## 14. Security considerations

The protocol is plain TCP and UDP with no encryption and no authentication of the server to the client. A client that
sends something over it MUST assume anyone on the path can read and alter it. This section says what is and is not
protected. Report a problem as [SECURITY.md](../SECURITY.md) describes.

### 14.1 Names and identity

`online_id` and `comm_id` are claims, not identities. The HELLO name check stops two addresses from holding one name at
once; it does not say who the player is. A server SHOULD NOT treat a name as an account. Under connect mode only a
signed-in connection holds a name, so a connection that never signs in cannot keep anyone out of theirs.

### 14.2 The token

The HELLO_ACK `token` is a random 32-bit value. It is the proof a UDP PROBE, a relay datagram or a stream leg comes from
the player the server registered. It travels in the clear, so anyone who can read the connection can use it. It stops a
blind third party from moving another player's UDP endpoint; it does not stop a path attacker. A server MUST NOT accept a
PROBE or RELAY packet for a user whose token does not match. Under connect mode, no token exists until AUTH succeeds, so
before it a PROBE, RELAY or stream leg for that id is refused and counted `unauthorized`.

By default a probe also has to come from the player's TCP address (`-udp-any-source` turns this off), which makes
off-path spoofing of a probe by another address harder.

### 14.3 The sign-in pass

A pass is optional and off by default (`-pass-key-file`). A server that sets it checks an Ed25519 signature offline against
public keys from a file; it never contacts a provider and holds no provider secret. A pass shows that an account at a
login service signed in. It says nothing about who the player is or what name they use.

Format: `v1.` + base64url(payload) + `.` + base64url(signature), no padding. The signature is Ed25519 over the ASCII bytes
`v1.` + base64url(payload) exactly as sent. Nothing is canonicalised and there is no algorithm field. The payload is compact
JSON with exactly these fields. An unknown or missing field refuses the pass, and so does a `cnf` field.

| Field | Rule |
|---|---|
| `v` | `"v1"` |
| `kid` | names a key in the key file; an unlisted kid is refused |
| `iss` | equals `-pass-issuer` |
| `aud` | equals `-pass-audience`, which MUST NOT be empty |
| `sub` | `github:<number>` or `discord:<number>`, 1 to 19 digits, no leading zero; no name or email |
| `scope` | array of `"connect"` and `"relay"`, no repeats |
| `iat`, `exp` | whole seconds; `exp` after `iat` and at most 3600 s later; both compared with 60 s of skew |

Lifetime. `iat` may be up to 60 s ahead of the server clock, `exp` up to 3600 s after `iat`, and the relay check allows 60 s
past `exp`. A pass can therefore open the relay for up to 3719 s, about 62 minutes, from the time it was accepted: it is accepted from 60 s before `iat`, and it lapses 3720 s later (a 3600 s lifetime plus 60 s of skew on each side), when the check `now - 60 < exp` first fails. A
pass is checked when a stream or datagram starts, and a stream that is already open ends with its pass as described in
[10.2](#102-streams). Rooms, scores and direct peer traffic need no pass.

Server duties:

- A server MUST verify the signature and every field before it changes any state, and a refused AUTH MUST change nothing.
- A server MUST NOT log a pass. The reference server logs a short keyed hash of the account; the key is random, made at
  each start and never stored.
- A server SHOULD limit connections per account (`-pass-max-conns-per-sub`) and SHOULD re-read the revoked file and key
  file on SIGHUP, closing the connections and open relay streams of revoked accounts and of passes whose key was removed.

Client duties:

- A pass is a bearer token sent over plain TCP: anyone who can read the connection can use it until it expires. A client
  SHOULD send a pass only to a server the player chose, and MUST NOT log it.
- A client MUST NOT treat ERROR 1 in answer to AUTH, HELLO_ACK flag bit 3, or CAPS bit 7 as proof of anything. A forged
  server can say any of them.

A pass bound to a client key (version 2) is not built, and its AUTH version is refused.

### 14.4 The connect gate

`-require-pass connect` (it can be combined with `relay`) makes AUTH the way in. A connection is not a player until a pass
with the `connect` scope is accepted.

- Before AUTH, HELLO is read but only parked: no name, no token, no entry in the player list, any room, any UDP lookup or any
  relay lookup. The only requests answered are AUTH, HEARTBEAT and GET_CAPS. Everything else, defined or not, gets ERROR 10.
  A second HELLO gets ERROR 1.
- The connection MUST sign in within 30 s of accept. The time counts from accept, not from HELLO, and HEARTBEAT does not
  extend it. Before AUTH a frame may declare at most 2055 payload bytes; a longer header closes the connection before the
  body is read.
- Connections that have not signed in are capped (default 64 in all, 4 per address). One over the cap is closed at accept with
  no reply. They also count against `-max-conns` and `-max-conns-per-addr`.
- The connection is closed 60 s after the pass's `exp` unless a refresh AUTH moved it. A refresh needs the `connect` scope
  too, so a refresh cannot drop it. A client SHOULD refresh well before `exp`.
- A client that does not send AUTH is refused on purpose: its first request after HELLO gets ERROR 10 and it is closed at the
  deadline.

### 14.5 Resource limits and denial of service

The limits in [section 12](#12-limits) are the protection. A server SHOULD keep the per-connection request rate, the
per-address connection cap, the outbound queue cap and the pre-sign-in frame cap, and MUST NOT allocate from a length a
peer declares without checking it against the cap first. Every frame counts toward the request rate, replied to or not.
The room code is 8 symbols of a 32-symbol alphabet and wrong guesses are limited per connection and per address.

DISCOVER answers only from private-range sources, only to a 64-byte request, and never with a larger reply, so it cannot
be used to amplify traffic at a public address.

Relayed datagrams and streams use the server's bandwidth; `-max-udp-rate`, `-relay-idle` and the stream write timeout
bound it. The relay is off unless `-relay` is set.

### 14.6 Peer keys

A peer key gives two players and the server a secret that a member cannot use to forge a packet from another member to a
third, and that a sender outside the room cannot make. It gives no confidentiality: the key travels in the clear. It gives
no replay protection, because the protocol carries no counter. A title that tags its packets with a key SHOULD put a
sequence number or nonce inside the tagged data and drop repeats. Direct TCP streams are not covered.

### 14.7 What is not protected

- No confidentiality or integrity on TCP or UDP. A path attacker can read and change anything, and can read the token, a
  pass and peer keys.
- No authentication of the server to the client.
- A name is not an identity (14.1).
- Anyone who joins a room can see its members' endpoints as per [7.4](#74-member-endpoint). Joining a room is how a title
  learns who to connect to; a player who does not want a peer to learn their address SHOULD NOT join a room with them.
- A relay stream that is open when a pass lapses or an account is revoked is closed by the server, but it can carry
  data until the server notices, at the latest 60 s after `exp`.

## 15. Conformance

There is no separate conformance suite. The protocol is pinned by tests in this repository that talk to a real server
over loopback. A client or server that passes the tests that apply to it matches the reference on what the tests cover.

### 15.1 Running them

```sh
python3 tools/loopback.py          # the native C client and the Go app client, four named Go tests
python3 tools/loopback.py --race
go test ./server                   # the server suite
go test ./app/internal/client      # the Go client
```

`tools/loopback.py` runs through `tools/check_conformance.py`, which requires the named tests to run and pass, and fails
on a skip or a renamed test. [testing.md](testing.md) describes it, its isolation and its limits. These runs do not cover
a game, a real router, or any non-loopback network. A change to wire behavior MUST update the tests in the same commit.

### 15.2 Tests by area

Names are Go test functions. Server tests are in `server/`; the Go client tests are in `app/internal/client/`. The C client
is driven by the `TestCClient*` Go tests, which build and run `client/test_*.c` and `client/examples/minimal.c` against an
in-process server.

| Area | Tests |
|---|---|
| Native C client against the server | `TestCClient`, `TestCClientReportsRTT`, `TestCClientAuth`, `TestCClientConnectWait`, `TestCClientStreamDeadline`, `TestCClientRelayPair` |
| Go client: rooms, scores, errors, route | `TestConformanceRoomsAndScores`, `TestConformanceRelayRoute`, `TestConformanceErrors`, `TestConformanceHeartbeatReportsRTT`, `TestHelloChecksFieldsBeforeSending`, `TestDecodeTruncatedPush` |
| Go client: AUTH | `TestAuthConnectMode`, `TestAuthRefusals`, `TestAuthRelayOnlyMode`, `TestAuthOldServerAnswersBadRequest`, `TestAuthTokenOptional` |
| HELLO and names | `TestSecondHelloRefused`, `TestSameAddr`, `TestSameNetworkNeedsIPv4`, `TestIPv6NameNotTakenFromAnotherAddress`, `TestEndpointIPv6PlayersNotSameLAN`, `TestRelayStreamIPv6Viewer` |
| Capabilities | `TestCapsDefault`, `TestCapsRelayDiscoverAndNoPeer`, `TestCapsNeedsHello`, `TestCapsOnlyOnRequest` |
| Heartbeat and reported round trip | `TestHeartbeatReportsRTT`, `TestReportedRTTGoesStale` |
| Rooms: limits and sizes | `TestBlobLimit`, `TestPlayerRoomLimit`, `TestSetRoomDataReplyLimit`, `TestJoinReplyLimitJoinRoom`, `TestJoinReplyLimitByCode`, `TestJoinReplyLimitRejoin`, `TestJoinedSizeMatchesReply`, `TestSearchReplyFitsPacket`, `TestSearchFirstEntryTooBig` |
| Rooms: kick, access and codes | `TestRoomKick`, `TestRoomKickDispatchOrder`, `TestInviteOnlyRoom`, `TestRoomAccessErrors`, `TestCodeGuessesCloseTheConnection`, `TestCodeLeavesWithTheRoom`, `TestCodeShape`, `TestCodeMissesCountByAddressAcrossConnections`, `TestAddressCodeMissesExpireWithTheWindow`, `TestAddressCodeMissesAreForgotten` |
| Rooms: message order | `TestRoomEventsFollowTransitions` |
| Peer keys | `TestPeerKeyPair`, `TestPeerKeysPerPair`, `TestPeerKeyNeedsBoth`, `TestPeerKeyLateOptIn`, `TestPeerKeyRepeatAndRejoin`, `TestPeerAuthNeedsHello` |
| Scores | `TestBoardLimitAnswersSix`, `TestRankingByIDRefusesBeforeReading`, `TestRankingByIDLargeCountWire` |
| Connection limits and rate | `TestConnLimitPerAddr`, `TestRequestRateLimit`, `TestMaxConnsRefusesBeyondTheTotal`, `TestMaxConnsAndPerAddrCountSeparately`, `TestMaxConnsDefault` |
| UDP probe, guard and drops | `TestUDPLimiter`, `TestUDPLimiterIPv6Prefix`, `TestUDPAdmissionRealPackets`, `TestUDPDropReasonsAndOff`, `TestUDPProbeWriteErrorsAndSourceMismatch`, `TestUDPSourceCapacityAccounting` |
| Endpoint choice | `TestLANPublicAddrOff`, `TestLANPublicAddrRemoteViewer`, `TestLANPublicAddrLANViewer`, `TestLANPublicAddrPublicMember` |
| Discovery | `TestDiscoverOffNoReply`, `TestDiscoverReply`, `TestDiscoverWrongLengthAndOtherTypes`, `TestDiscoverSourceClasses`, `TestDiscoverPublicSourceRefused`, `TestDiscoverRateLimited`, `TestDiscoverNameRules` |
| Relay: streams cut off with the pass | `TestRelayStreamEndsWhenAccountRevoked`, `TestRelayStreamEndsWhenKeyRemoved`, `TestRelayStreamEndsWhenPassExpires`, `TestRelayStreamKeepsRunningPastExpiryWithoutRequirement`, `TestRelayReloadRacingStreamSetup`, `TestRelayStreamLeavesNothingBehind`, `TestRelayCutWhileWaitingReleasesAtOnce`, `TestRelayEndedStreamStopsItsTimer`, `TestRelayLegArrivingAfterCutIsClosed` |
| Relay: route, streams, datagrams | `TestRoutePushOnJoin`, `TestRelayStreamRule`, `TestStreamRelay`, `TestUDPRelay`, `TestUDPRelayOff`, `TestUDPRelayFromOtherAddress`, `TestUDPRelayDropAccounting`, `TestUDPRelayRollbackTraffic`, `TestUDPRelayPayloadSizes`, `TestRelayForceRule`, `TestRoutePushForced`, `TestRelayForceNeedsRelay`, `TestAwaitAcceptUsesLegClaimedAtTimeout`, `TestAwaitAcceptClaimedThenFailed`, `TestAwaitAcceptTimesOutWhenUnclaimed` |
| Titles that opt out of peer traffic | `TestNoPeerTitleProbe`, `TestNoPeerTitleHelloFlag`, `TestNoPeerTitleRoute`, `TestNoPeerTitleRelayRefused`, `TestNoPeerTitleRelayCounter` |
| Pass: format, checks, refresh | `TestCheckPassAcceptsAGoodPass`, `TestCheckPassRefusals`, `TestCheckPassSkewAndRevocation`, `TestPassAllowsBoundary`, `TestAuthWithoutPassSettingsIsBadRequest`, `TestAuthBeforeHello`, `TestAuthAcceptsAndRefuses`, `TestConnectionClosedAfterRefusedPasses`, `TestRefreshReplacesScopes` |
| Pass: relay requirement | `TestRelayCapsFollowThePass`, `TestCapsWithoutRequirementAreAsBefore`, `TestRelayUDPNeedsPassesAtBothEnds`, `TestRelayUDPWithoutRequirementIsAsBefore`, `TestStreamsNeedPassesAtBothEnds`, `TestStreamAcceptNeedsThePassToo`, `TestStreamErrorCodeIsPass` |
| Pass: accounts, reload, logging | `TestPerAccountConnectionLimit`, `TestTakeoverFreesTheAccountSlotOnce`, `TestReloadEndsPassesOfRemovedKeys`, `TestReloadClosesRevokedAccountsAndKeepsOldSettingsOnError`, `TestPassNeverInTheLog`, `TestSubTagIsKeyed` |
| Connect gate | `TestConnectBeforeHelloIsUnchanged`, `TestConnectGateRefusesEveryRequestBeforeAuth`, `TestConnectPreAuthCapsFollowTheRelaySettings`, `TestConnectGateOpensAfterAuth`, `TestConnectHelloIsInert`, `TestConnectUnauthenticatedHelloHoldsNoName`, `TestConnectNameClashAtAuth`, `TestConnectSubLimitWithTakeover`, `TestConnectUDPProbeNeedsAuth`, `TestConnectRelayDatagramNeedsAuth`, `TestConnectRelayLegNeedsAuth`, `TestConnectUnauthLimits`, `TestConnectCountersReturnToZero`, `TestConnectDeadline`, `TestConnectDeadlineNotExtendedByHeartbeat`, `TestConnectDeadlineCountsFromAccept`, `TestConnectFrameLimitBeforeAuth`, `TestConnectFrameLimitLiftedAfterAuth`, `TestConnectRefusedPasses`, `TestConnectRefresh`, `TestConnectExpiry`, `TestExpiryDoesNotCloseWithoutConnectMode`, `TestConnectOldClientIsRefused`, `TestConnectOffRegistersAtHello`, `TestConnectAndRelayBothRequired`, `TestConnectReload`, `TestConnectTakeoverVictimIsGuarded`, `TestConnectLogHygiene`, `TestConnectStress`, `TestConnectFlags` |

### 15.3 Requirements no test pins

These are stated above from the source. A client or server MAY rely on them, but a change would not fail a test today.

- A frame with a payload under 4 bytes is dropped silently.
- RECORD_SCORE `order` values other than 1 act as 0; score names are case-sensitive.
- HEARTBEAT before HELLO answers ERROR 7; a 1-byte HEARTBEAT body is ignored.
- An unknown request type answers ERROR 1 after HELLO and ERROR 7 before.
- GET_RANKING `start` 0 and `count` over 100 answer ERROR 1; a `count` of 0 returns no rows.
- ROOM_MESSAGE to an absent member or oneself answers OK and delivers nothing.
- SEARCH_ROOMS `total` is the length of the whole list, and `count` may be under it.
- The order of PEER_KEY after MEMBER_JOINED and ROUTE at a join.
- The 62-minute bound on pass lifetime and the 65490-byte
  relay payload limit: both are arithmetic over the constants.
- A stream from a player to themselves.

### 15.4 Not checked

Not run for this page: a server from 0.5.0 or before (the ERROR 1 statements about it are api.md's); any non-loopback network
(the member-endpoint and ROUTE cases for different public addresses were read from the source and checked with synthetic
records); the idle, sign-in, stream-accept and write-stall timeouts as wall-clock waits; outbound-queue overflow close;
the PROXY header; the HTTP and admin endpoints; Windows. See also [testing.md](testing.md).

## 16. What this page does not cover

- The status page, the admin API and `/api/stats`.
- The desktop app's trophy store and local broker.
- What a title does on top: how it maps its own online calls onto rooms and scores. See [integrating.md](integrating.md).
- A game's behavior over the relay, or any compatibility with a title, a platform service or a Sony protocol.
- Real-time titles over the relay. [api.md](api.md#real-time-titles-such-as-rollback) has the advice.

## Appendix A. Worked bytes

A HELLO request with `req` 1, `comm_id` `NPUA80001`, `online_id` `alice`, `p2p_port` 3658 (0x0E4A), in hex. 34 payload bytes:

Fields: type 0x01, length 0x0022, `req` 00000001, `comm_id` padded to 12 bytes, `online_id` padded to 16 bytes, `p2p_port`
0x0E4A. On the wire, with no spaces:

```text
010022000000014e5055413830303031000000616c69636500000000000000000000000e4a
```

The server's HELLO_ACK for `user_id` 1, a client at 203.0.113.7, `token` 0x1234ABCD and no optional settings, 17 payload
bytes:

```text
81 0011 00000001 00000001 cb007107 1234abcd 06
```

Without spaces: `8100110000000100000001cb0071071234abcd06`.

A heartbeat reporting 35 ms:

```text
02 0006 00000002 0023
```

An ERROR 5 in answer to a LEAVE_ROOM with `req` 9:

```text
8f 0008 00000009 00000005
```
