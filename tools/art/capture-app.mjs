// Drives the real app UI in headless Chromium and writes screenshots; used by capture-app.sh only.
//   node tools/art/capture-app.mjs DEBUG_PORT LINK_FILE OUT_DIR GAME_ADDR PLAY_ADDR STATUS_URL
// GAME_ADDR is the address the app's own owned server will listen on (Host screen); PLAY_ADDR and STATUS_URL are a separate
// loopback server that already holds a room and a score (Play screen). All data is synthetic.
import {mkdir} from 'node:fs/promises';
import {readFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {connect} from '../site_browser_cdp.mjs';

const [port, linkFile, outDir, gameAddr, playAddr, statusUrl] = process.argv.slice(2);
await mkdir(outDir, {recursive: true});
const c = await connect(Number(port));
const link = (await readFile(linkFile, 'utf8')).trim();
const click = selector => c.evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);
const fill = (selector, value) => c.evaluate(`{const x=document.querySelector(${JSON.stringify(selector)});x.value=${JSON.stringify(value)};x.dispatchEvent(new Event('input',{bubbles:true}));}`);
const idle = () => c.waitFor(`!document.querySelector('#refresh-state').disabled && document.querySelector('#error').hidden`);
const shot = async name => { await new Promise(r => setTimeout(r, 400)); await c.screenshot(resolve(outDir, name + '.png')); console.log('wrote', name + '.png'); };
const page = async name => { await click(`.navigation [data-page="${name}"]`); await c.waitFor(`!document.querySelector('#page-${name}').hidden`); await idle(); };

await c.call('Emulation.setDeviceMetricsOverride', {width: 1280, height: 1100, deviceScaleFactor: 1, mobile: false});
await c.call('Page.enable'); await c.call('Page.navigate', {url: link});
await c.waitFor(`!!document.querySelector('#welcome') && !document.querySelector('#welcome').hidden`);
await idle();
await shot('app-welcome');

await click('[data-role="both"]');
await c.waitFor(`!document.querySelector('#workspace').hidden`);
await idle();

await page('host');
await fill('#host-bind', gameAddr);
await c.evaluate(`document.querySelector('#host-form').requestSubmit()`);
await c.waitFor(`document.querySelector('#host-badge').textContent === 'Running'`);
await idle();
await shot('app-host');

await page('play');
await fill('#play-server', playAddr);
await fill('#play-status-url', statusUrl);
await c.evaluate(`document.querySelector('#play-form').requestSubmit()`);
await c.waitFor(`document.querySelector('#play-badge').textContent === 'Reachable'`);
await idle();
await shot('app-play');

await page('profile');
await fill('#profile-name', 'alice');
await c.evaluate(`document.querySelector('#profile-create-form').requestSubmit()`);
await c.waitFor(`!document.querySelector('#profile-details').hidden`);
await idle();
await shot('app-profile');

await page('overview');
await shot('app-overview');
process.exit(0);
