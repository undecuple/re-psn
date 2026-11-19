# Docker Compose for a server the internet can reach

[`docker/docker-compose.public.yml`](../docker/docker-compose.public.yml) runs your own re-psn server with the settings
that [public-server.md](public-server.md) recommends, hardened as far as the image allows. Read that page and
[SECURITY.md](../SECURITY.md) first. re-psn is unofficial fan work and is not affiliated with Sony. It has no public instance
and no accounts: this file is for a host who chooses to expose their own server, and nothing here says one exists.

## Use it

This needs Docker Compose 2.24 or newer: the `env_file` entry with `required` fails with a schema error on an older one,
and on Compose v1.

```sh
# 1. The status-page token goes in a file next to the compose file, not in the compose file. Keep it out of git.
# openssl is assumed; any 64 random hex characters will do.
( umask 077; printf 'RE_PSN_HTTP_TOKEN=%s\n' "$(openssl rand -hex 32)" > docker/re-psn.public.env )

# 2. Build and start (from the repository root).
docker compose -f docker/docker-compose.public.yml up -d --build

# 3. Look at it, from this machine only.
docker compose -f docker/docker-compose.public.yml ps
curl -H "Authorization: Bearer $(cut -d= -f2 docker/re-psn.public.env)" http://127.0.0.1:36101/api/stats
```

