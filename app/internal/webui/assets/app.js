/* Private local UI. Backend authority stays in Go; no external resources. */
(() => {
  'use strict';
  const MAX_ARCHIVE = 4 * 1024 * 1024 + 52;
  const MAX_JSON = 8 * 1024 * 1024;
  const SESSION_KEY = 're-psn-ui-token-v1';
  const ROUTES = new Set(['/api/settings', '/api/play', '/api/host/start', '/api/host/stop', '/api/profile/create', '/api/catalog', '/api/grant', '/api/revoke', '/api/friends', '/api/profile/export', '/api/profile/restore', '/api/shutdown']);
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
      const deadline = setTimeout(() => controller.abort(), 30000);
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
  const exported = {MAX_ARCHIVE, SESSION_KEY, personal, hosting, readGuard, clean, fragmentToken, takeToken, openSession, catalogBody, base64, clientAddress, localBind, selectedRelay, responseBytes, makeAPI};
  if (typeof module !== 'undefined' && module.exports) module.exports = exported;
  if (typeof document === 'undefined') return;

  const $ = id => document.getElementById(id);
  const text = (id, value) => { $(id).textContent = clean(value); };
  const show = (id, visible) => { $(id).hidden = !visible; };
  const stamp = ms => ms ? new Date(ms).toLocaleString() : 'Not checked';
  const node = (tag, value, className) => { const n = document.createElement(tag); if (value !== undefined) n.textContent = clean(value); if (className) n.className = className; return n; };
  let state, api, currentPage = 'overview', busy = false, reading = false, authenticated = false, stopped = false;
  let settingsDirty = false, playDirty = false, hostDirty = false, rawCatalog = '', catalogSequence = 0;
  let friendsKey = '', trophiesKey = '', playKey = '';
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
    return new Promise(resolve => { dialog.addEventListener('close', () => resolve(dialog.returnValue === 'accept'), {once: true}); dialog.showModal(); });
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
  function draftSettings() { const out = {}; for (const [key, id] of Object.entries(settingFields)) out[key] = key === 'host_relay' ? $(id).checked : $(id).value.trim(); return out; }
  async function saveSettings(next, discard = false) {
    let stop = false;
    if (next.mode === 'play' && state.host.running) { if (!await confirm('Stop your owned host?', 'Switching to Play will stop the server this app is hosting. Your separate Play target and local profile are kept.', 'Stop host & switch')) return null; stop = true; }
    const result = await api.post('/api/settings', {...next, stop_host: stop});
    if (discard) settingsDirty = playDirty = hostDirty = false;
    return result;
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
    tableRows('boards-body', p.boards, 4, 'No leaderboard snapshot available.', b => [b.title + ' / ' + b.id, b.entries, b.leader || '—', b.best || '—']);
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
    show('welcome', !mode); show('workspace', !!mode);
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
    fillSettings(); renderPlay(); renderTrophies(); renderFriends(); navigate(currentPage, false); controls();
    text('bridge-state', 'Private app session · local state updated ' + new Date().toLocaleTimeString());
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
  for (const b of document.querySelectorAll('[data-role]')) b.addEventListener('click', () => action(async () => { if (settingsDirty && !await confirm('Discard unsaved settings?', 'Switching workspace keeps the saved settings and discards these unsaved edits.', 'Discard & switch')) return null; return saveSettings({...state.settings, mode: b.dataset.role}, true); }));
  $('settings-form').addEventListener('input', () => { settingsDirty = true; text('settings-dirty', 'Unsaved edits'); });
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
