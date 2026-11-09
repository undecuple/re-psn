# Project website and terminal view

The [project website](https://undecuple.github.io/re-psn/) presents the same features, settings, installation guidance and
documentation links as this repository. It works as a normal document without JavaScript. Phones start in that plain
view; choose **terminal view** to use the menu and command line. **plain view** returns to the document at any time.

## Numbered output

Each successful command appears as a file with a name, line-number gutter and frame. Wrapped text keeps one line number,
with an empty gutter beside continuation rows. The header and gutter numbers are excluded from text selection. Headings,
options, environment variables and numbers use the existing dark/light palette; colour is an additional cue.

Type `help` for commands. While typing, Tab or Right accepts a suggestion, Up/Down chooses a suggestion, and Esc closes it.
With no suggestion open, Up/Down recalls command history. Commands and settings are read from the site's data; the site
does not connect to a game server or change its configuration.

The theme choice is stored in this browser when local storage is available; it is the only saved choice. Storage
failures leave the interface usable.

## Contributor checks

`tools/site.py` generates `site/index.html` and `site/data.json`. Edit their repository sources, then regenerate;
`site/style.css` and `site/terminal.js` are maintained directly. The deployed page uses one script with no dependencies
and loads its data from the same origin.

```sh
python3 tools/features.py --check
python3 tools/site.py --check
python3 -m unittest
node --test tools/terminal.test.js
```

The optional browser check requires Node 22 or newer and a locally installed Chromium-compatible browser. It adds no
package dependency and is separate from the ordinary unit tests. Use three terminals and a fresh disposable browser
profile; keep both listeners on loopback:

```sh
# Terminal 1: serve the local candidate.
python3 -m http.server 8000 --bind 127.0.0.1 --directory site

# Terminal 2: use a fresh profile, not a personal browsing session.
chromium --headless --no-first-run --disable-background-networking --disable-component-update --disable-sync \
  --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 \
  --user-data-dir=/tmp/re-psn-site-check about:blank

# Terminal 3: check interactions and save local screenshots/results.
node tools/site_browser_check.mjs 9222 http://127.0.0.1:8000/ work/site-browser-check
```

The check creates and closes its own browser tab. Stop the browser and HTTP server afterward. It exercises command entry, autocomplete/history, theme and resize changes, text selection, narrow layouts, reduced motion,
forced-colors mode, data-load failure, and the no-JavaScript document. It checks for uncaught exceptions and external
page requests. Emulation and programmatic key input do not replace testing on a physical phone, real keyboard, or screen
reader. Browser checks are contributor tooling, not part of the deployed site.
