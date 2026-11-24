const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ui = require('../app/internal/webui/assets/app.js');
const nonce = '1'.repeat(64), token = '2'.repeat(64);
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});
function storage(seed) {
  const data = new Map(seed ? [[ui.SESSION_KEY, seed]] : []), writes = [];
  return {data, writes, getItem: key => data.get(key), removeItem: key => data.delete(key), setItem: (key, value) => {writes.push(value); data.set(key, value);}};
}
test('private fragment is stripped before storage access and never becomes the stored API capability', async () => {
  let stripped = false;
  const store = storage(token), history = {replaceState: (_s, _t, target) => {assert.equal(target, '/'); stripped = true;}};
  const capture = ui.takeToken({hash: '#token=' + nonce, pathname: '/', search: '?ignored=1'}, history, () => {assert.equal(stripped, true); return store;});
  assert.deepEqual(capture, {bootstrap: nonce, token: ''});
  assert.equal(store.data.has(ui.SESSION_KEY), false);
  const calls = [];
  const result = await ui.openSession(capture, store, async (url, opts) => {calls.push(url); assert.equal(url, '/api/open'); assert.equal(opts.method, 'POST'); assert.equal(opts.headers.Authorization, 'Bearer ' + nonce); assert.equal(opts.body, '{}'); assert.equal(opts.credentials, 'omit'); assert.equal(opts.redirect, 'error'); return json({token});});
  assert.equal(result, token); assert.deepEqual(store.writes, [token]); assert.deepEqual(calls, ['/api/open']);
});
test('fresh invalid/spent bootstrap cannot fall back to an old session', async () => {
  const store = storage(token);
  assert.throws(() => ui.takeToken({hash: '#token=' + nonce + '&extra=1', pathname: '/'}, {replaceState() {}}, store), /private launch link/);
  assert.equal(store.data.has(ui.SESSION_KEY), false);
  const capture = ui.takeToken({hash: '#token=' + nonce, pathname: '/'}, {replaceState() {}}, store);
  await assert.rejects(ui.openSession(capture, store, async () => json({error: 'spent'}, 401)), /expired|already used/);
  assert.equal(store.data.has(ui.SESSION_KEY), false);
  await assert.rejects(ui.openSession(capture, store, async () => json({token: '<invalid>'})), /Invalid private session/);
});
test('fragment launch still works when browser storage is unavailable; reload requires a saved API key', async () => {
  let stripped = false;
  const capture = ui.takeToken({hash: '#token=' + nonce, pathname: '/local'}, {replaceState() {stripped = true;}}, () => {assert.equal(stripped, true); throw new Error('storage blocked');});
  assert.equal(await ui.openSession(capture, () => {throw new Error('blocked');}, async () => json({token})), token);
  assert.throws(() => ui.takeToken({hash: '', pathname: '/'}, {replaceState() {}}, null), /Relaunch/);
  const saved = ui.takeToken({hash: '', pathname: '/'}, {replaceState() {}}, storage(token));
  assert.equal(await ui.openSession(saved, null, () => {throw new Error('no exchange on refresh');}), token);
});
test('API requests stay on fixed local routes with header authorization and strict fetch policies', async () => {
  const calls = [], api = ui.makeAPI(token, async (url, options) => {calls.push([url, options]); return json({settings: {mode: 'play'}});});
  await api.state(); await api.post('/api/friends', {action: 'request', id: 'rp1-' + 'a'.repeat(64)});
  assert.equal(calls[0][0], '/api/state'); assert.equal(calls[0][1].method, 'GET');
  const [url, options] = calls[1]; assert.equal(url, '/api/friends'); assert.equal(options.method, 'POST'); assert.equal(options.headers.Authorization, 'Bearer ' + token); assert.equal(options.headers['Content-Type'], 'application/json'); assert.equal(options.cache, 'no-store'); assert.equal(options.credentials, 'omit'); assert.equal(options.redirect, 'error'); assert.equal(options.referrerPolicy, 'no-referrer'); assert.equal(JSON.parse(options.body).action, 'request');
  for (const foreign of ['https://example.invalid/api/state', '//example.invalid', '/api/open', '/api/state?token=' + token, '/api/exec']) await assert.rejects(api.post(foreign), /Unsupported/);
  await assert.rejects(api.post('/api/state'), /Unsupported/); assert.equal(calls.length, 2);
});
test('saved-server routes are fixed local POSTs and nothing else under that prefix is reachable', async () => {
  const calls = [], api = ui.makeAPI(token, async (url, options) => {calls.push([url, options]); return json({settings: {mode: 'play'}});});
  for (const route of ['add', 'remove', 'check', 'check-all', 'use', 'export', 'preview', 'import']) await api.post('/api/servers/' + route, {server: 'h:1'});
  assert.deepEqual(calls.map(c => c[0]), ['add', 'remove', 'check', 'check-all', 'use', 'export', 'preview', 'import'].map(r => '/api/servers/' + r));
  assert.ok(calls.every(c => c[1].method === 'POST' && c[1].redirect === 'error' && c[1].credentials === 'omit' && c[1].headers.Authorization === 'Bearer ' + token));
  for (const other of ['/api/servers', '/api/servers/', '/api/servers/import/', '/api/servers/../settings', '/api/servers/add?x=1']) await assert.rejects(api.post(other, {}), /Unsupported/);
  assert.equal(calls.length, 8);
});
test('API errors are bounded text, redact capabilities and distinguish an expired session', async () => {
  const api = ui.makeAPI(token, async () => json({error: '<script>bad</script> ' + token}, 400));
  await assert.rejects(api.post('/api/settings', {}), error => error.message === '<script>bad</script> [redacted]');
  await assert.rejects(ui.makeAPI(token, async () => json({}, 401)).state(), error => error.status === 401 && /session expired/.test(error.message));
  await assert.rejects(ui.makeAPI(token, async () => new Response('{malformed')).state(), SyntaxError);
  await assert.rejects(ui.makeAPI(token, async () => new Response(new Uint8Array([0xff]))).state(), TypeError);
});
test('catalog preview does not erase duplicate/case-alias/unknown input from backend validation', () => {
  const raw = '{"title_id":"ONE","title_id":"TWO","Title_ID":"ALIAS","unknown":true}';
  const posted = JSON.parse(JSON.stringify(ui.catalogBody(raw)));
  assert.equal(posted.catalog, raw); assert.equal(typeof posted.catalog, 'string');
  assert.throws(() => ui.catalogBody('{malformed'), SyntaxError);
  assert.throws(() => ui.catalogBody('[]'), /JSON object/);
  assert.throws(() => ui.catalogBody(' '.repeat(4 * 1024 * 1024 + 1)), /4 MiB/);
});
test('backup transport accepts ciphertext only and enforces streaming/decoded size limits', async () => {
  const archive = new Uint8Array(64); archive.set(new TextEncoder().encode('RPSNBAK1'));
  const api = ui.makeAPI(token, async () => new Response(archive, {headers: {'Content-Type': 'application/octet-stream'}}));
  assert.deepEqual(await api.export('synthetic passphrase'), archive);
  await assert.rejects(ui.makeAPI(token, async () => json({seed: 'unexpected'})).export('synthetic passphrase'), /encrypted backup/);
  await assert.rejects(ui.makeAPI(token, async () => new Response(new Uint8Array(64), {headers: {'Content-Type': 'application/octet-stream'}})).export('synthetic passphrase'), /supported encrypted/);
  const chunks = new ReadableStream({start(controller) {controller.enqueue(new Uint8Array(8)); controller.enqueue(new Uint8Array(9)); controller.close();}});
  await assert.rejects(ui.responseBytes(new Response(chunks), 16), /size limit/);
  await assert.rejects(ui.responseBytes(new Response('small', {headers: {'Content-Length': '99'}}), 16), /size limit/);
  assert.throws(() => ui.base64(new Uint8Array(ui.MAX_ARCHIVE + 1)), /size limit/);
  const bytes = new Uint8Array([0, 1, 127, 128, 255]); assert.deepEqual([...Buffer.from(ui.base64(bytes), 'base64')], [...bytes]);
});
test('role and target policies keep player and operator contexts separate', () => {
  for (const role of ['', 'play', 'host', 'both']) {assert.equal(ui.personal(role), ['play', 'both'].includes(role)); assert.equal(ui.hosting(role), ['host', 'both'].includes(role));}
  assert.equal(ui.clientAddress('0.0.0.0:36100'), '127.0.0.1:36100'); assert.equal(ui.clientAddress('[::]:36100'), '[::1]:36100'); assert.equal(ui.clientAddress('192.0.2.2:36100'), '192.0.2.2:36100');
  assert.equal(ui.localBind('127.0.0.1:36100'), true); assert.equal(ui.localBind('[::1]:36100'), true); assert.equal(ui.localBind('0.0.0.0:36100'), false);
  const state = {settings: {social_origin: 'https://one.example.invalid/'}, profile: {relays: [{origin: 'https://two.example.invalid', state: {friends: ['other']}}, {origin: 'https://one.example.invalid', state: {friends: ['selected']}}]}};
  assert.deepEqual(ui.selectedRelay(state).state.friends, ['selected']); state.settings.social_origin = 'https://unknown.example.invalid'; assert.equal(ui.selectedRelay(state), undefined);
});
test('ageWords gives plain elapsed time', () => {
  const now = 1_000_000_000;
  assert.equal(ui.ageWords(0, now), 'not checked');
  assert.equal(ui.ageWords(now - 12000, now), '12 s ago');
  assert.equal(ui.ageWords(now + 5000, now), '0 s ago');
  assert.equal(ui.ageWords(now - 59000, now), '59 s ago');
  assert.equal(ui.ageWords(now - 180000, now), '3 min ago');
  assert.equal(ui.ageWords(now - 3600000, now), 'over an hour ago');
});
test('setupSteps computes rows from state alone for each role', () => {
  const play = {settings: {mode: 'play', server: '', status_url: '', name: ''}, host: {running: false, address: ''}, play: {checked_at: 0}};
  assert.deepEqual(ui.setupSteps(play).map(s => [s.id, s.done]), [['server', false], ['name', false], ['check', false]]);
  play.settings.server = 'play.example.org:36100'; play.settings.name = 'player1'; play.play.checked_at = 5;
  assert.deepEqual(ui.setupSteps(play).map(s => s.done), [true, true, true]);
  assert.match(ui.setupSteps(play)[1].detail, /Not checked, not protected by a password/);
  const host = {settings: {mode: 'host', host_address: '127.0.0.1:36100'}, host: {running: false, address: ''}, play: {checked_at: 0}};
  let rows = ui.setupSteps(host);
  assert.deepEqual(rows.map(s => [s.id, s.done]), [['listen', true], ['running', false], ['share', false]]);
  assert.match(rows[0].detail, /Friends cannot reach it/);
  host.host = {running: true, address: '127.0.0.1:36100'}; assert.equal(ui.setupSteps(host)[2].done, false);
  host.host = {running: true, address: '192.0.2.2:36100'}; assert.equal(ui.setupSteps(host)[2].done, true);
  assert.equal(ui.setupSteps({...host, settings: {mode: 'both', host_address: '192.0.2.2:36100'}}).length, 6);
  assert.ok(ui.setupSteps(play).every(s => ['play', 'settings', 'host'].includes(s.page)));
});
test('the setup checklist makes no request before its button is pressed', async () => {
  const calls = [];
  const api = ui.makeAPI(token, async (url, options) => {calls.push(url); return json({settings: {mode: 'play'}});});
  const state = {settings: {mode: 'play', server: 'play.example.org:36100'}, host: {}, play: {}};
  ui.setupSteps(state); ui.ageWords(0, Date.now());
  assert.equal(calls.length, 0);
  await api.post('/api/settings', {...state.settings, setup_done: true});
  assert.deepEqual(calls, ['/api/settings']);
});
test('setup_done has no form control but the settings draft carries it', () => {
  const root = path.join(__dirname, '../app/internal/webui/assets'), html = fs.readFileSync(path.join(root, 'index.html'), 'utf8'), js = fs.readFileSync(path.join(root, 'app.js'), 'utf8');
  assert.doesNotMatch(js, /settingFields = \{[^}]*setup_done/); assert.match(js, /out\.setup_done = !!state\.settings\.setup_done/); assert.match(js, /mode: b\.dataset\.role, \.\.\.\(state\.settings\.mode \? \{\} : \{setup_done: false\}\)/);
  for (const id of ['setup', 'setup-list', 'setup-count', 'setup-skip', 'play-age', 'play-next', 'play-rooms-note']) assert.match(html, new RegExp('id="' + id + '"'));
});
test('a local GET begun before an action cannot overwrite that action with an old role', async () => {
  const guard = ui.readGuard(), renders = []; let release;
  const old = new Promise(resolve => {release = resolve;}), generation = guard.capture();
  const read = old.then(value => {if (guard.current(generation)) renders.push(value);});
  guard.invalidate(); renders.push('new host role'); release('old play role'); await read;
  assert.deepEqual(renders, ['new host role']);
  const fresh = guard.capture(); assert.equal(guard.current(fresh), true);
});
test('HTML uses local assets, labeled forms and distinct role cards without executable content injection', () => {
  const root = path.join(__dirname, '../app/internal/webui/assets'), html = fs.readFileSync(path.join(root, 'index.html'), 'utf8'), js = fs.readFileSync(path.join(root, 'app.js'), 'utf8');
  const ids = [...html.matchAll(/\bid="([^"]+)"/g)].map(x => x[1]); assert.equal(new Set(ids).size, ids.length);
  for (const id of ['play-form', 'host-form', 'profile-create-form', 'catalog-form', 'friend-request-form', 'export-form', 'restore-form', 'settings-form', 'confirm-dialog']) assert.ok(ids.includes(id), id);
  for (const role of ['play', 'host', 'both']) assert.match(html, new RegExp('class="role-card" data-role="' + role + '"'));
  assert.match(html, /script defer src="\/app.js"/); assert.match(html, /href="\/app.css"/); assert.doesNotMatch(html, /<script[^>]*>[^<]+|\son\w+=|style=|(?:src|href)="https?:/i);
  assert.doesNotMatch(js, /\.innerHTML|\.outerHTML|insertAdjacentHTML|\beval\(|new Function|localStorage/);
  for (const input of ['export-passphrase', 'export-confirm', 'restore-passphrase']) assert.match(html, new RegExp('id="' + input + '" type="password"'));
  assert.equal(ui.clean('\x1b[31mname\u202espoof'), '[31mnamespoof');
});
