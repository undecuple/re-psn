// Browser-local appearance and read-only polling. No external resources or HTML injection.
(() => {
  'use strict';
  const key = 're-psn-status-appearance-v1';
  const slots = ['page', 'surface', 'text', 'muted', 'accent', 'good', 'warn', 'bad'];
  const hex = value => typeof value === 'string' && /^#[0-9a-f]{6}$/i.test(value);
  function contrast(a, b) {
    const luminance = colour => {
      const channels = colour.slice(1).match(/../g).map(x => parseInt(x, 16) / 255)
        .map(x => x <= .04045 ? x / 12.92 : ((x + .055) / 1.055) ** 2.4);
      return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722;
    };
    const x = luminance(a), y = luminance(b);
    return (Math.max(x, y) + .05) / (Math.min(x, y) + .05);
  }
  function colourError(colours) {
    if (!colours || !slots.every(slot => hex(colours[slot]))) return 'Use #rrggbb for all eight colours.';
    for (const slot of slots.slice(2)) {
      for (const background of ['page', 'surface']) {
        if (contrast(colours[slot], colours[background]) < 4.5) return `${slot} needs at least 4.5:1 contrast on ${background}.`;
      }
    }
    return '';
  }
  function parseStats(text) {
    // Quote only unsafe integer tokens before JSON.parse rounds them. Complete
    // JSON strings are matched first so names, escapes and numeric-looking text
    // remain untouched. The API keeps its numeric schema; display stays exact.
    const lossless = text.replace(/"(?:[^"\\]|\\.)*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g, token => {
      if (/^-?\d+$/.test(token) && !Number.isSafeInteger(Number(token))) return JSON.stringify(token);
      return token;
    });
    return JSON.parse(lossless);
  }
  function settings(value) {
    const clean = {theme: 'system', size: '16', density: 'comfortable', colours: {}};
    if (!value || typeof value !== 'object') return clean;
    if (['system', 'dark', 'light'].includes(value.theme)) clean.theme = value.theme;
    if (['14', '16', '18'].includes(value.size)) clean.size = value.size;
    if (['compact', 'comfortable'].includes(value.density)) clean.density = value.density;
    for (const mode of ['dark', 'light']) {
      const colours = value.colours && value.colours[mode];
      if (colours && !colourError(colours)) clean.colours[mode] = Object.fromEntries(slots.map(slot => [slot, colours[slot]]));
    }
    return clean;
  }
  if (typeof module !== 'undefined' && module.exports) module.exports = {contrast, colourError, settings, parseStats};
  if (typeof document === 'undefined') return;

  const root = document.documentElement;
  const byID = id => document.getElementById(id);
  const media = matchMedia('(prefers-color-scheme: light)');
  let preferences;
  try { preferences = settings(JSON.parse(localStorage.getItem(key))); }
  catch { preferences = settings(null); } // Storage may be blocked or contain an older/invalid value.
  const mode = () => preferences.theme === 'system' ? (media.matches ? 'light' : 'dark') : preferences.theme;
  const message = text => { byID('appearance-message').textContent = text; };
  function save() {
    try { localStorage.setItem(key, JSON.stringify(preferences)); return true; }
    catch { message('Applied for this page. Browser storage is unavailable.'); return false; }
  }
  for (const slot of slots) {
    const label = document.createElement('label');
    label.textContent = slot === 'page' ? 'Background' : slot[0].toUpperCase() + slot.slice(1);
    const input = document.createElement('input');
    input.id = 'colour-' + slot;
    input.required = true;
    input.pattern = '#[0-9a-fA-F]{6}';
    input.maxLength = 7;
    input.spellcheck = false;
    input.autocomplete = 'off';
    label.append(input);
    byID('colour-fields').append(label);
  }
  function apply() {
    if (preferences.theme === 'system') root.removeAttribute('data-theme');
    else root.dataset.theme = preferences.theme;
    root.dataset.density = preferences.density;
    root.style.fontSize = preferences.size + 'px';
    for (const slot of slots) root.style.removeProperty('--' + slot);
    const custom = preferences.colours[mode()];
    if (custom) for (const slot of slots) root.style.setProperty('--' + slot, custom[slot]);
    const style = getComputedStyle(root);
    for (const slot of slots) byID('colour-' + slot).value = style.getPropertyValue('--' + slot).trim();
    // Computed custom properties may retain var() references; resolve through a colour property.
    for (const slot of slots) {
      const probe = document.createElement('span');
      probe.style.color = 'var(--' + slot + ')';
      root.append(probe);
      const channels = getComputedStyle(probe).color.match(/\d+/g);
      probe.remove();
      byID('colour-' + slot).value = '#' + channels.slice(0, 3).map(x => Number(x).toString(16).padStart(2, '0')).join('');
    }
    for (const button of document.querySelectorAll('button[data-theme]')) button.setAttribute('aria-pressed', String(button.dataset.theme === preferences.theme));
    byID('text-size').value = preferences.size;
    byID('density').value = preferences.density;
  }
  for (const button of document.querySelectorAll('button[data-theme]')) button.addEventListener('click', () => {
    preferences.theme = button.dataset.theme;
    message(''); apply(); save();
  });
  media.addEventListener('change', () => { if (preferences.theme === 'system') apply(); });
  for (const [id, field] of [['text-size', 'size'], ['density', 'density']]) byID(id).addEventListener('change', event => {
    preferences[field] = event.target.value;
    apply(); save();
  });
  byID('colours').addEventListener('submit', event => {
    event.preventDefault();
    const colours = Object.fromEntries(slots.map(slot => [slot, byID('colour-' + slot).value.trim()]));
    const error = colourError(colours);
    if (error) { message(error); return; }
    preferences.colours[mode()] = colours;
    apply(); message('Custom colours applied.'); save();
  });
  byID('reset').addEventListener('click', () => {
    preferences = settings(null);
    apply(); message('Appearance reset to the default colours.'); save();
  });

  const tabs = [byID('overview-tab'), byID('rooms-tab'), byID('boards-tab')];
  const sections = [byID('rooms-panel'), byID('boards-panel')];
  function selectTab(tab) {
    for (const item of tabs) {
      const selected = item === tab;
      item.setAttribute('aria-selected', String(selected));
      item.tabIndex = selected ? 0 : -1;
      const panel = byID(item.getAttribute('aria-controls'));
      panel.hidden = !selected;
    }
    const overview = tab.id === 'overview-tab';
    for (const section of sections) {
      const selected = overview || section.id === (tab.id === 'rooms-tab' ? 'rooms-panel' : 'boards-panel');
      section.hidden = !selected;
      byID(selected ? tab.getAttribute('aria-controls') : 'overview-view').append(section);
    }
  }
  tabs.forEach((tab, index) => {
    tab.addEventListener('click', () => selectTab(tab));
    tab.addEventListener('keydown', event => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) || event.altKey || event.ctrlKey || event.metaKey) return;
      event.preventDefault();
      const next = event.key === 'Home' ? tabs[0] : event.key === 'End' ? tabs[tabs.length - 1]
        : tabs[(index + (event.key === 'ArrowLeft' ? tabs.length - 1 : 1)) % tabs.length];
      selectTab(next); next.focus();
    });
  });
  const cell = (value, className = '') => {
    const td = document.createElement('td');
    td.textContent = String(value); td.className = className;
    return td;
  };
  function rows(id, values, empty, makeCells) {
    const fragment = document.createDocumentFragment();
    for (const value of values) {
      const row = document.createElement('tr');
      row.append(...makeCells(value)); fragment.append(row);
    }
    if (!values.length) {
      const row = document.createElement('tr'), td = cell(empty, 'empty');
      td.colSpan = 4; row.append(td); fragment.append(row);
    }
    byID(id).replaceChildren(fragment);
  }
  let paused = false, busy = false, activeRequest;
  const state = byID('refresh-state');
  const updated = () => 'Updated ' + new Date().toLocaleTimeString() + (paused ? ' · paused' : ' · every 5 seconds');
  async function refresh() {
    if (busy) return;
    busy = true; byID('refresh').disabled = true;
    activeRequest = new AbortController();
    const deadline = setTimeout(() => activeRequest.abort(), 10000);
    try {
      // Preserve a query token for this request only; never copy it to storage or links.
      const url = new URL('api/stats', location.href);
      url.search = location.search;
      const response = await fetch(url, {cache: 'no-store', credentials: 'same-origin', redirect: 'error', signal: activeRequest.signal});
      if (!response.ok) throw new Error('HTTP ' + response.status);
      const data = parseStats(await response.text());
      if (!Array.isArray(data.rooms) || !Array.isArray(data.boards)) throw new Error('Invalid status response');
      for (const [id, value] of [['uptime', data.uptime], ['clients', data.clients], ['connections', data.connections_total], ['unlisted', data.unlisted_rooms], ['rooms-count', data.rooms.length], ['boards-count', data.boards.length]]) byID(id).textContent = String(value);
      rows('rooms-body', data.rooms, 'No public rooms. A room appears here when a player hosts one.', room => [cell(room.id, 'number'), cell(room.label || room.title, 'identifier'), cell(`${(room.members || []).length}/${room.max}${room.verified ? ` (${room.verified} verified)` : ''} ${(room.members || []).map((name, i) => name + (room.rtt_ms && Number.isInteger(room.rtt_ms[i]) ? ` (${room.rtt_ms[i]} ms)` : '')).join(' ')}`), cell(room.age)]);
      rows('boards-body', data.boards, 'No leaderboards. Scores appear after a player submits one.', board => [cell(board.label || board.title, 'identifier'), cell(board.board, 'number'), cell(board.entries), cell(board.entries ? `${board.leader} (${board.best})` : 'No scores yet')]);
      state.removeAttribute('data-error'); state.textContent = updated();
    } catch (error) {
      state.dataset.error = 'true';
      state.textContent = 'Refresh failed · showing previous snapshot. Check connection or status token.';
    } finally {
      clearTimeout(deadline); busy = false; byID('refresh').disabled = false;
    }
  }
  byID('refresh').addEventListener('click', refresh);
  byID('pause').addEventListener('click', () => {
    paused = !paused;
    byID('pause').setAttribute('aria-pressed', String(paused));
    byID('pause').textContent = paused ? 'Resume refresh' : 'Pause refresh';
    if (!state.hasAttribute('data-error')) state.textContent = paused ? 'Automatic refresh paused · snapshot retained' : 'Automatic refresh · every 5 seconds';
    if (!paused) refresh();
  });
  apply(); selectTab(byID('rooms-tab'));
  for (const id of ['theme-controls', 'toolbar', 'appearance']) byID(id).hidden = false;
  byID('fallback-refresh').remove();
  setInterval(() => { if (!paused && !document.hidden) refresh(); }, 5000);
  addEventListener('pagehide', () => { if (activeRequest) activeRequest.abort(); });
})();
