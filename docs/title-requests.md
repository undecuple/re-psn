# Requesting a title

How to ask for a game title to be added to the known list or to have its test level recorded, and what the
maintainers do with the request. re-psn is unofficial fan work and is not affiliated with Sony.

## Contents

- [What compatibility means here](#what-compatibility-means-here)
- [What is not possible](#what-is-not-possible)
- [What to send](#what-to-send)
- [What the maintainers do](#what-the-maintainers-do)
- [What tested means](#what-tested-means)

## What compatibility means here

The server does not know games. A title is a string: its NP communication ID (such as `NPWR00153_00`), which the client
sends in HELLO. The ID scopes rooms and leaderboards, so two titles never see each other's ([protocol](api.md)). Any
client that sends an ID and speaks the protocol gets rooms and scores. No row is needed for that.

Two files make a title special:

- **`server/titles.tsv`** is a table of ID, display name and serials. It is read once at start and changes only how the
  status page and `/api/stats` label a title (`[Killzone 3 (BCUS98234, BCES01007)]` instead of the bare ID). A row is not
  a support claim.
- **`-no-peer-titles`** (`RE_PSN_NO_PEER_TITLES`) is a server setting that lists IDs of titles that use no peer traffic.
  For them the server answers no UDP probe, relays nothing and sends no relay route. Rooms and scores still work. See
  [running.md](running.md) and [public-server.md](public-server.md).

What a title needs to work with re-psn:

1. A communication ID of 1 to 12 printable ASCII bytes.
2. A client on the game side: the [C client](../client/psnr.h), or an adapter in the game's runtime that maps its online
   calls to the protocol ([integrating.md](integrating.md)).
3. The features it uses: rooms, room data and messages, leaderboards. Peer traffic only if the game sends packets
   directly between players; then it also needs the probe and, behind routers, the relay or hole punching.

Whether the game's own online code runs is the runtime's job, not the server's. A room join does not show that the game's
peer traffic or sync works.

## What is not possible

- The server carries no game code and no game data. Room blobs are capped at 4096 bytes.
- A stock game cannot use re-psn by itself. Its online calls must be mapped to the client by the title's own developers
  or by a patch or recompilation project for that title. A request cannot change that.
- Game material is never sent here: no disc or update files, keys, firmware, saves or links to them.
- Titles that need a publisher backend, other Sony services or genuine PSN authentication are out of scope. [medius-scope.md](medius-scope.md) is a scope
  record only; no Medius profile is implemented or supported.

## What to send

Open a [title request](https://github.com/undecuple/re-psn/issues/new?template=title_request.yml). Give:

- **Title name and region.** Serials are fine if you read them from your own disc's files.
- **NP communication ID.** The name of the title's trophy data directory, on a copy you own. It looks like `NPWR00153_00`; it is not the serial. Say where you read it.
- **Features it uses:** rooms, room data, leaderboards.
- **Peer traffic or not.** Packets sent directly between players. If you do not know, say so.
- **The client side.** Which integration the game has: the C client, a runtime adapter, or none yet.
- **A test log** (optional; needed for anything above "not tested") with user names, addresses and paths removed. Server lines from `-v` and the client output.
- **What you ran,** and what you did not run, with the commit or release you used. If you ran nothing, write "nothing run".

## What the maintainers do

One of:

- **Add a row** to `server/titles.tsv`, with a test, when the row comes from the game's own files (see the comment at the top of the file).
  `TestTitleTableRows` accepts only IDs of the form `NPWR` plus 5 digits plus `_00` and serials of 4 capital letters plus 5
  digits. Any other ID works without a row and shows as the bare ID.
- **Say no-peer.** If the title uses no peer traffic, the requester's own server can list its ID in `-no-peer-titles`. No code change. There is no public instance.
- **Ask for more** when the ID or the source of the ID is missing, or the log is missing for a level above not tested.
- **Record the level only** when there is no client yet: the answer is what the title needs and its test level, with no row.
- **Decline** when the title needs something out of scope, or the request holds game material.

Whatever the result, the reply states the test level. The form's level choice is your own view; the maintainers state the level from what you sent.

## What tested means

| Level | Meaning |
|---|---|
| Not tested | No run is recorded. This is the default for every title, including those in `titles.tsv`. |
| Loopback-tested | A client written for the tests, or the C client, ran against a server on one machine with the title's ID. It shows the server handles the ID and features. It does not show the game works. |
| Tested with a real client | The title itself, with a client integration, ran against a server and its actions were seen working, with the log. Say which features and which network cases. |

To loopback-test a title's ID on your own machine: follow the build steps in [integrating.md](integrating.md#first-test-the-server-and-c-client-without-a-game) but
change the `RE-PSN-DEMO` constant in `client/examples/minimal.c` to the ID, rebuild, and run the server with `-v` so its log
shows the ID. Do not commit that edit; send the log lines.

The [features list](features.md) uses the same honesty: it says what was run and what was not.
