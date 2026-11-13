# Building re-psn

Where to find how to build and test each part. The steps themselves are kept in one place each, so they do not drift;
this page only points to them.

| What | Where | Needs |
|---|---|---|
| The server (`re-psn`) | [README, From source](README.md#from-source); `Setup.cmd` (Windows) or `./setup.sh` (Linux, macOS) do it for you | Go 1.22 or newer (`go.mod`); a C compiler only for the C client and its tests |
| The desktop app (`re-psn-app`) | [app/README.md, Build and test](app/README.md#build-and-test) | Go 1.26 or newer (`app/go.mod`); the app is a separate Go module |
| The C client | [client/psnr.c](client/psnr.c), built and tested with the server's tests: [README, From source](README.md#from-source) | a C compiler |
| The container image | [docker/README.md, Build and run](docker/README.md#build-and-run) | Docker or Podman |
| Release archives and the AppImage | [docs/releasing.md](docs/releasing.md) (`scripts/release-build.sh`, `scripts/build-appimage.sh`) | for maintainers; see that page |

## Tests

- Before a pull request: [CONTRIBUTING.md, Testing](CONTRIBUTING.md#testing) lists what to run.
- Local loopback checks: [docs/testing.md](docs/testing.md).
- The NAT lab (players behind routers in Docker): [lab/README.md](lab/README.md).

If a step here does not work as written, please open an [issue](https://github.com/undecuple/re-psn/issues/new/choose).
