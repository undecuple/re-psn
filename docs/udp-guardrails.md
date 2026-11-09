# UDP rate limits and drop counters

This page describes the server on `main`; v0.3.0 does not have the limiter or the counters.

The server applies one packet allowance per UDP source (an IPv4 address, or an IPv6 /64 network) before decoding a datagram or taking the room/client state
lock. PROBE, RELAY and invalid datagrams share this allowance. The wire format, tokens, source-address check and
shared-room requirement are unchanged. Game traffic sent directly between players does not pass through this limiter.

## Settings

| Flag | Environment variable | Default |
|---|---|---|
| `-max-udp-rate` | `RE_PSN_MAX_UDP_RATE` | `1000` packets/second per source IP |

The deprecated `PSNR_MAX_UDP_RATE` alias follows the [usual precedence rules](running.md#environment-variables).
`0` disables the rate limiter and its source table; negative values stop startup. Drop counters remain enabled.

Each source starts with a burst allowance equal to one second of its rate. Tokens refill continuously up to that
allowance; a packet consumes one token. Changing the source port does not create a fresh allowance. All players
behind one public NAT address share it, including their probes and server-relayed datagrams. IPv4-mapped addresses
use the same source identity as IPv4 addresses. Probes and forwarded endpoints use IPv4. Incoming RELAY packets may
also arrive from IPv6 when `-udp-any-source` is enabled, preserving that existing source-check opt-out.

The default is an initial operational choice, not a measured capacity recommendation. Monitor the counters under your
normal relay workload and raise the rate when a shared NAT needs more packets. For example:

```sh
re-psn -relay -max-udp-rate 4000
```

This packet limit does not cap bytes per second: a large datagram costs one token, as a small one does. It also does
not limit relay TCP streams. The application still reads incoming datagrams; a saturated network link or socket queue
needs protection outside the process.

## Bounded source state

At most **4096 sources** are tracked. All addresses in one IPv6 /64 count as one source, so a single network cannot fill the table with different addresses or restart its allowance by changing address. Entries idle for **one minute** expire, with cleanup at most once per second
of incoming traffic. No background cleanup goroutine is created. When the table is full, a previously unseen source
is dropped until cleanup frees space; existing sources keep their allowance. Active entries are never evicted to
make room for another address, so address churn cannot repeatedly reset their buckets.

Spoofed or distributed traffic can fill the table and temporarily exclude new sources. These bounds control memory
and admission work; they do not authenticate a source or provide DDoS protection. The rate check runs before token
validation, so rejected tokens and malformed packets use the same allowance as valid traffic from that address. For
the same reason, spoofed packets that carry another player's source address use up that address's allowance, and the
player's own probes and relay datagrams are dropped as `rate_limit` until it refills.

## Drop counters

The existing opt-in `/api/stats`, protected when `-http-token` is set, adds a `udp_drops` object. Each value is a process-lifetime cumulative
count; restarting resets it. The object contains no source addresses, ports, player IDs, names or token values.
The rest of the status response still contains the usual room/player data and needs the same access controls.

| Field | First applicable reason for dropping a received datagram |
|---|---|
| `rate_limit` | The source has no packet token available. |
| `source_capacity` | The source table is full and this source has no entry. |
| `malformed` | An unusable source address, an IPv6 probe, short packet, wrong magic or unknown UDP message type. A short RELAY is malformed even when relay is off. |
| `relay_disabled` | A well-formed RELAY arrived while `-relay` is off. |
| `unauthorized` | A missing client, bad token, unready target endpoint or missing shared room prevents acceptance. |
| `source_mismatch` | An otherwise authorized packet's source differs from the player's TCP source and `-udp-any-source` is off. |
| `write_error` | Writing an otherwise accepted probe reply or relay forward fails or is short. |

A rejected packet increments one counter, at the first applicable check in the order above, except that an unusable source
address is counted as `malformed` before the allowance is applied. Successful replies and
forwards do not increment a drop counter. Invalid traffic rejected by admission increments the admission reason rather
than its later parse/authentication reason. The existing one-time warning for a player's mismatched probe remains;
new drop paths add no per-packet log messages. Counters are cumulative across all sources and are sampled independently.

## Coverage and remaining work

Deterministic bucket tests cover burst/refill, source independence, expiry, bounded capacity and disabling. Real
loopback tests exercise valid probes, rate rejection, invalid-traffic charging, relay admission and drop counters.
Existing native C-client and Go app-client conformance remain required. No game or production flood measurement is
claimed; only loopback was exercised.

The broader roadmap item is partial. Per-player relay byte/datagram budgets, maximum relay payload size, TCP relay
idle/stream limits, repeated-failed-token blocking and a UDP fuzz target remain planned. A larger token would change
the protocol and needs its own versioned change. This work introduces no protocol change or new dependency.
