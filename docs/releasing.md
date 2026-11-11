# Releasing

How a maintainer cuts a re-psn pre-release: where the version lives, the bump, the tag, the draft release and what is
checked on the way. Pre-releases come often and on no schedule, whenever there is something worth trying.

> [!NOTE]
> The first release, v0.2.0, was cut by this process on 2026-11-06 as a pre-release. The tag step and the draft job ran on that
> tag and passed; the draft was published by hand after its assets were read back. What has still not been checked is listed in
> [Not checked yet](#not-checked-yet).

## Contents

- [Versions and tags](#versions-and-tags)
- [Cut a pre-release](#cut-a-pre-release)
- [Before the first tag: the dry run](#before-the-first-tag-the-dry-run)
- [The AppImage](#the-appimage)
- [The container image](#the-container-image)
- [Checked automatically](#checked-automatically)
- [Not checked yet](#not-checked-yet)

## Versions and tags

- [`VERSION`](../VERSION) holds the version, `X.Y.Z`. The default of `var version` in `server/main.go`, the newest numbered
  heading in [CHANGELOG.md](../CHANGELOG.md) and the `<!-- version -->...<!-- /version -->` span in the
  [README](../README.md) status line state the same number; `TestVersionAgrees` fails when they differ. Release builds
  also set the version with `-ldflags -X main.version=...` from the tag.
- The status badge in the README top row (`status-alpha`) is not inside the version span, so a bump does not touch it. Change it
  by hand only when the project's stage changes (for example from alpha to beta), in the same pull request as that decision.
- A tag is `v` + `VERSION` at the tagged commit, for example `v0.2.0`. Tags are never moved, deleted or reused. A bad
  release is followed by the next version.
- Every release is a pre-release while the version is below 1.0.0: the workflow creates it with the pre-release flag,
  the notes start with a pre-release notice, and `release-check` fails a 0.x release that is not marked pre-release.
- The notes end with `Next release: when ready`. There is no release day and no skipped cycle to announce.
- `VERSION` starts at 0.1.0, the number the code and CHANGELOG.md already state (the original psnr's first version). No
  `v0.1.0` tag exists. The first pre-release of re-psn takes a new number with the first bump; 0.2.0 fits, since the
  Unreleased entries include protocol changes that clients built against 0.1.0 do not match.

## Cut a pre-release

1. Start from a current `main` with CI green. Everything to ship is merged and listed under `## [Unreleased]` in
   CHANGELOG.md.
2. On a new branch with a clean working tree, look at the bump, then make it:

   ```sh
   scripts/bump-version.sh --dry-run 0.2.0   # prints the diff, writes nothing
   scripts/bump-version.sh 0.2.0
   ```

   It changes exactly four files: `VERSION`, the `var version` default in `server/main.go`, `CHANGELOG.md` (the
   Unreleased entries move under `## [0.2.0] - <today, UTC>`, a fresh empty Unreleased stays above, and an
   `[Unreleased]: .../compare/...` link moves on if the file has one) and the version span in `README.md`. It refuses a
   version that is not `X.Y.Z` or not newer than `VERSION`, an empty Unreleased section, a CHANGELOG whose newest version
   is not `VERSION`, and a dirty working tree. `--date YYYY-MM-DD` sets the heading's date. It writes the four files
   first and then builds the notes for the new version with `scripts/release-notes.sh`, failing if they do not build; a
   failure there leaves the four files changed for you to look at (`git diff`, `git checkout -- .` to undo).
3. Review: `git diff` shows only those version places, the span and the CHANGELOG move. Then run the checks and read the
   notes:

   ```sh
   scripts/check-release.sh
   scripts/release-notes.sh 0.2.0
   ```

4. Commit the four files by name as one commit, with nothing else in it, open a pull request, and merge it once CI is
   green and it is reviewed. The script makes no commit, tag or push.
5. Tag the merge commit on `main`, and only with the maintainer's word:

   ```sh
   git tag -a v0.2.0 -m v0.2.0 <merge commit>
   git push origin v0.2.0
   ```

6. The tag starts [`.github/workflows/release.yml`](../.github/workflows/release.yml). It first runs
   `scripts/check-release.sh --tag <tag>`, which fails unless the tag is `v` + `VERSION` at the tagged commit and the
   version checks pass; nothing is built after a failure. Then vet and tests, the server archives for windows/amd64, linux/amd64
   and linux/arm64, the same three archives for the desktop app (`re-psn-app-vX.Y.Z-<os>-<arch>`, built from `app/`),
   and the x86_64 [AppImage](#the-appimage), copies of `scripts/install.sh` and `scripts/install.ps1`
   (byte for byte, as `install.sh` and `install.ps1`; see [installing.md](installing.md)), and one `SHA256SUMS` listing
   those nine files (LF, no BOM); `scripts/check-release.sh --assets` then checks that exactly the ten assets are there.
   Then the notes, and a **draft** pre-release
   titled with the tag. The notes are `scripts/release-notes.sh` for `VERSION` (the CHANGELOG.md entries under that
   version's heading, which the bump filled) with the list of files, the checksums and the closing line
   `Next release: when ready` added. The job fails if a release for the tag exists already.
7. Check the draft: the notes, the assets (three server archives, three app archives, the AppImage, `install.sh`, `install.ps1`, `SHA256SUMS`: ten
   in all), and a download checked with `sha256sum -c SHA256SUMS`. A bad draft is deleted before it is published; then re-run the workflow for the same
   tag, or, if the tagged commit is wrong, bump to the next version.
8. Publishing is the maintainer's. Publishing starts
   [`.github/workflows/release-check.yml`](../.github/workflows/release-check.yml), which reads the release back.

Manual `release-check` runs still accept the historical lists: nine assets for v0.2.0 (each download with a `.sha256` file,
and `SHA256SUMS`), five for v0.3.0 and seven for v0.4.0 (no app archives). Later tags require all ten assets and compare both installer scripts with the
tagged source.
9. Afterwards: CI is green on the tagged commit, and a fixed issue gets a comment naming the release.

> [!NOTE]
> One bump is one commit, made by the maintainer. A fix found after the tag goes into the next pre-release, never into a
> moved tag.

## Before the first tag: the dry run

Run the release workflow by hand (Actions, release, Run workflow). A manual run builds the archives and the AppImage for
`v0.0.0`, copies the install scripts, checks the ten assets with `scripts/check-release.sh --assets`, and keeps them,
with `notes.md`, as a run artifact; it creates no release and has no write permission. The tag check is skipped (there
is no tag), but `scripts/release-build.sh` still runs `scripts/check-release.sh`, and the notes are built the same way as on
a tag, for the version in `VERSION`: what a tag at that commit would get. Before the first tag, check that artifact by hand: the sizes, the checksums
against `SHA256SUMS`, and that the Linux archive unpacks in an empty directory and the server inside starts without Go or
build tools. The Windows and arm64 archives are cross-built; say in the notes which ones were not run.

`scripts/release-build.sh v0.0.0` builds the same archives and copies the install scripts locally into `dist/` (ignored by
git); without the AppImage that is nine files, so the ten-asset check runs only with it. To build the AppImage too:

```sh
scripts/build-appimage.sh fetch /tmp/appimage-tools      # the only download; checked against the pins
scripts/release-build.sh v0.0.0 --appimage-tools /tmp/appimage-tools
```

## The AppImage

`re-psn-vX.Y.Z-x86_64.AppImage` is the linux/amd64 server as one runnable file, for people who would rather not unpack an
archive. It is a command-line server: double-clicking it does nothing useful. `./re-psn-vX.Y.Z-x86_64.AppImage -h` works
like `re-psn -h`, and any other arguments go to the server the same way.

- **Contents:** the same `re-psn` binary as `re-psn-vX.Y.Z-linux-amd64.tar.gz`, byte for byte, the examples (`deploy/`,
  in `usr/share/re-psn/examples`), `LICENSE` and `NOTICE.md`, and from [`scripts/appimage/`](../scripts/appimage) an
  `AppRun` that runs the binary with the arguments given, a `.desktop` file and an icon (a small SVG in the colours of
  [`docs/art/palette.json`](art/palette.json); the art in `docs/img/` has no square image). `--appimage-extract` unpacks
  it into `squashfs-root/`, which is how to get at the examples.
- **Running it:** `chmod +x` it first. Mounting needs FUSE on the host (the kernel module and `fusermount3` or
  `fusermount`; the runtime carries its own libfuse, so no libfuse2 package is needed). Without FUSE, for example in
  most containers, run it with `--appimage-extract-and-run` as the first argument: it unpacks to a temporary directory,
  runs, and cleans up afterwards. The tarball needs neither.
- **x86_64 only.** The arm64 binary is cross-built, and an aarch64 AppImage could not be run once on the x86_64 runner
  before it ships; arm64 hosts take the tarball.
- **Unsigned,** like the rest of the release (signing is planned later, with Windows signing; see
  [ROADMAP.md](../ROADMAP.md)). Check a download against `SHA256SUMS`:

  ```sh
  sha256sum -c SHA256SUMS --ignore-missing
  ```

How it is built ([`scripts/build-appimage.sh`](../scripts/build-appimage.sh), run by `scripts/release-build.sh` with
`--appimage-tools`):

1. `release.yml` downloads two files, each from a fixed GitHub release and checked against its SHA-256 before anything
   else happens (a mismatch stops the job):

   | File | Version and source | SHA-256 | Licence |
   |---|---|---|---|
   | `appimagetool-x86_64.AppImage` | [appimagetool 1.9.1](https://github.com/AppImage/appimagetool/releases/tag/1.9.1) | `ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0` | MIT |
   | `runtime-x86_64` | [type-2 runtime 20251108](https://github.com/AppImage/type2-runtime/releases/tag/20251108) | `2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d` | MIT |

   Both are x86_64 programs and run on the x86_64 `ubuntu-latest` runner. Nothing is committed; [NOTICE.md](../NOTICE.md)
   records them. To move to a newer version, change the URL and hash in both `scripts/build-appimage.sh` and
   `release.yml` (`TestAppImagePinsAgree` fails when they differ), here and in NOTICE.md.
2. The script checks both hashes again, on its own copies, and refuses to run anything on a mismatch. It refuses a tag
   that is not `vMAJOR.MINOR.PATCH`, or not `v` + `VERSION` (except the dry run's `v0.0.0`).
3. It builds the AppDir from the linux/amd64 archive in `dist/`, with fixed modes and every mtime set to
   `SOURCE_DATE_EPOCH` (default 0), and packs it with appimagetool and `--runtime-file`, so appimagetool fetches
   nothing. Two builds of the same commit with the same Go give the same file.
4. Smoke test: the files in the AppImage equal the AppDir, its binary equals the archive's, `--appimage-extract-and-run -h`
   prints what the binary's `-h` prints (apart from the program path on the first line), and the server it starts on
   loopback reports the tag's version.
5. It adds the AppImage to `SHA256SUMS`, and `sha256sum -c SHA256SUMS` runs on the lot.

## The container image

`.github/workflows/container.yml` builds `docker/Dockerfile` for linux/amd64 and linux/arm64 and pushes `ghcr.io/<owner>/re-psn`.

- A published (not draft) release runs it and pushes. The workflow file is the one in the release's tag, so a tag made before the
  file existed does not trigger it.
- A manual run takes the release tag and a `push` choice. With `push` off (the default) it builds both platforms and pushes nothing:
  the dry run. With `push` on it pushes, which is also how an older tag such as `v0.2.0` gets its image: the run checks out that tag's
  Dockerfile.
- The tag must be v + VERSION (`scripts/check-release.sh --tag`). The amd64 image is started and must report the version and run as
  uid 10001 before anything is pushed.
- Tags: `X.Y.Z` always; `X.Y` and `latest` only for a release that is not a pre-release. A manual run counts as a pre-release, so it never pushes `X.Y` or `latest`.
- The image is unsigned, and the workflow switches the provenance and SBOM attestations off, so it is a plain image.
- The package starts private under a user account. Making it public, and linking it to the repository, is a setting changed by hand;
  the image's `org.opencontainers.image.source` label points at this repository.

## Checked automatically

| Check | Where |
|---|---|
| `VERSION`, `server/main.go`, the newest CHANGELOG.md version and the README span agree (`TestVersionAgrees`) | `go test ./...` in CI on pushes to `main` and pull requests; `scripts/check-release.sh`; `scripts/release-build.sh` |
| The bump and notes scripts on a fixture tree (`TestBumpVersion`, `TestReleaseNotes`) | the same; skipped on Windows, where the scripts do not run |
| The notes for the current version build from the real CHANGELOG.md | `scripts/check-release.sh`; `release.yml` builds the notes with `scripts/release-notes.sh` |
| Tag shape `vMAJOR.MINOR.PATCH` | `scripts/release-build.sh`, both release workflows |
| The tag is `v` + `VERSION` at the tagged commit (`TestCheckReleaseTag` for the script) | `scripts/check-release.sh --tag`, first step of `release.yml` on a tag |
| The deploy examples match the server's flags | `scripts/check-deploy.sh`, run by `scripts/release-build.sh` |
| `SHA256SUMS` verifies | `scripts/release-build.sh`, `release.yml` (build and draft jobs), `release-check.yml` |
| The build holds exactly the ten assets, `SHA256SUMS` has one `HASH  NAME` line (LF, no BOM) for each of the other nine, and `install.sh` and `install.ps1` are the tree's scripts byte for byte (`TestCheckReleaseAssets`); the script and both workflows name the same ten (`TestReleaseAssetListsAgree`) | `scripts/check-release.sh --assets`, run by `scripts/release-build.sh` with the AppImage, in `release.yml`; the tests in `go test ./...` |
| appimagetool and the runtime match their SHA-256 pins; the script and the workflow pin the same files (`TestAppImagePinsAgree`); a wrong hash or tag is refused before anything runs (`TestBuildAppImageRefuses`) | `release.yml`, `scripts/build-appimage.sh`; the tests in `go test ./...` |
| The AppImage holds the archive's binary, and its `-h` and version match | `scripts/build-appimage.sh` (smoke test), in `release.yml` |
| No release exists for the tag yet | `release.yml` |
| Published, titled with the tag, pre-release while 0.x, exactly the ten assets (the historical lists for v0.2.0, nine, v0.3.0, five, and v0.4.0, seven), checksums, and `install.sh` and `install.ps1` equal to the files at the tag; `TestReleaseCheckWorkflow` exercises metadata and script comparison with local API responses | `release-check.yml` |

## Not checked yet

- That the Windows and arm64 binaries run; they are cross-built. The exe, the AppImage and `SHA256SUMS` are unsigned.
- The AppImage on other distributions: it was built and run (with FUSE and with `--appimage-extract-and-run`) on one
  x86_64 Linux machine only.
- The ten-asset release: v0.4.0 carries seven assets, v0.3.0 five and v0.2.0 nine. The upload of the three app archives to a
  draft, and `release-check.yml` reading ten assets back and comparing the scripts with the tag
  through the contents API, run for the first time on the next tag. The app archives have never been built by the release
  workflow, and the Windows and arm64 app binaries have not been run. A manual run of the release workflow builds and checks
  the ten files but creates no draft.
- Sample log output in the README and `docs/running.md` (`version=0.1.0`) is an example and is not changed by the bump.
