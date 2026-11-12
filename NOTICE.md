# Third-party components and credits

re-psn is a fork of [sp00nznet/psnr](https://github.com/sp00nznet/psnr) (MIT), written by sp00nz, and is maintained by [undecuple](https://github.com/undecuple),
who wrote the changes since the fork. The upstream licence and copyright line are in
[LICENSE](LICENSE) and stay in this fork, with a second line for the fork's changes; the server, the C client and the protocol are upstream's work, and this fork's changes are listed in
[CHANGELOG.md](CHANGELOG.md).

Feature page generator adapted from Project Helghast (https://github.com/undecuple/project-helghast, MIT, Copyright (c) 2026 undecuple): tools/features.py.
Art generator (tools/art/make_art.py) adapted from Project Helghast (https://github.com/undecuple/project-helghast, MIT, Copyright (c) 2026 undecuple): tools/release/installer_art.py, tools/features.py.
Website, README art and desktop-app dark colours follow the page at https://undecuple.github.io/me/, whose stylesheet credits the guts.nvim colour scheme (https://github.com/vossenwout/guts.nvim); that repository has no licence file, so no licence is stated for it here and the credit names the source only.
Light colours of the desktop app and the status page from gruvbox-material (https://github.com/sainnhe/gruvbox-material, MIT, Copyright (c) 2020 sainnhe).

| Component | Use | Licence |
|---|---|---|
| Go standard library | the server (`server/`) uses nothing else | BSD-3-Clause (Go) |
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) v2.1.1, [Lip Gloss](https://github.com/charmbracelet/lipgloss) v2.0.6, `github.com/charmbracelet/x/term` and the modules they need ([list](app/README.md#dependencies)) | the desktop app's terminal UI, built into `re-psn-app` (`app/`); not in the server | MIT (Charmbracelet, Inc. and the authors of each module); `golang.org/x/sync` and `golang.org/x/sys`: BSD-3-Clause (Go) |
| `client/psnr.c`, `client/psnr.h` | the C client, one file with no dependencies, written for upstream psnr | MIT |
| [JetBrains Mono](https://github.com/JetBrains/JetBrainsMono) | the font in the images in `docs/img/`; fetched by `tools/art/make_art.py`, not committed or shipped | SIL Open Font License 1.1, JetBrains |
| [Pillow](https://python-pillow.org/) | runs `tools/art/make_art.py`; in CI only, not shipped | MIT-CMU |
| [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck), [gitleaks](https://github.com/gitleaks/gitleaks) | run in CI only; not shipped | BSD-3-Clause, MIT |
| [appimagetool](https://github.com/AppImage/appimagetool) 1.9.1, `appimagetool-x86_64.AppImage` from its [GitHub release](https://github.com/AppImage/appimagetool/releases/tag/1.9.1), SHA-256 `ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0` | packs the AppImage in the release workflow; downloaded at build time and checked against that hash, never committed, not shipped | MIT; the helper programs it carries have their own licences (mksquashfs and desktop-file-validate: GPL-2.0-or-later) |
| [AppImage type-2 runtime](https://github.com/AppImage/type2-runtime) 20251108, `runtime-x86_64` from its [GitHub release](https://github.com/AppImage/type2-runtime/releases/tag/20251108), SHA-256 `2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d` | the start of every re-psn AppImage (it mounts or extracts the rest); downloaded at build time and checked against that hash, never committed | MIT; statically linked with libfuse 3 (LGPL-2.1), squashfuse (BSD-2-Clause), zstd (BSD-3-Clause), zlib (Zlib), mimalloc (MIT) and musl (MIT) |

PlayStation, PS3 and PSN are trademarks of Sony Interactive Entertainment. This project is not affiliated with or endorsed by
Sony or any game publisher. The names are used only to say what the server stands in for; no logos or other marks are used.

**No game content is distributed.** This repository contains no game files, keys or firmware.
