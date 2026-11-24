/* Private local UI. Backend authority stays in Go; no external resources. */
(() => {
  'use strict';
  const MAX_ARCHIVE = 4 * 1024 * 1024 + 52;
  const MAX_JSON = 8 * 1024 * 1024;
  // Check all runs 4 servers at a time with 5 s each; 64 unreachable ones take 80 s.
  const CHECK_ALL_MS = 100000;
  const SESSION_KEY = 're-psn-ui-token-v1';
  const ROUTES = new Set(['/api/settings', '/api/play', '/api/host/start', '/api/host/stop', '/api/profile/create', '/api/catalog', '/api/grant', '/api/revoke', '/api/friends', '/api/profile/export', '/api/profile/restore', '/api/shutdown', '/api/servers/add', '/api/servers/remove', '/api/servers/check', '/api/servers/check-all', '/api/servers/use', '/api/servers/export', '/api/servers/preview', '/api/servers/import']);
  const personal = mode => mode === 'play' || mode === 'both';
  const hosting = mode => mode === 'host' || mode === 'both';
  function readGuard() { let generation = 0; return {capture: () => generation, invalidate: () => {generation++;}, current: value => value === generation}; }
  const clean = value => String(value == null ? '' : value).replace(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/g, '').slice(0, 8192);
  function fragmentToken(hash) {
    const match = /^#token=([a-f0-9]{64})$/.exec(hash || '');
    if (!match) throw new Error('Open the private launch link from the running app.');
    return match[1];
  }
  function storageHandle(storage) { try { return typeof storage === 'function' ? storage() : storage; } catch { return null; } }
  function takeToken(location, history, storage) {
    const fragment = location.hash;
    // Strip secrets before reading storage, rendering or making any request.
    history.replaceState(null, '', location.pathname);
    storage = storageHandle(storage);
    if (fragment) {
      let token;
      try { token = fragmentToken(fragment); }
      catch (error) { try { storage.removeItem(SESSION_KEY); } catch {} throw error; }
      // The URI carries only a one-use bootstrap, never the lasting API key.
      try { storage.removeItem(SESSION_KEY); } catch {}
      return {bootstrap: token, token: ''};
    }
    let saved;
    try { saved = storage.getItem(SESSION_KEY); } catch {}
    if (!/^[a-f0-9]{64}$/.test(saved || '')) throw new Error('This private app session is unavailable. Relaunch the app to open it.');
    return {bootstrap: '', token: saved};
  }
  async function openSession(capture, storage, fetcher) {
    if (!capture.bootstrap) return capture.token;
    const controller = new AbortController(), deadline = setTimeout(() => controller.abort(), 10000);
    try {
      const response = await fetcher('/api/open', {method: 'POST', headers: {Authorization: 'Bearer ' + capture.bootstrap, 'Content-Type': 'application/json'}, body: '{}', cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer', signal: controller.signal});
      const bytes = await responseBytes(response, 4096);
      if (!response.ok) throw new Error('This private launch link expired or was already used. Relaunch the app to open it.');
      const result = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(bytes));
      if (!result || !/^[a-f0-9]{64}$/.test(result.token || '')) throw new Error('Invalid private session response.');
      try { storageHandle(storage).setItem(SESSION_KEY, result.token); } catch {} // Current tab can work without storage.
      return result.token;
    } finally { clearTimeout(deadline); }
  }
  function catalogBody(text) {
    if (new TextEncoder().encode(text).length > 4 * 1024 * 1024) throw new Error('The catalog exceeds 4 MiB.');
    const preview = JSON.parse(text);
    if (!preview || typeof preview !== 'object' || Array.isArray(preview)) throw new Error('Choose a catalog JSON object.');
    // Keep the original text. The backend must see duplicate/unknown fields.
    return {catalog: text};
  }
  function base64(bytes) {
    if (bytes.length > MAX_ARCHIVE) throw new Error('The backup exceeds its size limit.');
    let binary = '';
    for (let i = 0; i < bytes.length; i += 32768) binary += String.fromCharCode(...bytes.subarray(i, i + 32768));
    return btoa(binary);
  }
  function clientAddress(address) {
    if (address.startsWith('0.0.0.0:')) return '127.0.0.1:' + address.slice(8);
    if (address.startsWith('[::]:')) return '[::1]:' + address.slice(5);
    return address;
  }
  function localBind(address) { return /^(127\.0\.0\.1|localhost|\[::1\]):\d+$/.test(address); }
  function selectedRelay(state) {
    const relays = (state.profile && state.profile.relays) || [];
    const origin = (state.settings.social_origin || '').replace(/\/$/, '');
    return origin ? relays.find(r => r.origin.replace(/\/$/, '') === origin) : relays[relays.length - 1];
  }
  // How long ago a check finished, in plain words. Pure so it can be tested; render() feeds it the clock.
  function ageWords(checkedAt, now) {
    if (!checkedAt) return 'not checked';
    const seconds = Math.max(0, Math.floor((now - checkedAt) / 1000));
    if (seconds < 60) return seconds + ' s ago';
    if (seconds < 3600) return Math.floor(seconds / 60) + ' min ago';
    return 'over an hour ago';
  }
  // The first-run checklist rows, computed from state only. Nothing here contacts anything: `page` is where the button goes.
  function setupSteps(state) {
    const s = state.settings || {}, host = state.host || {}, play = state.play || {}, steps = [];
    if (personal(s.mode)) {
      const hasServer = !!(s.server || s.status_url);
      steps.push({id: 'server', title: 'Server', done: hasServer, detail: hasServer ? (s.server || s.status_url) : 'Choose a game server or a status URL on the Play page.', page: 'play', button: 'Open Play page'});
      steps.push({id: 'name', title: 'Your name', done: !!s.name, detail: s.name ? '"' + s.name + '". Not checked, not protected by a password.' : 'Optional. Set an online ID in Settings. It is not checked and has no password.', page: 'settings', button: 'Open Settings'});
      steps.push({id: 'check', title: 'Check the server', done: !!play.checked_at, detail: 'Opens a short connection and asks who is there. It does not start a game.', page: 'play', button: 'Open Play page'});
    }
    if (hosting(s.mode)) {
      const bind = host.address || s.host_address || '127.0.0.1:36100', local = localBind(bind);
      steps.push({id: 'listen', title: 'Listen address', done: true, detail: bind + (local ? '. Friends cannot reach it.' : '.'), page: 'host', button: 'Open Host page'});
      steps.push({id: 'running', title: 'Host running', done: !!host.running, detail: host.running ? 'Running on ' + host.address + '.' : 'Not running. Start it on the Host page.', page: 'host', button: 'Open Host page'});
      const shared = !!host.running && !!host.address && !localBind(host.address);
      steps.push({id: 'share', title: 'Address to give friends', done: shared, detail: shared ? 'Friends on your network type ' + host.address + '.' : 'Listen on a non-loopback address to let friends connect.', page: 'host', button: 'Open Host page'});
    }
    return steps;
  }
  async function responseBytes(response, limit) {
    const declared = Number(response.headers.get('Content-Length'));
    if (declared > limit) throw new Error('The response exceeds its size limit.');
    const reader = response.body && response.body.getReader();
    if (!reader) { const b = new Uint8Array(await response.arrayBuffer()); if (b.length > limit) throw new Error('The response exceeds its size limit.'); return b; }
    const chunks = []; let length = 0;
    try {
      for (;;) { const part = await reader.read(); if (part.done) break; length += part.value.length; if (length > limit) throw new Error('The response exceeds its size limit.'); chunks.push(part.value); }
    } catch (error) { await reader.cancel().catch(() => {}); throw error; }
    finally { reader.releaseLock(); }
    const bytes = new Uint8Array(length); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
    return bytes;
  }
  function makeAPI(token, fetcher) {
    async function request(path, body, binary = false) {
      if (path !== '/api/state' && !ROUTES.has(path) || path === '/api/state' && body !== undefined) throw new Error('Unsupported local action.');
      const controller = new AbortController();
      const deadline = setTimeout(() => controller.abort(), path === '/api/servers/check-all' ? CHECK_ALL_MS : 30000);
      try {
        const headers = {Authorization: 'Bearer ' + token};
        const options = {method: body === undefined ? 'GET' : 'POST', headers, cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer', signal: controller.signal};
        if (body !== undefined) { headers['Content-Type'] = 'application/json'; options.body = JSON.stringify(body); }
        const response = await fetcher(path, options);
        if (binary && response.ok && (response.headers.get('Content-Type') || '').split(';')[0] !== 'application/octet-stream') throw new Error('The app did not return an encrypted backup.');
        const bytes = await responseBytes(response, binary && response.ok ? MAX_ARCHIVE : MAX_JSON);
        if (!response.ok) {
          let message = 'Local request failed (HTTP ' + response.status + ').';
          try { const problem = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(bytes)); if (typeof problem.error === 'string') message = clean(problem.error).split(token).join('[redacted]'); } catch {}
          const error = new Error(response.status === 401 ? 'Your private app session expired. Relaunch the app to open a new session.' : message);
          error.status = response.status; throw error;
        }
        if (binary && (bytes.length < 52 || new TextDecoder().decode(bytes.subarray(0, 8)) !== 'RPSNBAK1')) throw new Error('The app did not return a supported encrypted backup.');
        return binary ? bytes : JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(bytes));
      } catch (error) { if (controller.signal.aborted) throw new Error('The local action timed out. Refresh to check its outcome before retrying.'); throw error; }
      finally { clearTimeout(deadline); }
    }
    return {state: () => request('/api/state'), post: (path, body = {}) => request(path, body), export: passphrase => request('/api/profile/export', {passphrase}, true)};
  }
  const exported = {MAX_ARCHIVE, SESSION_KEY, personal, hosting, ageWords, setupSteps, roomsNote, failureHints, copyText, readGuard, clean, fragmentToken, takeToken, openSession, catalogBody, base64, clientAddress, localBind, selectedRelay, responseBytes, makeAPI};
  if (typeof module !== 'undefined' && module.exports) module.exports = exported;
  if (typeof document === 'undefined') return;

  const $ = id => document.getElementById(id);
  // Write only when the text changes, so a poll that finds nothing new leaves selections and live regions alone.
  const text = (id, value) => { const target = $(id), next = clean(value); if (target.textContent !== next) target.textContent = next; };
  const show = (id, visible) => { $(id).hidden = !visible; };
  const stamp = ms => ms ? new Date(ms).toLocaleString() : 'Not checked';
  const node = (tag, value, className) => { const n = document.createElement(tag); if (value !== undefined) n.textContent = clean(value); if (className) n.className = className; return n; };
  let state, api, currentPage = 'overview', busy = false, reading = false, authenticated = false, stopped = false;
  let settingsDirty = false, playDirty = false, hostDirty = false, rawCatalog = '', catalogSequence = 0;
  let friendsKey = '', trophiesKey = '', playKey = '', serversKey = '', setupKey = '', setupLeft = false;
  const generations = readGuard();
  function message(value, error = false) { const id = error ? 'error' : 'notice'; text(id, value); show(id, !!value); if (!error) show('error', false); }
  function fail(error) {
    if (error.status === 401) { authenticated = false; try { sessionStorage.removeItem(SESSION_KEY); } catch {} }
    message(error.message || 'The action could not finish. Refresh to check its outcome.', true);
    if (error.status === 401) text('bridge-state', 'Private session unavailable');
  }
  function controls() {
    const blocked = busy || !authenticated || stopped || !state;
    for (const button of document.querySelectorAll('[data-mutates]')) button.disabled = blocked;
    for (const button of document.querySelectorAll('form button[type="submit"]')) button.disabled = blocked;
    $('refresh-state').disabled = busy || reading || !authenticated || stopped;
    $('content').setAttribute('aria-busy', String(busy));
    if (!state) return;
    const profile = !!state.profile, friendReady = profile && !!state.settings.social_origin && personal(state.settings.mode);
    $('grant-start').disabled = blocked || !profile || !state.profile.titles || !state.profile.titles.length;
    $('grant-revoke').disabled = blocked || !state.grant.active;
    $('catalog-form').querySelector('button').disabled = blocked || !profile || !rawCatalog;
    $('friends-refresh').disabled = blocked || !friendReady;
    $('friend-register').disabled = blocked || !friendReady;
    $('friend-request-form').querySelector('button').disabled = blocked || !friendReady;
    for (const button of $('friend-groups').querySelectorAll('button')) button.disabled = blocked || !friendReady;
    $('export-form').querySelector('button').disabled = blocked || !profile;
    $('restore-form').querySelector('button').disabled = blocked || profile;
    $('host-start').disabled = blocked || state.host.running || !state.host.available;
    $('host-stop').disabled = blocked || !state.host.running;
    $('use-host').disabled = blocked || state.settings.mode !== 'both' || !state.host.running;
  }
  async function action(fn, success) {
    if (busy || !authenticated || stopped || !state) return;
    generations.invalidate(); // A later action outranks any outstanding local GET.
    busy = true; controls(); show('error', false); show('notice', false);
    try { const next = await fn(); if (next && next.settings) render(next); if (success && next != null) message(success); }
    catch (error) { fail(error); }
    finally { busy = false; controls(); }
  }
  function confirm(title, description, accept = 'Continue') {
    text('confirm-title', title); text('confirm-description', description); text('confirm-accept', accept);
    const dialog = $('confirm-dialog'); dialog.returnValue = 'cancel';
    return new Promise(resolve => {
      // The buttons close the dialog themselves, so the answer never depends on form submission (the page's CSP has form-action 'none').
      const buttons = [...dialog.querySelectorAll('button[value]')], answer = event => { event.preventDefault(); dialog.close(event.currentTarget.value); };
      for (const button of buttons) button.addEventListener('click', answer);
      dialog.addEventListener('close', () => { for (const button of buttons) button.removeEventListener('click', answer); resolve(dialog.returnValue === 'accept'); }, {once: true});
      dialog.showModal();
    });
  }
  function navigate(page, focus = true) {
    if (!state) return;
    if (['play', 'trophies', 'friends', 'profile'].includes(page) && !personal(state.settings.mode)) page = 'overview';
    if (page === 'host' && !hosting(state.settings.mode)) page = 'overview';
    currentPage = page;
    for (const section of document.querySelectorAll('.page')) section.hidden = section.id !== 'page-' + page;
    for (const button of document.querySelectorAll('.navigation [data-page]')) { if (button.dataset.page === page) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current'); }
    if (focus) { const title = $('page-' + page).querySelector('h1'); title.tabIndex = -1; title.focus(); }
  }
  const settingFields = {mode: 'setting-mode', theme: 'setting-theme', name: 'setting-name', server: 'setting-server', status_url: 'setting-status-url', social_origin: 'setting-social-origin', social_ca_file: 'setting-social-ca', host_address: 'setting-host-address', host_relay: 'setting-host-relay'};
  function fillSettings(force = false) {
    if (!settingsDirty || force) { for (const [key, id] of Object.entries(settingFields)) { if (key === 'host_relay') $(id).checked = !!state.settings[key]; else $(id).value = state.settings[key] || (key === 'mode' ? 'play' : key === 'theme' ? 'dark' : ''); } }
    if (!playDirty || force) { $('play-server').value = state.settings.server || ''; $('play-status-url').value = state.settings.status_url || ''; }
    if (!hostDirty || force) { $('host-bind').value = state.settings.host_address || '127.0.0.1:36100'; $('host-relay').checked = !!state.settings.host_relay; }
    text('settings-dirty', settingsDirty ? 'Unsaved edits' : 'Saved');
  }
  // setup_done has no form control, so it is carried over here; otherwise saving Settings would reset it.
  function draftSettings() { const out = {}; for (const [key, id] of Object.entries(settingFields)) out[key] = key === 'host_relay' ? $(id).checked : $(id).value.trim(); out.setup_done = !!state.settings.setup_done; return out; }
  async function saveSettings(next, discard = false) {
    let stop = false;
    if (next.mode === 'play' && state.host.running) { if (!await confirm('Stop your owned host?', 'Switching to Play will stop the server this app is hosting. Your separate Play target and local profile are kept.', 'Stop host & switch')) return null; stop = true; }
    const result = await api.post('/api/settings', {...next, stop_host: stop});
    if (discard) settingsDirty = playDirty = hostDirty = false;
    return result;
  }
  // What the last check found, in plain words, so an empty list is not mistaken for an empty server. Pure: reads the
  // play snapshot only. Returns '' when there is nothing to add.
  function roomsNote(p) {
    if (!p || p.status !== 'reachable') return '';
    const lines = [];
    if (p.source === 'status_page') {
      if (!(p.rooms || []).length) lines.push(p.titled ? 'The status page lists no public rooms for this title ID.' : 'The status page lists no public rooms.');
      if (p.unlisted > 0) lines.push('The status page counts ' + p.unlisted + (p.unlisted === 1 ? ' unlisted room' : ' unlisted rooms') + ' on this server' + (p.titled ? ', for any title' : '') + ', without names.');
    } else if (!p.titled) {
      lines.push((p.status_fallback ? 'The status URL could not be read, so only the game connection was used. ' : '') + 'The server was reached, but no rooms are listed because no title ID or status URL was set. Set one and check again.');
    } else if (!(p.rooms || []).length) {
      lines.push((p.status_fallback ? 'The status URL could not be read, so only the game connection was used. ' : '') + 'The server was reached and lists no rooms for this title ID.');
    } else if (p.status_fallback) {
      lines.push('The status URL could not be read, so these rooms come from the game connection.');
    }
    return lines.join(' ');
  }
  // Things that may explain a failed check, chosen by the failure kind the backend reports. Wording says "may": the app
  // cannot see why a connection failed, so this is a list of places to look, not a diagnosis.
  function failureHints(p) {
    if (!p || p.status !== 'unavailable') return [];
    const hints = [], kind = ['refused', 'timeout', 'status'].includes(p.kind) ? p.kind : 'other';
    if (kind === 'refused') hints.push('The port may be wrong.', 'The server may not be running.');
    else if (kind === 'timeout') hints.push('A firewall or router may be blocking the port, or not forwarding it to the server.', 'The address may be wrong, or the computer may be off or on another network.');
    else if (kind === 'status') hints.push('The status URL may be mistyped or may not point at a status page.', 'The server may not be running.');
    else hints.push('The port may be wrong, or the address may not be a re-psn server.', 'The server may not be running.');
    if (p.used_status_url || kind === 'status') hints.push('The status page may need an access token. A token file can be named in the settings file; see the app README.');
    return hints;
  }
  // Copies text with the clipboard object it is given (the browser's, or a stand-in in tests). Browsers and webviews may
  // refuse, so the answer is 'copied' or 'manual'; on 'manual' the caller selects the text and asks the person to copy it.
  async function copyText(value, clipboard) {
    if (!value) return 'manual';
    try {
      if (clipboard && typeof clipboard.writeText === 'function') { await clipboard.writeText(value); return 'copied'; }
    } catch {}
    return 'manual';
  }
  function tableRows(id, rows, count, empty, cells) {
    const target = $(id), fragment = document.createDocumentFragment();
    for (const row of rows || []) { const tr = node('tr'); for (const value of cells(row)) { const td = node('td'); if (value instanceof Node) td.append(value); else td.textContent = clean(value); tr.append(td); } fragment.append(tr); }
    if (!(rows || []).length) { const tr = node('tr'), td = node('td', empty, 'empty'); td.colSpan = count; tr.append(td); fragment.append(tr); }
    target.replaceChildren(fragment);
  }
  function renderPlay() {
    const p = state.play, key = JSON.stringify(p); if (key === playKey) return; playKey = key;
    const label = p.status === 'reachable' ? 'Reachable' : p.status === 'unavailable' ? 'Unavailable' : 'Not checked';
    text('play-badge', label); text('overview-play-tag', label);
    text('overview-play-note', p.checked_at ? 'Checked ' + stamp(p.checked_at) : 'Check explicitly when you are ready.');
    text('play-check-time', p.checked_at ? 'Checked ' + stamp(p.checked_at) + (p.round_trip_ms ? ' · ' + p.round_trip_ms + ' ms' : '') : 'Nothing contacted yet.');
    text('play-message', p.message || 'This is a snapshot check. Your game connects directly.');
    tableRows('rooms-body', p.rooms, 4, 'No room snapshot. Check a server to load its public rooms.', r => [r.id, r.title, r.current + '/' + r.max, (r.members || []).join(', ')]);
    const hints = failureHints(p), list = $('play-hints-list'); list.replaceChildren(...hints.map(h => node('li', h))); show('play-hints', hints.length > 0);
    const target = $('play-copy-text'); if (target.value !== clean(p.address)) target.value = clean(p.address); text('play-copy-note', ''); show('play-copy', !!p.address);
    tableRows('boards-body', p.boards, 4, 'No leaderboard snapshot available.', b => [b.title + ' / ' + b.id, b.entries, b.leader || '—', b.best || '—']);
  }
  const NEXT_STEP = {
    unchecked: 'Next: save a server and press Save & check server. Nothing is contacted until you do.',
    reachable: 'Next: put this address in your game\'s own network setting. This app does not do that. A check is not a game session.',
    unavailable: 'Next: check the address and that the server is running, then check again.'
  };
  // Runs on every render, outside renderPlay's early return, so the age moves with each poll. There is no timer.
  function renderAge() {
    const p = state.play, s = state.settings, status = NEXT_STEP[p.status] ? p.status : 'unchecked';
    text('play-age', p.checked_at ? 'Checked ' + ageWords(p.checked_at, Date.now()) : 'Not checked yet');
    text('play-next', NEXT_STEP[status]);
    const noRooms = !s.status_url && !$('play-title-id').value.trim();
    const note = p.checked_at ? roomsNote(p) : noRooms ? 'Set a title ID or status URL to list rooms.' : '';
    text('play-rooms-note', note); show('play-rooms-note', !!note);
  }
  function renderSetup() {
    const showing = !!state.settings.mode && !state.settings.setup_done && !setupLeft;
    show('setup', showing); show('workspace', !!state.settings.mode && !showing);
    if (!showing) return;
    const steps = setupSteps(state), key = JSON.stringify(steps); if (key === setupKey) return; setupKey = key;
    const done = steps.filter(s => s.done).length, rows = document.createDocumentFragment();
    text('setup-count', done + ' of ' + steps.length + ' done'); text('setup-skip', done === steps.length ? 'Finish setup' : 'Skip setup');
    for (const [i, step] of steps.entries()) {
      const row = node('li', undefined, 'setup-step'), body = node('div'), go = node('button', step.button);
      body.append(node('strong', (step.done ? 'Done: ' : 'To do: ') + (i + 1) + '. ' + step.title), node('span', step.detail, 'muted'));
      go.type = 'button'; go.dataset.setupPage = step.page; go.addEventListener('click', () => { setupLeft = true; render(state); navigate(step.page); });
      row.append(body, go); rows.append(row);
    }
    $('setup-list').replaceChildren(rows);
  }
  const savedServers = () => state.servers || {entries: [], error: '', max: 64};
  const ref = e => ({server: e.server, status_url: e.status_url || ''});
  function serverCheck(c) {
    if (c.status === 'reachable') return 'Reachable · ' + stamp(c.checked_at) + (c.round_trip_ms ? ' · ' + Math.round(c.round_trip_ms) + ' ms' : '');
    if (c.status === 'unavailable') return 'Unavailable · ' + stamp(c.checked_at);
    return 'Not checked';
  }
  function serverButton(label, entry, run) { const b = node('button', label); b.type = 'button'; b.dataset.mutates = ''; b.addEventListener('click', () => action(() => run(entry), null)); return b; }
  function renderServers() {
    const list = savedServers(), key = JSON.stringify(list); if (key === serversKey) return; serversKey = key;
    text('servers-count', list.entries.length + ' of ' + list.max); text('servers-error', list.error); show('servers-error', !!list.error);
    tableRows('servers-body', list.entries, 5, 'No saved servers yet. Add one above.', e => {
      const name = node('span', e.name), address = node('span', e.server); if (e.status_url) { address.append(node('br'), node('span', e.status_url, 'muted')); }
      if (e.local) { name.append(' ', node('span', 'local network address', 'tag')); }
      const buttons = node('span', undefined, 'actions');
      buttons.append(serverButton('Check', e, checkServer), serverButton('Use', e, useServer), serverButton('Remove', e, removeServer));
      return [name, address, serverCheck(e.check), e.check.status === 'reachable' ? String(e.check.rooms) : '—', buttons];
    });
    controls();
  }
  async function checkServer(e) { const next = await api.post('/api/servers/check', ref(e)); message('Checked ' + e.name + '.'); return next; }
  async function useServer(e) {
    if ((playDirty || settingsDirty) && !await confirm('Replace your unsaved edits?', 'Using ' + e.name + ' replaces the Play server and status URL you are editing.', 'Use this server')) return null;
    const next = await api.post('/api/servers/use', ref(e)); settingsDirty = playDirty = false;
    message('Play target is now ' + e.name + '. Press Save & check server when you are ready to contact it.'); return next;
  }
  async function removeServer(e) {
    if (!await confirm('Remove this server?', e.name + ' (' + e.server + ') is removed from your list. Nothing else changes.', 'Remove')) return null;
    return api.post('/api/servers/remove', ref(e));
  }
  async function checkAllServers() {
    const entries = savedServers().entries; if (!entries.length) { message('Add a server first.'); return null; }
    const local = entries.filter(e => e.local).length, seconds = Math.ceil(entries.length / 4) * 5;
    const detail = 'This contacts all ' + entries.length + ' saved servers, 4 at a time, waiting up to 5 seconds for each; it can take up to ' + seconds + ' seconds.' + (local ? ' ' + local + ' of them ' + (local === 1 ? 'is' : 'are') + ' on a local network address.' : '');
    if (!await confirm('Check all saved servers?', detail, 'Check all')) return null;
    const next = await api.post('/api/servers/check-all'); message('Checked ' + entries.length + ' saved servers.'); return next;
  }
  async function importServers() {
    const input = $('servers-import-file'), file = input.files[0];
    try {
      if (!file) return null;
      if (file.size > 64 * 1024) throw new Error('Choose a saved servers file of at most 64 KiB.');
      const body = {file: await file.text()}, found = await api.post('/api/servers/preview', body);
      if (!found.add) { message('Nothing new: all ' + found.total + ' servers in that file are already in your list, or the list is full.'); return null; }
      const detail = 'The file lists ' + found.total + ' servers. ' + found.add + ' will be added and ' + found.skip + ' skipped because they are already in your list or it is full. Nothing is contacted until you press Check.';
      if (!await confirm('Import ' + found.add + ' servers?', detail, 'Import')) return null;
      const next = await api.post('/api/servers/import', body); message('Imported ' + found.add + ' servers. They are not checked yet.'); return next;
    } finally { input.value = ''; }
  }
  async function exportServers() {
    const file = await api.post('/api/servers/export'), url = URL.createObjectURL(new Blob([JSON.stringify(file, null, 2) + '\n'], {type: 'application/json'})), link = node('a');
    link.href = url; link.download = 're-psn-servers.json'; document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    message('Server list downloaded. It holds names and addresses only.'); return null;
  }
  function renderTrophies(force = false) {
    const titles = state.profile && state.profile.titles || [], key = JSON.stringify(titles);
    if (!force && key === trophiesKey) return; trophiesKey = key;
    const selection = $('trophy-title').value, fragment = document.createDocumentFragment();
    for (const t of titles) { const option = node('option', t.catalog.name + ' · ' + t.catalog.title_id); option.value = t.catalog.title_id; fragment.append(option); }
    $('trophy-title').replaceChildren(fragment); if (titles.some(t => t.catalog.title_id === selection)) $('trophy-title').value = selection;
    const title = titles.find(t => t.catalog.title_id === $('trophy-title').value), unlocks = new Map((title && title.unlocks || []).map(u => [u.trophy_id, u]));
    tableRows('trophies-body', title && title.catalog.trophies, 3, 'No catalog yet. Approve a developer catalog below.', t => { const name = node('span', t.name, 'trophy-name'); if (t.description) name.append(node('span', t.description, 'trophy-description')); const u = unlocks.get(t.id); return [name, t.grade, u && u.unlocked ? 'Reported ' + stamp(u.unlocked_at) : 'Locked']; });
    const count = titles.reduce((n, t) => n + (t.unlocks || []).length, 0);
    text('overview-trophies', count + ' reported'); text('trophy-total', count + ' reported across ' + titles.length + ' titles'); text('nav-trophies', titles.length);
  }
  function renderFriends() {
    const relay = selectedRelay(state), key = JSON.stringify([state.settings.social_origin, relay, !!state.profile]); if (key === friendsKey) return; friendsKey = key;
    text('friend-origin', state.settings.social_origin || relay && relay.origin || 'No friend relay selected');
    text('friend-freshness', relay ? 'Cached authenticated snapshot · ' + stamp(relay.fetched_at) + '. Refresh to check changes; online presence is not provided.' : 'No authenticated snapshot yet. Register or refresh explicitly.');
    const s = relay && relay.state || {}, count = (s.friends || []).length;
    text('overview-friends', relay ? count + ' accepted friends' : 'No snapshot yet'); text('nav-friends', count);
    const focused = document.activeElement, peer = focused && focused.dataset.peer, oldAction = focused && focused.dataset.friendAction;
    const fragment = document.createDocumentFragment();
    for (const [kind, heading, entries, buttons] of [
      ['incoming', 'Incoming requests', s.incoming || [], [['accept', 'Accept'], ['decline', 'Decline'], ['block', 'Block']]],
      ['friends', 'Friends', s.friends || [], [['remove', 'Remove'], ['block', 'Block']]],
      ['outgoing', 'Sent requests', s.outgoing || [], [['cancel', 'Cancel request']]],
      ['blocked', 'Blocked', (s.blocked || []).map(id => ({account_id: id, label: ''})), [['unblock', 'Unblock']]]
    ]) {
      const group = node('section', undefined, 'friend-group'); group.setAttribute('aria-label', heading); group.append(node('h2', heading + ' · ' + entries.length));
      if (!entries.length) group.append(node('p', 'No ' + (kind === 'friends' ? 'accepted friends' : kind === 'blocked' ? 'blocked accounts' : 'requests') + '.', 'empty'));
      for (const contact of entries) { const item = node('div', undefined, 'friend-item'); item.append(node('strong', contact.label || 'Public account'), node('span', contact.account_id, 'friend-id')); const actions = node('div', undefined, 'actions');
        for (const [name, label] of buttons) { const button = node('button', label); button.type = 'button'; button.dataset.peer = contact.account_id; button.dataset.friendAction = name; button.dataset.mutates = ''; button.addEventListener('click', () => friendAction(name, contact.account_id)); actions.append(button); }
        item.append(actions); group.append(item);
      }
      fragment.append(group);
    }
    $('friend-groups').replaceChildren(fragment);
    if (peer) { const candidates = [...$('friend-groups').querySelectorAll('button')].filter(b => b.dataset.peer === peer); const match = candidates.find(b => b.dataset.friendAction === oldAction) || candidates[0]; if (match) match.focus(); }
  }
  function render(next) {
    if (!next || !next.settings || !next.host || !next.play || !next.grant) throw new Error('Invalid local app response.');
    state = next; const mode = state.settings.mode;
    document.documentElement.dataset.theme = ['dark', 'light', 'system'].includes(state.settings.theme) ? state.settings.theme : 'dark';
    text('version', 'v' + state.version + ' · optional client'); text('footer-version', state.version);
    show('welcome', !mode); renderSetup();
    for (const e of document.querySelectorAll('[data-personal]')) e.hidden = !personal(mode);
    for (const e of document.querySelectorAll('[data-host]')) e.hidden = !hosting(mode);
    for (const b of document.querySelectorAll('[data-role]')) b.setAttribute('aria-pressed', String(b.dataset.role === mode));
    text('workspace-label', mode === 'both' ? 'Player + operator workspace' : mode === 'host' ? 'Operator workspace' : 'Player workspace'); text('workspace-badge', mode === 'both' ? 'Both' : mode === 'host' ? 'Host' : 'Play / connect');
    text('activity-play', 'Play · ' + (state.play.status === 'reachable' ? 'checked / reachable' : state.play.status === 'unavailable' ? 'last check unavailable' : 'not checked'));
    text('activity-host', 'Host · ' + (state.host.running ? 'running on ' + state.host.address : 'stopped'));
    text('activity-grant', 'Trophies · ' + (state.grant.active ? 'reporting enabled for ' + state.grant.title_id : 'local service off'));
    text('overview-target', state.settings.server || 'No server selected');
    text('overview-host-tag', state.host.running ? 'Running' : 'Not running'); text('overview-host-address', state.host.address || state.settings.host_address || 'Not configured');
    text('overview-host-note', state.host.message || 'No owned server is running.'); text('host-message', state.host.message || 'No host is running.');
    text('host-badge', state.host.running ? 'Running' : state.host.available ? 'Not running' : 'Unavailable'); text('host-address', state.host.address || state.settings.host_address || 'Not configured'); text('host-started', state.host.started_at ? stamp(state.host.started_at) : '—');
    const profile = state.profile;
    for (const e of document.querySelectorAll('[data-needs-profile]')) e.hidden = !!profile;
    show('profile-details', !!profile); show('profile-create-form', !profile);
    text('profile-label', profile ? profile.label : 'Local profile'); $('profile-id').value = profile ? profile.account_id : '';
    text('profile-error', state.profile_error || ''); text('restore-note', profile ? 'A profile already exists here. Restore into a new private profile location using the CLI; this interface never replaces it.' : 'Restore into this new, empty profile. Remote registration and friendships are fetched from each relay after authentication.');
    text('grant-message', state.grant.message || (state.grant.active ? 'Game reporting is enabled. Keep this app running; revoke it when finished.' : 'Local game reporting is off.')); show('grant-path', state.grant.active && !!state.grant.path); text('grant-path', state.grant.active ? 'Private descriptor: ' + state.grant.path : '');
    fillSettings(); renderPlay(); renderAge(); renderServers(); renderTrophies(); renderFriends(); navigate(currentPage, false); controls();
    text('bridge-state', 'Private app session · local state updated'); text('bridge-time', new Date().toLocaleTimeString());
  }
  async function readState(initial = false) {
    if (reading || busy || !authenticated || stopped) return;
    reading = true; controls();
    const generation = generations.capture();
    try { const next = await api.state(); if (generations.current(generation) && !stopped) render(next); }
    catch (error) { if (generations.current(generation) && !stopped) { fail(error); if (!initial) text('bridge-state', 'Local refresh failed · previous view retained'); } }
    finally { reading = false; controls(); }
  }
  function friendAction(name, id = '') {
    return action(async () => { if (name === 'register' && !await confirm('Register on this friend relay?', 'This shares your public identity with ' + state.settings.social_origin + '. Reusing it makes your registrations linkable across relays. It does not publish trophies or create a provider badge.', 'Register identity')) return null; return api.post('/api/friends', {action: name, id}); }, 'Friend relay action completed. The list is a cached snapshot.');
  }
  function submit(id, fn) { $(id).addEventListener('submit', event => { event.preventDefault(); action(fn); }); }
  for (const b of document.querySelectorAll('[data-page]')) b.addEventListener('click', () => navigate(b.dataset.page));
  for (const b of document.querySelectorAll('[data-role]')) b.addEventListener('click', () => action(async () => { if (settingsDirty && !await confirm('Discard unsaved settings?', 'Switching workspace keeps the saved settings and discards these unsaved edits.', 'Discard & switch')) return null; return saveSettings({...state.settings, mode: b.dataset.role, ...(state.settings.mode ? {} : {setup_done: false})}, true); }));
  $('setup-skip').addEventListener('click', () => action(() => saveSettings({...state.settings, setup_done: true}, false), 'Setup finished. You can change everything in Settings.'));
  $('settings-form').addEventListener('input', () => { settingsDirty = true; text('settings-dirty', 'Unsaved edits'); });
  $('play-copy-btn').addEventListener('click', async () => {
    const field = $('play-copy-text'), result = await copyText(field.value, navigator.clipboard);
    if (result === 'copied') { text('play-copy-note', 'Address copied.'); return; }
    field.focus(); field.select(); text('play-copy-note', 'Copying was blocked here. The address is selected: press Ctrl+C (Cmd+C on a Mac) to copy it.');
  });
  $('play-form').addEventListener('input', () => { playDirty = true; });
  $('host-form').addEventListener('input', () => { hostDirty = true; });
  submit('settings-form', async () => { const result = await saveSettings(draftSettings(), true); if (result) message('Settings saved. Services start only when you choose their action.'); return result; });
  $('settings-reset').addEventListener('click', () => { settingsDirty = playDirty = hostDirty = false; fillSettings(true); });
  submit('play-form', async () => { const saved = await saveSettings({...state.settings, server: $('play-server').value.trim(), status_url: $('play-status-url').value.trim()}, false); if (!saved) return null; playDirty = false; render(saved); return api.post('/api/play', {title: $('play-title-id').value.trim(), board: Number($('play-board-id').value || 0), boards: $('play-boards').checked}); });
  submit('host-form', async () => { const address = $('host-bind').value.trim(); const publicBind = !localBind(address); if (publicBind && !await confirm('Listen beyond this computer?', 'The game server will listen on ' + address + '. Other computers may reach this bind. No firewall or router settings will be changed.', 'Start on this address')) return null; const saved = await saveSettings({...state.settings, host_address: address, host_relay: $('host-relay').checked}); if (!saved) return null; hostDirty = false; render(saved); return api.post('/api/host/start', {confirm_public: publicBind}); });
  $('host-stop').addEventListener('click', () => action(async () => await confirm('Stop your hosted server?', 'Players using this owned server will be disconnected. Your remote Play target remains saved.', 'Stop owned host') ? api.post('/api/host/stop') : null));
  $('use-host').addEventListener('click', () => action(() => saveSettings({...state.settings, server: clientAddress(state.host.address)}, true), 'Play target now uses your local host. Check it explicitly from Play.'));
  submit('profile-create-form', () => api.post('/api/profile/create', {label: $('profile-name').value.trim()}));
  $('catalog-file').addEventListener('change', async () => { const seq = ++catalogSequence; rawCatalog = ''; controls(); const file = $('catalog-file').files[0]; if (!file) { text('catalog-preview', 'No catalog selected.'); return; } try { if (file.size > 4 * 1024 * 1024) throw new Error('The catalog exceeds 4 MiB.'); const raw = await file.text(); catalogBody(raw); const preview = JSON.parse(raw); if (seq !== catalogSequence) return; rawCatalog = raw; text('catalog-preview', (preview.name || 'Catalog') + ' / ' + (preview.title_id || 'missing title ID') + ' · ' + (Array.isArray(preview.trophies) ? preview.trophies.length : 0) + ' trophy definitions. Backend validation still applies.'); } catch (error) { if (seq === catalogSequence) text('catalog-preview', error.message); } finally { controls(); } });
  submit('catalog-form', async () => { const result = await api.post('/api/catalog', catalogBody(rawCatalog)); rawCatalog = ''; $('catalog-file').value = ''; text('catalog-preview', 'Catalog approved.'); return result; });
  $('trophy-title').addEventListener('change', () => renderTrophies(true));
  $('grant-start').addEventListener('click', () => action(() => api.post('/api/grant', {title: $('trophy-title').value})));
  $('grant-revoke').addEventListener('click', () => action(() => api.post('/api/revoke'), 'Game reporting revoked. The game must use its local fallback or reconnect with a new grant.'));
  $('friends-refresh').addEventListener('click', () => friendAction('list'));
  $('friend-register').addEventListener('click', () => friendAction('register'));
  $('friend-request-form').addEventListener('submit', e => { e.preventDefault(); friendAction('request', $('friend-id').value.trim()); });
  submit('export-form', async () => { try { const passphrase = $('export-passphrase').value; if (passphrase !== $('export-confirm').value) throw new Error('The passphrases do not match.'); const bytes = await api.export(passphrase), url = URL.createObjectURL(new Blob([bytes], {type: 'application/octet-stream'})), link = node('a'); link.href = url; link.download = 're-psn-profile.backup'; document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000); message('Encrypted backup downloaded. Keep its passphrase separately.'); return null; } finally { $('export-passphrase').value = ''; $('export-confirm').value = ''; } });
  submit('restore-form', async () => { try { const file = $('restore-file').files[0]; if (!file || file.size > MAX_ARCHIVE) throw new Error('Choose a bounded encrypted profile backup.'); const archive = base64(new Uint8Array(await file.arrayBuffer())); const result = await api.post('/api/profile/restore', {archive, passphrase: $('restore-passphrase').value}); $('restore-file').value = ''; return result; } finally { $('restore-passphrase').value = ''; } });
  submit('servers-form', async () => { const next = await api.post('/api/servers/add', {name: $('server-name').value.trim(), server: $('server-address').value.trim(), status_url: $('server-status-url').value.trim()}); for (const id of ['server-name', 'server-address', 'server-status-url']) $(id).value = ''; return next; });
  $('servers-check-all').addEventListener('click', () => action(checkAllServers, null));
  $('servers-export').addEventListener('click', () => action(exportServers, null));
  $('servers-import-file').addEventListener('change', () => action(importServers, null));
  $('refresh-state').addEventListener('click', () => readState());
  $('shutdown').addEventListener('click', () => action(async () => { if (!await confirm('Stop the local app?', state.host.running ? 'This also stops the server owned by this app and revokes game reporting. Close this tab instead to leave them running.' : 'This revokes any local game grant. Ordinary game networking and separately hosted servers are unaffected.', 'Stop app')) return null; await api.post('/api/shutdown'); stopped = true; authenticated = false; try { sessionStorage.removeItem(SESSION_KEY); } catch {} message('The app has stopped. You can close this tab.'); text('bridge-state', 'App stopped'); return null; }));
  controls();
  (async () => {
    try { const capture = takeToken(location, history, () => sessionStorage); const token = await openSession(capture, () => sessionStorage, fetch.bind(window)); api = makeAPI(token, fetch.bind(window)); authenticated = true; controls(); readState(true); }
    catch (error) { fail(error); text('bridge-state', 'Private session unavailable'); controls(); }
  })();
  const poll = setInterval(() => { if (!document.hidden) readState(); }, 4000);
  addEventListener('pagehide', () => { clearInterval(poll); });
})();
