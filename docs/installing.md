# Installing with the scripts

Two optional scripts install the re-psn server from a GitHub release for the current user: `scripts/install.sh` on Linux
(amd64, arm64) and `scripts/install.ps1` on Windows (amd64). They do what the
[release archives](../README.md#release-archives) section of the README says to do by hand: download the archive and
`SHA256SUMS`, check the one against the other, and put the binary in a folder of your own. macOS is not supported: there are
no macOS builds, and the Linux script says so and stops.

> [!NOTE]
> Releases built after v0.4.0 also carry the desktop app as separate archives (`re-psn-app-vX.Y.Z-linux-amd64.tar.gz`,
> `-linux-arm64.tar.gz` and `-windows-amd64.zip`, listed in `SHA256SUMS`). These scripts do not install them yet: unpack an
> app archive by hand.

## Contents

- [Get the script, then read it](#get-the-script-then-read-it)
- [Linux](#linux)
- [Windows](#windows)
- [What the scripts never do](#what-the-scripts-never-do)
- [Tests](#tests)

## Get the script, then read it

Download the script and `SHA256SUMS` from a release, check the script's hash, read it, then run it. Do not pipe it into a
shell.

> [!NOTE]
> The scripts are release assets, listed in `SHA256SUMS`, from v0.4.0; v0.3.0 and earlier do not
> carry them. Use the versioned link below (replace `vX.Y.Z` with a release from the
> [releases page](https://github.com/undecuple/re-psn/releases)): every release so far is a pre-release, and GitHub's
> `releases/latest/download/...` link does not point at pre-releases, so it gives a 404.

Linux:

```sh
curl -fsSL --proto '=https' --tlsv1.2 -O https://github.com/undecuple/re-psn/releases/download/vX.Y.Z/install.sh
curl -fsSL --proto '=https' --tlsv1.2 -O https://github.com/undecuple/re-psn/releases/download/vX.Y.Z/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing   # must print "install.sh: OK"
less install.sh
```

Windows (the third command prints `True` when the hash matches the one in `SHA256SUMS`; continue only if it does):

```powershell
Invoke-WebRequest -UseBasicParsing -OutFile install.ps1 https://github.com/undecuple/re-psn/releases/download/vX.Y.Z/install.ps1
Invoke-WebRequest -UseBasicParsing -OutFile SHA256SUMS https://github.com/undecuple/re-psn/releases/download/vX.Y.Z/SHA256SUMS
(Get-FileHash install.ps1 -Algorithm SHA256).Hash -eq (Select-String -Path SHA256SUMS -Pattern '  install\.ps1$').Line.Split(' ')[0]
notepad install.ps1
```

The release's `install.sh` and `install.ps1` are byte for byte the files in [scripts/](../scripts/) at the release's tag; the
release workflow checks that before the draft is made, and again once it is published. `SHA256SUMS` is unsigned, so a match
shows the script is the one the release lists, not who made the release: read it before running it. What the script then
downloads is checked against the same `SHA256SUMS`. From a clone, `git show vX.Y.Z:scripts/install.sh > install.sh` gives the
same file.

## Linux

```sh
sh install.sh                     # the newest release, pre-releases included, into ~/.local/bin
sh install.sh --version v0.4.0    # a pinned release (or RE_PSN_VERSION=v0.4.0)
sh install.sh --dir ~/bin         # another folder (or RE_PSN_INSTALL_DIR)
sh install.sh --appimage          # re-psn.AppImage instead of the binary (x86_64 only)
sh install.sh --dry-run           # print the steps; download and write nothing
```

It runs under dash, bash and BusyBox ash, and needs curl (or GNU wget), tar, gzip and one of `sha256sum`, `shasum` or `openssl`.
It reads the release list from `api.github.com` only when no version is pinned, and downloads only from
`https://github.com/undecuple/re-psn/releases/download/`, over HTTPS with TLS 1.2 or newer; GitHub redirects downloads to its own
file servers. BusyBox wget is refused, since it cannot be limited to HTTPS.

The binary goes to `~/.local/bin/re-psn` with mode 0755: it is written under a temporary name in the same folder and then renamed
over the old one, so a running server keeps its file and a failed run leaves nothing half-written. The archive's `LICENSE`,
`NOTICE.md` and `examples/` (the settings file, the systemd unit and the proxy examples) go to
`~/.local/share/re-psn/vX.Y.Z/` (under `XDG_DATA_HOME` if set), and an existing folder for that version is kept as it is. When
the folder is not on `PATH`, the script prints the line to add to `~/.profile`, for example:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Then `re-psn` starts the server; `re-psn -http 127.0.0.1:36101` also turns on the status page. See
[running.md](running.md) for the flags.

## Windows

```powershell
powershell -ExecutionPolicy Bypass -File install.ps1               # the newest release, into %LOCALAPPDATA%\re-psn\bin
powershell -ExecutionPolicy Bypass -File install.ps1 -Version v0.4.0
powershell -ExecutionPolicy Bypass -File install.ps1 -AddToPath    # also add that folder to the user PATH
powershell -ExecutionPolicy Bypass -File install.ps1 -DryRun
```

It is written for Windows PowerShell 5.1 and PowerShell 7 (`pwsh -File install.ps1`) but has been tested on PowerShell 7 only; it needs no administrator rights, turns on TLS 1.2,
and checks the zip with `Get-FileHash` against `SHA256SUMS`. `-Dir` picks another folder. `re-psn.exe` is copied under a
temporary name and moved over the old one; a running server has to be stopped first, and the script says so. `LICENSE`,
`NOTICE.md` and `examples\` go to `%LOCALAPPDATA%\re-psn\vX.Y.Z\`. Without `-AddToPath` it changes no PATH and prints the
command that would add the folder to the user PATH. `-AddToPath` rewrites the user PATH as plain text, so an entry that used `%VARIABLE%` references loses its expansion.

## What the scripts never do

- Use sudo or ask for administrator rights. The Linux script refuses to run as root unless `--allow-root` is given.
- Edit a shell profile, or change PATH (on Windows, only the user PATH and only with `-AddToPath`).
- Install anything when the checksum does not match or `SHA256SUMS` has no line for the download.
- Download from any address other than the ones above, or run anything they download.

## Tests

`sh scripts/test-install.sh [SHELL]` runs the Linux script against a fake release with stub `curl`, `uname` and `id` commands
and no network: both CPUs, a checksum mismatch, a missing line, macOS and an unknown CPU, `--dry-run`, `--dir`, the PATH hint,
root, an existing binary replaced by a rename, the AppImage, and the `shasum` and `openssl` fallbacks.
`pwsh -NoProfile -File scripts/test-install.ps1` does the same for the Windows script, with the downloads and the user PATH
replaced by test functions.
