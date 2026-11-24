# App framework API

`github.com/undecuple/re-psn/app/framework` is an exported Go 1.26 package for an app-owned offline trophy store,
portable re-psn identities and optional authenticated per-host friends. It is separate from the existing game
protocol and server module. No constructor implicitly registers with a host or starts a listener.

## Store and identity

| API | Ownership and behavior |
|---|---|
| `Create(dir, label)` | Explicitly creates an Ed25519 identity and private store; never replaces a profile. |
| `Open(dir)` / `Store.Close()` | Takes/releases a stable OS writer lock; one writer per directory. A process crash releases its lock. |
| `Read(dir)` / `Store.View()` | Returns a public snapshot without the signing seed; read-only disk snapshots can coexist with the writer. |
| `DecodeCatalog(reader)` / `Store.InstallCatalog(catalog)` | Strict bounded input and explicit catalog approval; refuses incompatible redefinition. |
| `Store.Unlock(title, id)` / `Store.Status(title, id)` | Monotonic, idempotent reports with app receipt time in Unix milliseconds. |
| `Store.Export(writer, passphrase)` / `Restore(dir, reader, passphrase)` | Encrypted identity/trophy recovery into a new/empty destination; excludes remote caches and game capabilities. |

Public IDs are `rp1-` plus lowercase SHA-256 hex of the raw Ed25519 public key. A key proves control of that
re-psn identity, not a PSN login, earned achievement, game ownership or provider badge. Reusing it on different
hosts makes those registrations linkable. Each host controls its own account and friendship authority.

Use a private app-owned directory, separate from shared INI connection settings. Unix requires an existing
directory to have no group/other permissions; Windows callers must choose appropriate per-user ACLs. The runtime
snapshot contains an unlocked signing seed. There is no same-user malware or filesystem rollback protection.

State is bounded to 4 MiB, 128 catalogs and 1024 trophy definitions per catalog, with strict schema/identity checks.
Writes flush a temporary snapshot and atomically replace; an error after replacement means uncertain final
durability. Backup v1 uses fixed 600000-iteration PBKDF2-HMAC-SHA256, random salt16/nonce12 and AES-256-GCM with
authenticated header. Passphrases are 12–1024 bytes. There is no merge, reset, key rotation or password-reset API.

## Scoped game broker

`NewBroker(store)` creates no listener. Call `Broker.Grant(title)` only for an approved catalog, then serve an
explicit listener bound to literal IPv4 `127.0.0.1`. Grants carry a random 32-byte token, title and expiry. The game
can only query/report trophy IDs within that grant. `Revoke(token)` serializes with mutations; `Close()` revokes,
closes and joins connections. Stop the broker before closing the store. `Serve` owns its supplied listener.

Grants expire after 15 minutes. A host may call `Renew(token)` before expiry, typically every five minutes while
the explicit game session remains active. Renewal preserves rate counters and cannot resurrect expired/revoked
tokens. The SDK has no renewal operation. Broker connections have two-second deadlines; connections/grants are
bounded to 16/128 with per-grant rate limits.

Vendor [psnr_app.h](../client/psnr_app.h) and [psnr_app.c](../client/psnr_app.c) for dependency-free C99/C++ calls.
Pass a private descriptor path to `psnr_app_load_grant`, then call `psnr_app_unlock` / `psnr_app_status` from your
adapter's worker queue. Calls are synchronous; avoid engine/runtime locks and choose an explicit offline fallback.
Failure never implies an unlock. The SDK does not implement Sony trophy container/save formats or automatically
award platinum. The [synthetic example](../client/examples/app_trophy.c) and [catalog](../client/examples/app_trophies.json)
contain no extracted game metadata. Windows links `ws2_32`.

The descriptor is at most 512 bytes: `re-psn-app-grant-v1`, `port=DECIMAL`, `token=HEX64`, each newline terminated.
Keep it private, remove it at session shutdown and never log its contents or put tokens in arguments. It contains
no identity key and is invalid after broker restart. A same-user descriptor thief can report that title's trophies.

Local wire v1 is one request per TCP connection: `RPT1`, opcode byte (1 unlock, 2 status), three zero bytes,
uint32 body length 36, token 32, uint32 trophy ID. Integers are big-endian. Half-close the write side after exactly
48 bytes: the broker requires EOF before mutation. Response: `RPR1`, status byte, three zero bytes, uint32 body
length; success 0 has unlocked byte 0/1 and uint64 Unix milliseconds (zero if locked), length 9. Errors have no body:
1 unauthorized/expired, 2 unknown trophy, 3 busy, 4 malformed, 5 storage, 6 rate limited. Successful native calls require
response EOF. `timeout_ms` is an absolute connect/send/receive deadline (default 2000, range 1–60000); file open is
separate. Neither this framing nor the HTTPS API changes the legacy game protocol version.

## Optional authenticated social API

Open `SocialService` with an explicit private `StateDir` and `AllowedIDs` (empty denies all), then `ServeTLS`
with the host's certificate and explicit listener. The service uses direct TLS1.3 mutual authentication and derives
account identity from the verified Ed25519 key. Use TLS passthrough rather than an HTTP proxy that terminates client
authentication. Account registration, friend requests, acceptance, blocks and revocations persist per host.

`NewSocialClient(store, SocialClientConfig{Origin: httpsOrigin, Roots: optionalCAPool})` makes no network request.
`Register`, `State`, `Request`, `Accept`, `Decline`, `Cancel`, `Remove`, `Block` and `Unblock` take a context.
Clients verify the server CA/hostname; no insecure mode, redirects or environment proxies. Origins have no path
prefix, query or fragment. Requests across clients sharing a store are serialized through response/cache commit,
with context cancellation while queued. `Close` releases client transport ownership; close clients before the store.

Requests require peer acceptance; crossed requests do not imply consent. Blocking atomically clears pending and
accepted edges in both directions; unblocking restores neither. Unknown/blocked targets are generic unavailable.
Only own-state snapshots are returned, timestamped as cached data. A `CacheError` means the remote action succeeded
but local persistence failed; refresh rather than assuming rollback. An interrupted response can leave outcome unknown.

The companion caps 256 accounts, 64 contacts per set, 4 MiB state, 32 connections and bounded request/rate budgets. Host-only
`RevokeAccount` persists denial and removes pending/accepted edges. Eligibility is checked on every request, including
existing/resumed TLS sessions. Allowlist changes require restart; no remote administration, unban or live reload.
No public directory, federation, live presence, invitations, trophy uploads or gameplay-identity binding is supplied.

Tests use generated identities/certificates and synthetic metadata, including actual two-host TLS consent and native
SDK reporting. These are not real-game, Sony save-format, native Windows SDK runtime or physical terminal evidence.