Open 36100/tcp and 36100/udp, and nothing else for re-psn. Docker's published ports bypass the `ufw` and `firewalld` input rules,
so a rule that blocks the port there does not block it here; check from another machine which ports answer. Podman works with `podman compose` or
`podman-compose`; see [Not checked](#not-checked).

## What is set, and why

Every setting has a short comment in the file. This is the reason for each.

| Setting | Value | Why |
|---|---|---|
| `user` | `10001:10001` | A flaw in the server then gives an attacker an unprivileged user, not root. The image creates this user. |
| `cap_drop` | `ALL` | The game port is above 1024, so no capability is needed. |
| `security_opt` | `no-new-privileges` | A process in the container cannot gain rights through a set-uid file. |
| `read_only`, `tmpfs /tmp` | on, 16 MB | The server writes only a small pid file. With logs on stdout nothing else needs disk, so the root file system can be read-only. |
| `mem_limit`, `memswap_limit` | 256 MB, no swap | Rooms, names and scores are in memory. A flood can fill that memory; the limit stops it taking the host. Lower `-max-rooms` or raise the limit to taste. |
| `cpus` | `1.0` | The limits in re-psn bound work per source, not total work; this bounds it for the host. |
| `pids_limit` | `128` | A bound on processes and threads, so a fault cannot fork the host into trouble. |
| `ulimits nofile` | `16384` | Every connection is a file. `-max-conns` is 4096 and a relayed stream holds two sockets, so the limit must stay above twice that. It matches the systemd unit. |
| `restart` | `unless-stopped` | The server restarts after a crash or a reboot, and stays down when you stop it. Players reconnect; rooms and scores are lost ([restarts.md](restarts.md)). |
| `healthcheck` | `wget` on `/healthz` with the token | Shows `healthy` or `unhealthy` in `ps`. `/healthz` needs the same token as the status page, and the image has busybox `wget` and no `curl`. It checks the two serving loops, not that games work. Docker does not restart an unhealthy container by itself; an outside supervisor must act on it. |
| `logging` | `json-file`, 10 MB x 5 | The server does not rotate logs. The driver keeps at most 50 MB. Logs hold your players' addresses and IDs, and stay until they rotate out of that window or the container is removed. |
| `RE_PSN_LOG_FORMAT` | `json` | One line per event for tools. |
| `RE_PSN_LOG_LEVEL` | `info` | Connects, hellos and disconnects. `warn` would hide them. |
| `RE_PSN_ADDR` | `:36100` | The only port players need, for TCP and UDP. |
| `RE_PSN_HTTP` | `0.0.0.0:36101` inside the container | The status page lists every online ID. It must listen on all interfaces inside the container to be published, and the `ports` line puts it on the host's `127.0.0.1` only. |
| `RE_PSN_HTTP_TOKEN` | from `re-psn.public.env` | It keeps other local users and other containers on the network out. The server has no `-http-token-file`, so the token is an environment variable, kept out of the compose file and out of git. |
| `ports` | `36100` tcp and udp; `127.0.0.1:36101` | Nothing else is published: no admin API, no Medius port. |
| `RE_PSN_RELAY` | `false` | No flag limits relay bandwidth, and a stream that keeps moving has no byte cap. Turn it on only when you can cap bandwidth outside the process. |
| `RE_PSN_DISCOVER` | `false` | LAN discovery has no use on the internet. |
| `RE_PSN_MEDIUS` | `false` | No Medius protocol is implemented. |
| `RE_PSN_MAX_CONNS` and the other limits | the defaults, written out | They control memory and work per source. They are not a flood defence ([udp-guardrails.md](udp-guardrails.md)). Written out so a change is visible in review. |

### Optional: sign-in passes

A commented block in the file turns on sign-in passes. It is for a host who runs their own login service that signs short-lived
passes; re-psn holds no secret and no provider, and only checks a signature against the public keys in a file
([Sign-in passes](api.md#sign-in-passes)). Leave it commented otherwise.

| Setting | Value | Why |
|---|---|---|
| `RE_PSN_PASS_KEY_FILE` | a read-only mount of your key file | Public keys, no secret, but the server refuses a file that group or others can access: own it as 10001, mode 0400. It is read again on SIGHUP. |
| `RE_PSN_PASS_ISSUER`, `RE_PSN_PASS_AUDIENCE` | exactly what your service signs | A pass from another service, or made for another server, is refused. |
| `RE_PSN_REQUIRE_PASS` | `connect`, `relay` or both | `connect` makes a pass the way in: a connection that has not signed in is not a player and is closed after 30 seconds. A client that cannot send AUTH is refused on purpose. `relay` only gates the relay. |
| `RE_PSN_PASS_MAX_UNAUTH_CONNS`, `_PER_ADDR` | `64`, `4` | Cap connections that have not signed in. Players sharing one address share the second one. |
| `RE_PSN_PASS_MAX_CONNS_PER_SUB` | `2` | Connections one account may hold. |
| `RE_PSN_PASS_REVOKED_FILE` | optional | Accounts to refuse; same owner and mode. It needs its own mount: set without one, the server fails to start. |

A pass proves an account signed in at your service. It does not authenticate the online ID a player uses.

## Names, proxies and bandwidth

- **A host name is fine, but keep the DNS record DNS-only.** Players can use a name instead of an IP address. If your DNS
  provider is Cloudflare, set the record to "DNS only" (grey cloud). Cloudflare's ordinary proxy carries web traffic and cannot
  carry the raw TCP and UDP of the game port, and its raw-port proxy product needs an enterprise plan.
- **Watch the monthly transfer allowance.** The relay and every reply leave through your host. Most virtual servers include a
  monthly transfer allowance, and running over it is the limit that usually bites first. A bandwidth cap on the port limits the
  rate, not the total. Check the allowance, watch `relay_bytes` in `/api/stats`, and leave the relay off if you cannot afford the
  traffic. Direct player-to-player traffic does not pass through your host.

## What is deliberately not in the file

| Not set | Why |
|---|---|
| TLS | The game port has no TLS and the server offers none. Tokens and names cross it in the clear. A TLS proxy would only front the status page, which stays on localhost. |
| DDoS protection | The server limits connections, requests and UDP packets per source. That bounds memory and work, and does not protect a saturated link. That is your network's or provider's job. |
| Moderation | There is none: the admin API is read-only and off. You can restart the server, set limits or firewall an address. |
| A relay bandwidth cap | No setting exists. Cap it at your provider or with traffic shaping, and watch `relay_bytes` in `/api/stats`, before you set `RE_PSN_RELAY`. |
| A log file and volume | Logs go to stdout and the driver rotates them. A log file needs a volume and logrotate. Choose that only if a tool such as [fail2ban](fail2ban.md) must read a file. |
| `-trusted-proxies`, PROXY protocol | Only with a proxy you run, and UDP is never proxied. See [public-server.md](public-server.md#network). |
| `-udp-any-source` | It weakens the check against spoofed UDP probes. |
| `-admin` | Experimental and read-only. |
| Host networking | The file uses the default bridge. If every player appears to come from the gateway, UDP probes are refused; use [`docker-compose.host.yml`](../docker/docker-compose.host.yml) and copy the settings over, with two changes. Set `RE_PSN_HTTP` to `127.0.0.1:36101`, not `0.0.0.0:36101`: on host networking the `0.0.0.0` bind would put the status page, which lists every online ID, on every interface of the host, guarded only by the token. And copy the lines the host file lacks: `mem_limit`, `memswap_limit`, `cpus`, `pids_limit`, `ulimits`, `healthcheck`, `logging`, the env file and the other `RE_PSN_` settings. |
| Backups | There is nothing to back up: rooms and scores are in memory. |

## Not checked

The file was checked with `docker-compose config` (Compose 5.6.0), and the image was built from `docker/Dockerfile` and run with
`podman-compose` 1.6.0 on Podman 6.1.4, rootless, on loopback ports. On that run: HELLO worked, the process ran as 10001 with no
capabilities and `NoNewPrivs`, the root was read-only and `/tmp` was writable, the status page answered 401 without the token and
200 with it and was unreachable on the host's other addresses, the health check reported `healthy`, `/api/stats` showed the limits
in the file, and the optional pass block started with a 0400 key file, accepted a signed pass and refused a key file that others
could read.

It was not checked:

- **With Docker.** The file was not run with Docker Engine. Podman-compose did not carry over two settings: it ignored
  `memswap_limit` (swap stayed allowed) and `max-file` (only the 10 MB size was applied). Look at `docker inspect` on your host.
- **Real source addresses.** Rootless Podman showed every player as the bridge address in the log. Docker with published ports
  normally keeps the player's address for TCP and UDP, but this was not tried. If your log shows one address for everyone,
  `-max-conns-per-addr` and the UDP source check will fail for all players; use `docker-compose.host.yml`.
- **Under load or attack.** The memory, CPU, process and file limits are an initial choice, not measured. At idle the server used
  about 6 MB and 8 processes. Nothing was tested at 4096 connections.
- **Automatic restart on `unhealthy`.** Docker only marks the container; it does not restart it.
- **The token in the container.** It is an environment variable, so `docker inspect` and `/proc` inside the container show it to
  anyone who can run those. Keep access to the Docker socket for people you trust.
- **A real login service.** The pass block was tried with a test key and a pass made for the test, not with a service.
