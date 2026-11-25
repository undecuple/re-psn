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
  for (const id of ['setup', 'setup-list', 'setup-count', 'setup-skip', 'play-age', 'play-next', 'play-rooms-note', 'play-copy', 'play-copy-text', 'play-copy-btn', 'play-copy-note', 'play-hints', 'play-hints-list', 'play-saved', 'play-saved-pick', 'play-saved-select', 'play-saved-note', 'play-saved-empty', 'play-saved-manage', 'servers-title']) assert.match(html, new RegExp('id="' + id + '"'));
});
test('roomsNote says what an empty or partial room list means', () => {
  const ok = {status: 'reachable', rooms: [], unlisted: 0};
  assert.equal(ui.roomsNote({status: 'unchecked'}), '');
  assert.equal(ui.roomsNote({status: 'unavailable', source: 'game_connection'}), '');
  assert.match(ui.roomsNote({...ok, source: 'game_connection', titled: false}), /reached, but no rooms are listed because no title ID or status URL was set/);
  assert.match(ui.roomsNote({...ok, source: 'game_connection', titled: false, status_fallback: true}), /status URL could not be read.*no title ID or status URL/);
  assert.match(ui.roomsNote({...ok, source: 'game_connection', titled: true}), /lists no rooms for this title ID/);
  assert.equal(ui.roomsNote({...ok, source: 'game_connection', titled: true, rooms: [{id: '1'}]}), '');
  assert.match(ui.roomsNote({...ok, source: 'status_page'}), /status page lists no public rooms/);
  assert.match(ui.roomsNote({...ok, source: 'status_page', unlisted: 1}), /counts 1 unlisted room on this server, without names/);
  assert.match(ui.roomsNote({...ok, source: 'status_page', unlisted: 4, rooms: [{id: '1'}]}), /counts 4 unlisted rooms on this server/);
  assert.doesNotMatch(ui.roomsNote({...ok, source: 'game_connection', titled: true}), /unlisted/);
  assert.match(ui.roomsNote({...ok, source: 'status_page', titled: true}), /no public rooms for this title ID/);
  assert.match(ui.roomsNote({...ok, source: 'status_page', titled: true, unlisted: 3, rooms: [{id: '1'}]}), /3 unlisted rooms on this server, for any title/);
  assert.match(ui.roomsNote({...ok, source: 'game_connection', titled: true, status_fallback: true}), /status URL could not be read.*no rooms for this title ID/);
});
test('failureHints picks "may" hints by failure kind and never claims a cause', () => {
  assert.deepEqual(ui.failureHints({status: 'reachable'}), []);
  assert.deepEqual(ui.failureHints(null), []);
  const of = p => ui.failureHints({status: 'unavailable', ...p});
  assert.match(of({kind: 'refused'}).join(' '), /port may be wrong.*may not be running/);
  assert.doesNotMatch(of({kind: 'refused'}).join(' '), /firewall|token/);
  assert.match(of({kind: 'timeout'}).join(' '), /firewall or router may be blocking/);
  assert.match(of({kind: 'status'}).join(' '), /status URL may be mistyped/);
  assert.match(of({kind: 'status'}).join(' '), /may need an access token/);
  assert.match(of({kind: 'timeout', used_status_url: true}).join(' '), /may need an access token/);
  assert.deepEqual(of({kind: 'surprise'}), of({kind: 'other'}));
  assert.deepEqual(of({}), of({kind: 'other'}));
  for (const kind of ['refused', 'timeout', 'status', 'other', '']) for (const h of of({kind, used_status_url: true})) assert.match(h, /\bmay\b/, h);
});
test('copyText reports copied or manual and never throws', async () => {
  const written = [];
  assert.equal(await ui.copyText('play.example.org:36100', {writeText: async v => {written.push(v);}}), 'copied');
  assert.deepEqual(written, ['play.example.org:36100']);
  assert.equal(await ui.copyText('play.example.org:36100', {writeText: async () => {throw new Error('blocked');}}), 'manual');
  assert.equal(await ui.copyText('play.example.org:36100', {writeText: () => {throw new Error('blocked');}}), 'manual');
  assert.equal(await ui.copyText('play.example.org:36100', undefined), 'manual');
  assert.equal(await ui.copyText('play.example.org:36100', {}), 'manual');
  assert.equal(await ui.copyText('', {writeText: async () => {throw new Error('must not be called');}}), 'manual');
});
test('the Play helpers and the copy decision make no request', async () => {
  const calls = [], real = globalThis.fetch;
  globalThis.fetch = async url => {calls.push(url); return json({});};
  try {
    ui.roomsNote({status: 'reachable', source: 'status_page', rooms: [], unlisted: 2}); ui.failureHints({status: 'unavailable', kind: 'timeout'});
    await ui.copyText('play.example.org:36100', {}); await ui.copyText('play.example.org:36100', {writeText: async () => {}});
  } finally {globalThis.fetch = real;}
  assert.deepEqual(calls, []);
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
test('every static id that app.js looks up exists in index.html', () => {
  const root = path.join(__dirname, '../app/internal/webui/assets'), html = fs.readFileSync(path.join(root, 'index.html'), 'utf8'), js = fs.readFileSync(path.join(root, 'app.js'), 'utf8');
  const ids = new Set([...html.matchAll(/\bid="([^"]+)"/g)].map(x => x[1]));
  // Matches $('id'), text('id', ...), show('id', ...), submit('id', ...) and tableRows('id', ...): a quoted literal followed by , or ).
  // A literal followed by anything else, such as $('page-' + page), is built at run time and is covered by the allow-list below.
  const lookup = /(?:\$|\b(?:text|show|submit|tableRows))\(\s*'([a-z][a-z0-9-]*)'\s*[,)]/g;
  const used = new Set([...js.matchAll(lookup)].map(x => x[1]));
  assert.ok(used.size > 50, 'the scan found only ' + used.size + ' ids; the pattern no longer matches the helpers');
  assert.deepEqual([...used].filter(id => !ids.has(id)), [], 'ids read by app.js but missing from index.html');
  // Ids app.js reads through a variable, so the scan above cannot see them. Each is listed with where it is built.
  const dynamic = [
    ...[...js.matchAll(/'(setting-[a-z-]+)'/g)].map(x => x[1]), // settingFields values, read with $(id) in fillSettings
    ...[...html.matchAll(/data-page="([a-z]+)"/g)].map(x => 'page-' + x[1]) // navigate() reads $('page-' + page)
  ];
  assert.ok(dynamic.length > 8);
  assert.deepEqual(dynamic.filter(id => !ids.has(id)), [], 'dynamically read ids missing from index.html');
  assert.match(js, /\$\('page-' \+ page\)/); assert.match(js, /const settingFields = \{/);
});
// The picker lives after app.js's browser-only early return, so these tests cut its source out and run it against a small fake DOM.
// Each cut is asserted to exist, so a rename fails loudly instead of leaving a test that checks nothing.
function pickerHarness(list, fields = {}) {
  const src = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  const start = src.indexOf('let pickerKey'), end = src.indexOf('async function checkServer');
  assert.ok(start > 0 && end > start, 'picker block not found');
  const change = src.match(/\$\('play-saved-select'\)\.addEventListener\('change', async \(\) => \{[\s\S]*?\n  \}\);/), input = src.match(/\$\('play-form'\)\.addEventListener\('input', event => \{[^\n]*\}\);/), manage = src.match(/\$\('play-saved-manage'\)\.addEventListener\('click', [^\n]*\);/);
  assert.ok(change && input && manage, 'picker listeners not found');
  const els = {}, handlers = {};
  const select = () => { let value = '', options = []; return {get value() {return value;}, set value(v) {value = options.some(o => o.value === v) ? v : '';}, get options() {return options;}, replaceChildren(f) {options = f.items; value = options.length ? options[0].value : '';}}; };
  const $ = id => els[id] || (els[id] = Object.assign(id === 'play-saved-select' ? select() : {value: '', hidden: false, textContent: '', focused: false, focus() {this.focused = true;}}, {addEventListener(type, fn) {handlers[id + ':' + type] = fn;}}));
  for (const [id, value] of Object.entries(fields)) $(id).value = value;
  $('play-saved-note');
  const confirms = [], requests = [], trap = new Proxy({}, {get: () => () => {requests.push('api'); throw new Error('request made');}});
  const doc = {createDocumentFragment: () => ({items: [], append(...n) {this.items.push(...n);}})};
  const text = (id, value) => { $(id).textContent = value; }, show = (id, visible) => { $(id).hidden = !visible; }, node = (tag, value) => ({tag, textContent: value, value: ''});
  const body = src.slice(start, end) + change[0] + input[0] + manage[0] + '\nreturn {renderPicker, syncPicked, entries: () => pickerEntries, dirty: () => playDirty};';
  const h = {answer: true}, state = {settings: {server: fields.__saved_server || '', status_url: ''}}, confirm = async (title) => { confirms.push(title); return h.answer; };
  const run = new Function('$', 'text', 'show', 'node', 'savedServers', 'document', 'api', 'fetch', 'state', 'confirm', 'let playDirty = false;\n' + body)($, text, show, node, () => list, doc, trap, () => {requests.push('fetch');}, state, confirm);
  return Object.assign(h, {els, handlers, requests, confirms, run, list});
}
const entry = (name, server, extra = {}) => ({name, server, status_url: '', ...extra});
test('picker options follow the saved list: a prompt first, one option per entry, local ones labelled', () => {
  const h = pickerHarness({entries: [entry('Friends', 'play.example.org:36100'), entry('Home', '192.0.2.2:36100', {local: true, status_url: 'https://relay.example.org/status'})], max: 64});
  h.run.renderPicker();
  const options = h.els['play-saved-select'].options;
  assert.deepEqual(options.map(o => [o.value, o.textContent]), [['', 'Choose a saved server'], ['0', 'Friends · play.example.org:36100'], ['1', 'Home · 192.0.2.2:36100 · local network address']]);
  assert.equal(h.els['play-saved-pick'].hidden, false); assert.equal(h.els['play-saved-empty'].hidden, true);
  assert.equal(h.els['play-saved-select'].value, '');
});
test('an empty saved list hides the picker and shows the empty note, with the prompt as the only option', () => {
  const h = pickerHarness({entries: [], max: 64});
  h.run.renderPicker();
  assert.equal(h.els['play-saved-pick'].hidden, true); assert.equal(h.els['play-saved-empty'].hidden, false);
  assert.deepEqual(h.els['play-saved-select'].options.map(o => o.value), ['']);
  const none = pickerHarness({}); none.run.renderPicker();
  assert.equal(none.els['play-saved-pick'].hidden, true); assert.equal(none.els['play-saved-empty'].hidden, false);
});
test('the picker is capped at the list maximum', () => {
  const h = pickerHarness({entries: [entry('a', 'a:1'), entry('b', 'b:1'), entry('c', 'c:1')], max: 2});
  h.run.renderPicker();
  assert.equal(h.els['play-saved-select'].options.length, 3);
});
test('the picker is not rebuilt for a check result, but is for a changed name, address, status URL or local flag', () => {
  const list = {entries: [entry('Friends', 'play.example.org:36100')], max: 64}, h = pickerHarness(list);
  h.run.renderPicker();
  const first = h.els['play-saved-select'].options;
  list.entries[0] = {...list.entries[0], status: 'reachable', checked_at: 5, rooms: 3}; h.run.renderPicker();
  assert.equal(h.els['play-saved-select'].options, first, 'a check result rebuilt the options');
  for (const change of [{name: 'Renamed'}, {server: 'other.example.org:36100'}, {status_url: 'https://relay.example.org/status'}, {local: true}]) {
    const before = h.els['play-saved-select'].options; list.entries[0] = {...list.entries[0], ...change}; h.run.renderPicker();
    assert.notEqual(h.els['play-saved-select'].options, before, JSON.stringify(change));
  }
});
test('choosing an entry fills the two fields, marks the form edited, and makes no request', async () => {
  const h = pickerHarness({entries: [entry('Friends', 'play.example.org:36100', {status_url: 'https://relay.example.org/status'}), entry('Plain', 'plain.example.org:36100')], max: 64}, {'play-server': 'old:1', 'play-status-url': 'https://old.example.org/status'});
  h.run.renderPicker();
  const select = h.els['play-saved-select'];
  select.value = '0'; await h.handlers['play-saved-select:change']();
  assert.equal(h.els['play-server'].value, 'play.example.org:36100'); assert.equal(h.els['play-status-url'].value, 'https://relay.example.org/status');
  assert.equal(h.run.dirty(), true); assert.match(h.els['play-saved-note'].textContent, /Filled in Friends with its status URL\. Nothing was contacted/);
  select.value = '1'; await h.handlers['play-saved-select:change']();
  assert.equal(h.els['play-status-url'].value, '', 'an entry without a status URL must clear the old one');
  assert.match(h.els['play-saved-note'].textContent, /cleared the status URL/);
  assert.deepEqual(h.requests, []);
});
test('choosing the prompt changes nothing and clears the note', async () => {
  const h = pickerHarness({entries: [entry('Friends', 'play.example.org:36100')], max: 64}, {'play-server': 'typed:1'});
  h.run.renderPicker(); h.els['play-saved-note'].textContent = 'stale';
  await h.handlers['play-saved-select:change']();
  assert.equal(h.els['play-server'].value, 'typed:1'); assert.equal(h.run.dirty(), false); assert.equal(h.els['play-saved-note'].textContent, ''); assert.deepEqual(h.requests, []);
});
test('choosing an entry asks first when typed text is neither saved nor in the settings, and keeps it on cancel', async () => {
  const h = pickerHarness({entries: [entry('Friends', 'play.example.org:36100')], max: 64}, {'play-server': 'typed:1', 'play-status-url': ''});
  h.run.renderPicker(); h.answer = false;
  h.els['play-saved-select'].value = '0'; await h.handlers['play-saved-select:change']();
  assert.equal(h.confirms.length, 1); assert.equal(h.els['play-server'].value, 'typed:1'); assert.equal(h.els['play-saved-select'].value, '');
  h.answer = true; h.els['play-saved-select'].value = '0'; await h.handlers['play-saved-select:change']();
  assert.equal(h.confirms.length, 2); assert.equal(h.els['play-server'].value, 'play.example.org:36100'); assert.deepEqual(h.requests, []);
});
test('the picker shows the matching entry, and the prompt once the fields are edited or no entry matches', () => {
  const h = pickerHarness({entries: [entry('A', 'a.example.org:1'), entry('B', 'b.example.org:1', {status_url: 'https://b.example.org/status'})], max: 64}, {'play-server': 'b.example.org:1', 'play-status-url': 'https://b.example.org/status'});
  h.run.renderPicker();
  assert.equal(h.els['play-saved-select'].value, '1');
  h.els['play-status-url'].value = ''; h.els['play-saved-note'].textContent = 'Filled in B'; h.run.syncPicked();
  assert.equal(h.els['play-saved-select'].value, '', 'the same address with a different status URL is a different target');
  assert.equal(h.els['play-saved-note'].textContent, '');
  h.els['play-server'].value = 'a.example.org:1'; h.run.syncPicked(); assert.equal(h.els['play-saved-select'].value, '0');
  h.els['play-server'].value = 'nobody:1'; h.run.syncPicked(); assert.equal(h.els['play-saved-select'].value, '');
});
test('a rebuild keeps the chosen entry by address even when its position moves, and drops it when removed', () => {
  const list = {entries: [entry('A', 'a.example.org:1'), entry('B', 'b.example.org:1')], max: 64}, h = pickerHarness(list, {'play-server': 'b.example.org:1'});
  h.run.renderPicker(); assert.equal(h.els['play-saved-select'].value, '1');
  list.entries = [entry('Z', 'z.example.org:1'), entry('A', 'a.example.org:1'), entry('B', 'b.example.org:1')]; h.run.renderPicker();
  assert.equal(h.els['play-saved-select'].value, '2');
  list.entries = [entry('A', 'a.example.org:1')]; h.run.renderPicker();
  assert.equal(h.els['play-saved-select'].value, '');
});
test('typing in the Play form resyncs the picker, but the picker own input event does not', () => {
  const h = pickerHarness({entries: [entry('A', 'a.example.org:1')], max: 64}, {'play-server': 'a.example.org:1'});
  h.run.renderPicker(); assert.equal(h.els['play-saved-select'].value, '0');
  h.els['play-server'].value = 'a.example.org:2'; h.handlers['play-form:input']({target: {id: 'play-server'}});
  assert.equal(h.els['play-saved-select'].value, ''); assert.equal(h.run.dirty(), true);
  h.els['play-server'].value = 'a.example.org:1'; h.run.syncPicked(); assert.equal(h.els['play-saved-select'].value, '0');
  h.els['play-server'].value = 'stale:9'; h.handlers['play-form:input']({target: {id: 'play-saved-select'}});
  assert.equal(h.els['play-saved-select'].value, '0', 'the picker input event fires before change and must not resync');
});
test('Manage saved servers moves focus to the heading and contacts nothing', () => {
  const h = pickerHarness({entries: [], max: 64});
  h.handlers['play-saved-manage:click']();
  assert.equal(h.els['servers-title'].focused, true); assert.equal(h.els['servers-title'].tabIndex, -1); assert.deepEqual(h.requests, []);
});
test('the picker is redrawn on every state read and resynced when the settings fields are filled', () => {
  const js = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  assert.match(js, /renderServers\(\); renderPicker\(\);/);
  assert.match(js, /text\('settings-dirty', [^\n]*\);\n    syncPicked\(\);/);
});
test('the picker markup sits in the Play form, starts empty and hidden-safe, and the heading it focuses is the saved-servers one', () => {
  const html = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/index.html'), 'utf8');
  assert.match(html, /<form id="play-form"[^>]*>\s*<div class="saved-pick" id="play-saved">/);
  assert.match(html, /<select id="play-saved-select"><option value="">Choose a saved server<\/option><\/select>/);
  assert.match(html, /id="play-saved-empty" hidden>/); assert.match(html, /<button type="button" id="play-saved-manage">/);
  assert.match(html, /<h2 id="servers-title">Saved servers<\/h2>/);
});
test('inviteKind words each kind plainly and falls back to the cautious one', () => {
  assert.match(ui.inviteKind('loopback').note, /Only this computer/);
  assert.match(ui.inviteKind('link-local').note, /Other computers cannot reach it/);
  assert.match(ui.inviteKind('private').note, /same network, or on the same VPN/);
  assert.equal(ui.inviteKind('other').label, 'May be reachable from outside');
  assert.doesNotMatch(ui.inviteKind('other').note, /public address|your address is/i);
  for (const unknown of ['', 'weird', undefined, 'constructor', '__proto__']) assert.equal(ui.inviteKind(unknown), ui.inviteKind('other'));
});
test('inviteLoopbackOnly trusts the server answer and otherwise reads the bind', () => {
  const run = address => ({host: {running: true, address}});
  assert.equal(ui.inviteLoopbackOnly(run('127.0.0.1:36100'), null), true);
  assert.equal(ui.inviteLoopbackOnly(run('[::1]:36100'), {failed: true}), true);
  assert.equal(ui.inviteLoopbackOnly(run('0.0.0.0:36100'), null), false);
  assert.equal(ui.inviteLoopbackOnly(run('192.0.2.2:36100'), null), false);
  assert.equal(ui.inviteLoopbackOnly(run('127.0.0.2:36100'), {loopback_only: true}), true);
  assert.equal(ui.inviteLoopbackOnly(run('127.0.0.1:36100'), {loopback_only: false}), false);
  assert.equal(ui.inviteLoopbackOnly({host: {running: false, address: '127.0.0.1:36100'}}, null), false);
});
test('inviteWanted asks for addresses only on the Host page with a running host', () => {
  const state = {settings: {mode: 'host'}, host: {running: true, address: '0.0.0.0:36100', started_at: 7}};
  assert.equal(ui.inviteWanted(state, 'host'), '0.0.0.0:36100|7');
  for (const page of ['overview', 'play', 'settings']) assert.equal(ui.inviteWanted(state, page), '');
  assert.equal(ui.inviteWanted({...state, host: {...state.host, running: false}}, 'host'), '');
  assert.equal(ui.inviteWanted({...state, settings: {mode: 'play'}}, 'host'), '');
  assert.equal(ui.inviteWanted({settings: {}, host: {running: true, address: 'x:1'}}, 'host'), '');
  assert.equal(ui.inviteWanted(null, 'host'), '');
  assert.notEqual(ui.inviteWanted({...state, host: {...state.host, started_at: 8}}, 'host'), ui.inviteWanted(state, 'host'));
});
test('the address list route is a known action, and the page never polls or fetches it from the state read', async () => {
  const calls = [];
  const api = ui.makeAPI(token, async (url, options) => {calls.push([url, options.method]); return json({addresses: []});});
  await api.post('/api/host/invite'); assert.deepEqual(calls, [['/api/host/invite', 'POST']]);
  const js = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  assert.equal([...js.matchAll(/api\.post\('\/api\/host\/invite'/g)].length, 1);
  assert.doesNotMatch(js, /setInterval\([^)]*[iI]nvite/);
  const read = js.slice(js.indexOf('async function readState'), js.indexOf('function friendAction'));
  assert.doesNotMatch(read, /[iI]nvite/);
});
test('the host invite markup has the ids the script uses, a status role for the warning, and no external address lookup', () => {
  const root = path.join(__dirname, '../app/internal/webui/assets'), html = fs.readFileSync(path.join(root, 'index.html'), 'utf8'), js = fs.readFileSync(path.join(root, 'app.js'), 'utf8');
  for (const id of ['host-invite', 'host-invite-bind', 'host-invite-warning', 'host-invite-list', 'host-invite-list-note', 'host-invite-copy-note', 'host-invite-refresh']) assert.match(html, new RegExp('id="' + id + '"'));
  assert.match(html, /id="host-invite-warning" role="status"/); assert.match(html, /id="host-invite-copy-note" role="status"/);
  assert.match(html, /id="host-invite" hidden/);
  assert.match(js, /Warning: only this computer can connect/);
  assert.doesNotMatch(js, /https?:\/\/(?!www\.w3\.org)/);
});

test('statusUptime says the time in words and not reported for anything odd', () => {
  assert.equal(ui.statusUptime(0), '0 seconds'); assert.equal(ui.statusUptime(1), '1 second'); assert.equal(ui.statusUptime(59), '59 seconds');
  assert.equal(ui.statusUptime(60), '1 minute'); assert.equal(ui.statusUptime(125), '2 minutes');
  assert.equal(ui.statusUptime(3600), '1 hour'); assert.equal(ui.statusUptime(3 * 3600 + 5 * 60), '3 hours 5 minutes');
  assert.equal(ui.statusUptime(2 * 86400 + 3600), '2 days 1 hour');
  for (const bad of [undefined, null, -1, NaN, Infinity, '5']) assert.equal(ui.statusUptime(bad), 'not reported');
});
test('statusRows shows missing fields as not reported and a reported zero as a number or no limit', () => {
  const rows = Object.fromEntries(ui.statusRows({clients: 0, rooms: 2, unlisted: 1, uptime_seconds: 90, limits: {max_rooms: 100, max_req_rate: 0}, refusals: {tcp_req_rate: 3}}));
  assert.equal(rows['Players connected'], '0'); assert.equal(rows['Public rooms'], '2'); assert.equal(rows['Running for (timed by this app)'], '1 minute');
  assert.equal(rows['Room limit'], '100'); assert.equal(rows['Requests per second limit'], 'no limit'); assert.equal(rows['Rooms per player limit'], 'not reported');
  assert.equal(rows['Requests refused (rate)'], '3'); assert.equal(rows['UDP packets dropped or refused'], 'not reported');
  const none = ui.statusRows({clients: null, limits: null, refusals: null});
  assert.ok(none.length >= 10); assert.equal(none[0][1], 'not reported'); assert.ok(none.every(r => r[1] === 'not reported'));
  assert.equal(ui.statusRows(null).length, none.length);
});
test('statusKey changes when the host stops or restarts', () => {
  const run = {host: {running: true, address: '127.0.0.1:36100', started_at: 5}};
  assert.equal(ui.statusKey(run), '127.0.0.1:36100|5');
  assert.notEqual(ui.statusKey({host: {...run.host, started_at: 6}}), ui.statusKey(run));
  assert.equal(ui.statusKey({host: {...run.host, running: false}}), ''); assert.equal(ui.statusKey(null), '');
});
test('host status is a known route, read only from the button, never polled or loaded with the state', async () => {
  const calls = [];
  const api = ui.makeAPI(token, async (url, options) => {calls.push([url, options.method]); return json({clients: 1});});
  await api.post('/api/host/status'); assert.deepEqual(calls, [['/api/host/status', 'POST']]);
  const js = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  assert.equal([...js.matchAll(/api\.post\('\/api\/host\/status'/g)].length, 1);
  assert.equal([...js.matchAll(/loadHostStatus\(/g)].length, 2); // its definition and the button handler
  assert.match(js, /\$\('host-status-refresh'\)\.addEventListener\('click'/);
  assert.doesNotMatch(js, /setInterval\([^)]*[sS]tatus/);
  const read = js.slice(js.indexOf('async function readState'), js.indexOf('function friendAction'));
  assert.doesNotMatch(read, /[hH]ostStatus/);
});
test('the host status markup has the ids the script uses, a status role for the note, and no external address', () => {
  const root = path.join(__dirname, '../app/internal/webui/assets'), html = fs.readFileSync(path.join(root, 'index.html'), 'utf8'), js = fs.readFileSync(path.join(root, 'app.js'), 'utf8');
  for (const id of ['host-status', 'host-status-title', 'host-status-note', 'host-status-list', 'host-status-refresh']) assert.match(html, new RegExp('id="' + id + '"'));
  assert.match(html, /id="host-status-note" role="status"/); assert.match(html, /id="host-status" hidden/); assert.match(html, />Host status</); assert.match(html, />Refresh status</);
  assert.match(js, /Read at /); assert.doesNotMatch(js, /https?:\/\/(?!www\.w3\.org)/);
});

test('the host status button is guarded, discards a read from an earlier run, and never shows a raw error', () => {
  const js = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  const handler = js.slice(js.indexOf("$('host-status-refresh').addEventListener"), js.indexOf("$('host-invite-list').addEventListener"));
  assert.match(handler, /hostStatusBusy/); assert.match(handler, /!authenticated/); assert.match(handler, /stopped/); assert.match(handler, /hosting\(state\.settings\.mode\)/);
  const load = js.slice(js.indexOf('async function loadHostStatus'), js.indexOf('function renderHostStatus'));
  assert.match(load, /hostStatusKey === key && !stopped/); assert.doesNotMatch(load, /error\.message/);
  const render = js.slice(js.indexOf('function renderHostStatus'), js.indexOf('const NEXT_STEP'));
  assert.match(render, /show\('host-status', !!key\)/); assert.match(render, /key !== hostStatusKey/); assert.match(render, /setAttribute\('aria-disabled', String\(hostStatusBusy\)\)/);
  assert.match(render, /not a live view/); assert.match(render, /Nothing has been read yet/);
  assert.match(js, /renderInvite\(\); renderHostStatus\(\);/);
});

test('lanPortFrom reads a typed port and gives 0 for anything the app should decide itself', () => {
  assert.equal(ui.lanPortFrom('play.example.org:36100'), 36100); assert.equal(ui.lanPortFrom(' 192.0.2.7:4242 '), 4242);
  assert.equal(ui.lanPortFrom('[::1]:9000'), 9000); assert.equal(ui.lanPortFrom(':7'), 7);
  for (const none of ['', 'host', 'host:', 'host:0', 'host:65536', 'host:123456', 'host:12a', 'a:b:80', undefined, null]) assert.equal(ui.lanPortFrom(none), 0, String(none));
});
test('lanRows is capped, cleaned and says what each server called itself', () => {
  const rows = ui.lanRows([{address: '192.0.2.7:36100', name: 'Friend box', players: 3, version: '0.9.0'}, {address: '192.0.2.8:36100', name: '', players: 1, version: ''}, {address: '192.0.2.9:36100', name: 'ev\u0000il‮', players: 'x'}, {address: ''}, null, {name: 'no address'}]);
  assert.deepEqual(rows.map(r => r.address), ['192.0.2.7:36100', '192.0.2.8:36100', '192.0.2.9:36100']);
  assert.equal(rows[0].label, 'Friend box · 3 players · version 0.9.0');
  assert.equal(rows[1].label, 'unnamed server · 1 player');
  assert.equal(rows[2].label, 'evil');
  const many = Array.from({length: 40}, (_, i) => ({address: '192.0.2.' + i + ':1', name: 'n', players: 0, version: ''}));
  assert.equal(ui.lanRows(many).length, 16);
  for (const bad of [undefined, null, 'x', {}, 5]) assert.deepEqual(ui.lanRows(bad), []);
});
test('lanNote says nothing before a search and does not promise a server after an empty one', () => {
  assert.equal(ui.lanNote(false, 0), ''); assert.equal(ui.lanNote(false, 3), '');
  assert.match(ui.lanNote(true, 0), /No server answered/); assert.match(ui.lanNote(true, 0), /does not cross a router/);
  assert.match(ui.lanNote(true, 1), /^1 server answered/); assert.match(ui.lanNote(true, 2), /^2 servers answered/);
  assert.match(ui.lanNote(true, 2), /nothing is saved until you press Save/); assert.match(ui.lanNote(true, 2), /not verified/);
});
test('the LAN search is a known POST route and nothing else under that prefix is reachable', async () => {
  const calls = [], api = ui.makeAPI(token, async (url, options) => {calls.push([url, options.method, options.body]); return json({settings: {mode: 'play'}, lan: []});});
  await api.post('/api/lan/find', {}); await api.post('/api/lan/find', {port: 4242});
  assert.deepEqual(calls, [['/api/lan/find', 'POST', '{}'], ['/api/lan/find', 'POST', '{"port":4242}']]);
  for (const other of ['/api/lan', '/api/lan/', '/api/lan/find/', '/api/lan/find?x=1', '/api/lan/other']) await assert.rejects(api.post(other, {}), /Unsupported/);
  assert.equal(calls.length, 2);
});
test('the LAN search is sent only after the confirm sentence, never polled and never loaded with the state', () => {
  const js = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  assert.equal([...js.matchAll(/api\.post\('\/api\/lan\/find'/g)].length, 1);
  assert.equal([...js.matchAll(/findOnLan\b/g)].length, 2); // its definition and the Send handler
  const find = js.slice(js.indexOf("$('lan-find').addEventListener"), js.indexOf("$('lan-cancel').addEventListener"));
  assert.match(find, /lanConfirming = true/); assert.doesNotMatch(find, /api\.|action\(/);
  assert.match(js, /\$\('lan-go'\)\.addEventListener\('click', \(\) => \{ if \(lanSearching\) return; if \(busy\) \{[^}]*\} action\(findOnLan/);
  assert.match(js, /\$\('lan-confirm'\)\.addEventListener\('keydown'[^\n]*Escape/);
  assert.doesNotMatch(js, /setInterval\([^)]*[lL]an/);
  const read = js.slice(js.indexOf('async function readState'), js.indexOf('function friendAction'));
  assert.doesNotMatch(read, /[lL]an(Find|Asked)|findOnLan/);
  assert.doesNotMatch(js, /window\.confirm|[^.\w]confirm\(\s*['"]Find/);
});
test('the Use button only fills the server box; nothing is saved or sent', () => {
  const js = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  const use = js.slice(js.indexOf("$('lan-list').addEventListener"), js.indexOf("$('host-invite-refresh').addEventListener"));
  assert.match(use, /\$\('play-server'\)\.value = button\.dataset\.lanUse/); assert.match(use, /playDirty = true/);
  assert.doesNotMatch(use, /api\.|action\(|saveSettings|fetch\(/);
  assert.match(use, /Nothing was saved or contacted/);
});
test('a settings save or using a saved server forgets that a search was made', () => {
  const js = fs.readFileSync(path.join(__dirname, '../app/internal/webui/assets/app.js'), 'utf8');
  const save = js.slice(js.indexOf('async function saveSettings'), js.indexOf('// What the last check found'));
  assert.match(save, /api\.post\('\/api\/settings'[^\n]*lanAsked = false/);
  const use = js.slice(js.indexOf('async function useServer'), js.indexOf('async function removeServer'));
  assert.match(use, /lanAsked = false/);
});
test('the LAN panel markup has the ids the script uses, a status role, a hidden confirm and no external address', () => {
  const root = path.join(__dirname, '../app/internal/webui/assets'), html = fs.readFileSync(path.join(root, 'index.html'), 'utf8'), js = fs.readFileSync(path.join(root, 'app.js'), 'utf8');
  for (const id of ['lan-panel', 'lan-title', 'lan-find', 'lan-confirm', 'lan-go', 'lan-cancel', 'lan-status', 'lan-list']) assert.match(html, new RegExp('id="' + id + '"'));
  assert.match(html, /id="lan-status" role="status" aria-live="polite"/); assert.match(html, /id="lan-confirm" hidden/);
  assert.match(html, />Find on my network \(broadcasts to this subnet\)</);
  assert.match(html, /id="lan-find" data-mutates/); assert.match(html, /id="lan-go" data-mutates/);
  // The panel sits on the Play page, ahead of the saved servers.
  const play = html.indexOf('id="page-play"'), panel = html.indexOf('id="lan-panel"'), saved = html.indexOf('id="servers-panel"');
  assert.ok(play > 0 && play < panel && panel < saved);
  assert.doesNotMatch(js, /https?:\/\/(?!www\.w3\.org)/);
});
test('the LAN search handler does not fall into another route', () => {
  const root = path.join(__dirname, '../app/internal/webui'), go = fs.readFileSync(path.join(root, 'actions.go'), 'utf8');
  const start = go.indexOf('case "/api/lan/find":'), next = go.indexOf('case "/api/shutdown":');
  assert.ok(start > 0 && next > start, 'the shutdown case must follow as its own case');
  assert.doesNotMatch(go.slice(start, next), /host\.Stop|s\.cancel\(\)/);
  assert.equal([...go.matchAll(/case "\/api\/shutdown":/g)].length, 1);
});
