# re-psn documentation

An index of the pages in this folder and the READMEs in the other folders, grouped by what you want to do. There is no public
instance and there are no accounts: you run your own server. The [project README](../README.md) has the overview.

## Play and connect

- [Connecting players](connecting.md): the ways players can reach a server (one LAN, port forward, public address, VPN, reverse proxy).
- [Optional desktop client](client-ui.md): run `re-psn-app` to open the Windows/Linux client in your browser.
- [Desktop app](../app/README.md): the optional app for Play, Host or Both, local trophies and authenticated friends.
- [Features](features.md): what re-psn does today and what is planned, with the evidence for each row.

## Host a server

- [Running the server](running.md): how to build and run it, its flags and environment variables, ports, logging and state.
- [Installing with the scripts](installing.md): two optional scripts that install the server from a GitHub release.
- [Running in a container](../docker/README.md): build the image and run it with Docker or Podman.
- [Deployment examples](../deploy/README.md): a settings file, a systemd unit, and Traefik, nginx, HAProxy and Caddy configs.
- [Running a server that the internet can reach](public-server.md): recommended settings for a server open to the internet.
- [Health checks and restarts](restarts.md): the health endpoint and shutdown behavior.
- [UDP rate limits and drop counters](udp-guardrails.md): the packet allowance per UDP source and its counters.
- [fail2ban for the status page](fail2ban.md): an optional example that blocks sources of refused status-page tokens.
- [Running re-psn beside a title's own server](side-service.md): using re-psn as a side service for the platform layer.

## Develop

- [Developer guide](developers.md): set up a checkout, add a feature, regenerate files and send a pull request.
- [Requesting a title](title-requests.md): what compatibility means here and what to send to ask for a game to be checked.
- [Integrating re-psn into a recompiled game](integrating.md): choose an integration path for your port.
- [Protocol](api.md): how game clients talk to the server over TCP, message by message.
- [Architecture](architecture.md): the server, the C client, the relay and how the parts fit.
- [App framework API](app-framework-api.md): the Go package for an app-owned trophy store, identities and friends.
- [Desktop profiles, trophies and authenticated friends](app-framework.md): how the desktop app uses that framework.
- [Admin API](admin-api.md): an experimental, read-only second HTTP listener.
- [Medius scope and candidate profile](medius-scope.md): a scope and evidence record, not an implementation.

## Operate

- [Running the server](running.md): the flags, ports and firewall rules.
- [Admin API](admin-api.md): read the server's state from another machine.
- [Health checks and restarts](restarts.md): supervising the server process.

## Maintain

- [Releasing](releasing.md): how a maintainer cuts a pre-release and what is checked on the way.
- [Local loopback checks](testing.md): run the native C client and Go app client against temporary servers.
- [NAT lab](../lab/README.md): reproduces players behind NAT routers in Docker, on one machine.
- [Project website and terminal view](website.md): how the project website presents the same documentation.

## Reference

- [Protocol](api.md): the game protocol, message by message.
- [Features](features.md): the full list of features and their status.
- [Medius scope and candidate profile](medius-scope.md): the candidate titles and what is recorded about each.
