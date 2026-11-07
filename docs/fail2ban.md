# fail2ban for the status page

The optional example in [deploy/fail2ban/](../deploy/fail2ban/re-psn-status.local) counts refused HTTP status-page
tokens and temporarily blocks their source addresses at a host firewall. It starts **disabled**. It uses existing
logs and changes no server behavior, game protocol or application authentication. Install fail2ban separately;
it is not bundled with re-psn.

Use it for an existing private status listener with `-http-token` enabled. Keep the listener on loopback, a VPN or a
private network as described in [running.md](running.md#ports). A listener reachable only on loopback generally needs
no network ban. This example assumes the source IP in the log also reaches the host firewall on the selected TCP port.

## Choose an enforcement point

| Deployment | Action |
|---|---|
| A native service reached directly, including over a VPN | The sample host jail can block TCP to the status listener's port. Its example port is 36101; use your actual port. |
| A reverse proxy on the same host | Block at the externally reachable proxy port, or use a proxy-specific action. Blocking backend port 36101 does not block requests to frontend port 443. A firewall ban on 443 affects other sites sharing that port. |
| A proxy or CDN on another machine | Enforce at that ingress. A backend firewall usually sees the proxy address, so banning a logged original-client address there does not stop the traffic. Keep proxy addresses out of automatic bans. |
| Bridged containers or published container ports | Check the host's forwarding/NAT path and use an action for that path. The sample INPUT-chain action is not a tested container recipe. |

Behind a proxy, configure only the actual proxy addresses in `-trusted-proxies` and ensure it reconstructs forwarded
headers from trusted information. For trusted peers re-psn prefers `CF-Connecting-IP`, then walks `X-Forwarded-For`
from the right. A non-CDN edge must strip client-supplied `CF-Connecting-IP`; the [Caddy example](../deploy/caddy/README.md)
does this. An incorrect trust configuration can put an innocent address in a ban log. Verify the logged address against
your actual request path before enabling the jail; see [proxy settings](running.md#behind-a-reverse-proxy).

## Configure logging

The filter expects re-psn's **JSON** log format. In the existing `/etc/re-psn/re-psn.env`, set:

```ini
RE_PSN_LOG_FORMAT=json
RE_PSN_LOG_LEVEL=info
```

Keep your current private HTTP bind and token settings. `info` includes the required warning; `warn` also works,
while `error` hides it. The logger writes to standard error even when an additional log file is configured.
This recipe uses the systemd journal, not that file.

For the supplied service, an explicit systemd drop-in can keep journal formatting predictable:

```ini
[Service]
StandardError=journal
SyslogIdentifier=re-psn
```

Add it with `sudo systemctl edit re-psn.service`, then reload the unit and restart re-psn at a suitable time:

```sh
sudo systemctl daemon-reload
sudo systemctl restart re-psn.service
```

A restart disconnects clients; [restarts.md](restarts.md) describes cleanup and lost in-memory state.
The filter accepts raw JSON messages and the journal backend's standard `host re-psn[pid]: message` prefix.
Custom identifiers, log processors, text format or reordered/extended records need their own reviewed filter.

## Install and test the example

Use a distribution-provided fail2ban with its systemd Python backend and an appropriate firewall action installed.
The offline filter check is exercised with fail2ban 1.1.1; distribution configuration and live firewall behavior need
verification on your host. The sample selects the packaged `nftables[type=multiport]` action and TCP only.

From a source checkout:

```sh
sudo install -m 0644 deploy/fail2ban/re-psn-status.conf /etc/fail2ban/filter.d/re-psn-status.conf
sudo install -m 0644 deploy/fail2ban/re-psn-status.local /etc/fail2ban/jail.d/re-psn-status.local
```

Release archives put deployment files under `examples/`; use those paths when working from an archive containing
this example. Older packaged releases may not include it.

Review `/etc/fail2ban/jail.d/re-psn-status.local` before enabling it. Set the correct port/action, retain loopback
exclusions and add your management/monitoring addresses to `ignoreip`. The initial policy is ten matching requests in
ten minutes, followed by a ten-minute ban. These are example thresholds, not measured recommendations.
Both missing and wrong tokens count: ordinary unauthenticated browsing, a stale app token file or a monitor can
trigger it. One IP ban affects everyone behind that IP, including players sharing a NAT.

Check the actual journal and filter locally:

```sh
sudo journalctl -u re-psn.service -n 20 --no-pager -o cat
sudo fail2ban-regex systemd-journal /etc/fail2ban/filter.d/re-psn-status.conf --journalmatch='_SYSTEMD_UNIT=re-psn.service' --print-all-matched
```

Journal logs may contain player names and addresses; review them locally and redact them before sharing.
Confirm that matched addresses are the true request sources and that expected failures are present. A zero-match
result does not establish protection. Use your actual unit name if it differs from `re-psn.service`.
The `systemd` backend uses `journalmatch`; do not add `logpath`. Its journal timestamp supplies the event time;
the filter's `datepattern = {NONE}` keeps the embedded JSON timestamp from being stripped. This is a journal recipe,
not a file-backend configuration for historical logs.

Once those checks succeed, edit the jail to `enabled = true`, then validate and load it:

```sh
sudo fail2ban-client -t
sudo systemctl reload fail2ban.service
sudo fail2ban-client status re-psn-status
```

The fail2ban service must already be running; use your distribution's service management instructions otherwise.
Check firewall rules and reachability from a separate test host to establish that the selected action affects the
correct port and address family. Do not test by risking your only administration connection.

To remove one ban, substitute the actual address for `ADDRESS`:

```sh
sudo fail2ban-client set re-psn-status unbanip ADDRESS
```

To disable the integration, set `enabled = false`, run `fail2ban-client -t` and reload fail2ban. Check that its rules
were removed; do not flush unrelated firewall tables or disable other jails.

## Game traffic and UDP limits

This jail targets HTTP token failures only. It does not ban game TCP/UDP port 36100. Current room-code and request-rate
refusal records do not include a directly attributable source IP. Do not recover one by searching arbitrary player
names or request paths, and do not infer individual offenders from the aggregate `udp_drops` counters.

Keep the built-in connection/request limits and [UDP admission limiter](udp-guardrails.md). Invalid UDP sources may be
spoofed; automatically banning an address merely named by such traffic can block another player. Application packet
limits and fail2ban do not recover a saturated network link. The broader per-player relay budgets and attributable,
bounded abuse events remain separate work.

## Repeatable checks and coverage

With Go and fail2ban-regex installed, run the [offline checker](../tools/check_fail2ban.py):

```sh
check_dir=$(mktemp -d)
python3 tools/check_fail2ban.py --out "$check_dir/results"
```

It exercises synthetic HTTP/logger fixtures against the real fail2ban filter engine, including IPv4/IPv6, journal
prefixes and forged path/address content. It does not start fail2ban, read a live journal, install files or change
firewall rules. The Go fixture also runs with the server tests. Failures or a missing required tool fail the checker;
normal CI does not install the optional fail2ban daemon/tooling. See [testing.md](testing.md).
The output directory must be fresh and outside the checkout. Use `--fail2ban-regex /path/to/fail2ban-regex` when the
executable is outside PATH; the checker also needs the matching fail2ban-client and packaged configuration files.

No live systemd jail, firewall ban/unban, proxy/CDN integration or container enforcement is claimed by these offline
checks. Follow the host verification above before using the example.

Primary references: [filter development and security](https://github.com/fail2ban/fail2ban/blob/1.1.1/FILTERS),
[jail/filter options](https://github.com/fail2ban/fail2ban/blob/1.1.1/man/jail.conf.5),
[journal formatting](https://github.com/fail2ban/fail2ban/blob/1.1.1/fail2ban/server/filtersystemd.py),
[nftables action](https://github.com/fail2ban/fail2ban/blob/1.1.1/config/action.d/nftables.conf).
