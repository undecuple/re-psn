// Tests for site/terminal.js (the pure part: run, complete and suggest). Run from the repository root:
//   node --test tools/terminal.test.js
// They live in tools/, not site/, because the Pages workflow publishes everything under site/.
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const T = require(path.join(root, 'site', 'terminal.js'));
const data = JSON.parse(fs.readFileSync(path.join(root, 'site', 'data.json'), 'utf8'));
const source = fs.readFileSync(path.join(root, 'site', 'terminal.js'), 'utf8');

function text(res) {
  return res.lines.map((l) => (Array.isArray(l) ? l : [l]).map((s) => (typeof s === 'string' ? s : s.t)).join('')).join('\n');
}
function links(res) {
  const out = [];
  res.lines.forEach((l) => (Array.isArray(l) ? l : [l]).forEach((s) => { if (typeof s === 'object' && s.href) out.push(s.href); }));
  return out;
}

const COMMANDS = ['help', '--help', 'features', 'roadmap', 'settings', 'install', 'connect', 'version', 'changelog', 'about',
  'credits', 'licence', 'license', 'docs', 'links', 'clear', 'history'];

test('every command answers and links stay on github.com or in the repository', () => {
  for (const c of COMMANDS) {
    const res = T.run(c, data, { history: ['x'] });
    assert.ok(!res.error, c);
    if (c !== 'clear') assert.ok(res.lines.length > 0, c);
    for (const h of links(res)) assert.match(h, /^(https:\/\/github\.com\/|#)/, c + ' ' + h);
  }
});

test('unknown input', () => {
  assert.strictEqual(text(T.run('frobnicate now', data)), 'command not found: frobnicate, type --help for options');
  assert.deepStrictEqual(T.run('   ', data).lines, []);
});

test('help lists every command', () => {
  const t = text(T.run('--help', data));
  for (const c of ['features', 'roadmap', 'settings', 'install', 'connect', 'version', 'changelog', 'about', 'credits', 'licence', 'docs', 'links', 'clear', 'history']) {
    assert.ok(t.includes(c), c);
  }
});

test('features: filter, detail, errors', () => {
  const all = text(T.run('features', data));
  assert.ok(all.startsWith(data.features.length + ' features'));
  const planned = text(T.run('features --status planned', data));
  assert.ok(planned.includes(data.roadmap.Next[0]));
  assert.ok(!planned.includes('[available]'));
  const id = data.features[0].id;
  const one = text(T.run('features ' + id, data));
  assert.ok(one.includes('Evidence: ' + data.features[0].evidence));
  assert.match(text(T.run('features --status nonsense', data)), /unknown status nonsense/);
  assert.match(text(T.run('features nope', data)), /no feature with id nope/);
  assert.match(text(T.run('features --status', data)), /needs a value/);
});

test('settings: list and one by flag or variable', () => {
  assert.ok(text(T.run('settings', data)).includes('RE_PSN_ADDR'));
  assert.ok(text(T.run('settings addr', data)).includes('-addr'));
  assert.ok(text(T.run('settings RE_PSN_ADDR', data)).includes('-addr'));
  assert.match(text(T.run('settings zzz', data)), /no setting named zzz/);
});

test('version, roadmap, install, connect use the data', () => {
  assert.ok(text(T.run('version', data)).includes('v' + data.version));
  const r = text(T.run('roadmap', data));
  assert.ok(r.includes('# Done (' + data.roadmap.Done.length + ')'));
  assert.ok(text(T.run('install', data)).includes(data.install.release_tag));
  assert.ok(text(T.run('connect', data)).includes(data.connect.intro));
});

test('history and clear', () => {
  assert.match(text(T.run('history', data, { history: ['features', 'roadmap'] })), /1 {2}features\n2 {2}roadmap/);
  assert.strictEqual(T.run('clear', data).clear, true);
});

test('links in text are limited to an allowlist', () => {
  assert.strictEqual(T.hrefFor('https://evil.example/x', data), null);
  assert.strictEqual(T.hrefFor('javascript:alert(1)', data), null);
  assert.strictEqual(T.hrefFor('//evil.example', data), null);
  assert.strictEqual(T.hrefFor('docs/api.md', data), data.repository + '/blob/main/docs/api.md');
  assert.deepStrictEqual(T.md('see [x](http://a.example) and `y`', data), ['see ', 'x', ' and ', { t: 'y', c: 'code' }]);
});

test('tab completion', () => {
  assert.strictEqual(T.complete('fea', data).value, 'features ');
  assert.strictEqual(T.complete('features --st', data).value, 'features --status ');
  assert.strictEqual(T.complete('features --status pl', data).value, 'features --status planned ');
  assert.ok(T.complete('c', data).candidates.includes('connect'));
  assert.deepStrictEqual(T.complete('zzz', data).candidates, []);
});

const texts = (r) => r.items.map((i) => i.text);

test('suggest: command prefix, ghost and description', () => {
  const r = T.suggest('fea', data);
  assert.strictEqual(r.ghost, 'tures');
  assert.deepStrictEqual(texts(r), ['features']);
  assert.strictEqual(r.items[0].replace, 'features ');
  assert.match(r.items[0].desc, /status/);
  const c = T.suggest('c', data);
  assert.ok(texts(c).includes('connect') && texts(c).includes('changelog'));
  assert.strictEqual(c.ghost, c.items[0].text.slice(1));
  assert.strictEqual(T.suggest('FEA', data).ghost, 'tures');
  assert.ok(c.items.every((i) => i.desc));
});

test('suggest: flags and flag values', () => {
  assert.deepStrictEqual(texts(T.suggest('features --st', data)), ['--status']);
  const v = T.suggest('features --status ', data);
  assert.deepStrictEqual(texts(v), Object.keys(data.status_counts));
  assert.strictEqual(v.items[0].replace, 'features --status ' + v.items[0].text + ' ');
  const p = T.suggest('features --status pl', data);
  assert.deepStrictEqual(texts(p), ['planned']);
  assert.strictEqual(p.ghost, 'anned');
  assert.match(p.items[0].desc, /^\d+ features$/);
});

test('suggest: feature ids, settings names', () => {
  const id = data.features[0].id;
  const f = T.suggest('features ' + id.slice(0, 3), data);
  assert.ok(texts(f).includes(id));
  assert.strictEqual(f.items.find((i) => i.text === id).desc, data.features[0].feature);
  const s = T.suggest('settings -ad', data);
  assert.ok(texts(s).includes('-addr'));
  assert.ok(s.items.every((i) => i.desc));
});

test('suggest: nothing for unknown, empty, exact or finished input', () => {
  assert.deepStrictEqual(T.suggest('zzz', data), { ghost: '', items: [] });
  assert.deepStrictEqual(T.suggest('', data), { ghost: '', items: [] });
  assert.deepStrictEqual(T.suggest('   ', data), { ghost: '', items: [] });
  assert.deepStrictEqual(T.suggest('roadmap', data).items, []);
  assert.deepStrictEqual(T.suggest('roadmap ', data).items, []);
  assert.deepStrictEqual(T.suggest('features --status zzz', data).items, []);
});

test('suggest: substring fallback after prefix matches, with no ghost for it', () => {
  const r = T.suggest('ange', data);
  assert.deepStrictEqual(texts(r), ['changelog']);
  assert.strictEqual(r.ghost, '');
  const m = T.suggest('s', data);
  const first = m.items.findIndex((i) => !i.text.startsWith('s'));
  assert.ok(first > 0 && m.items.slice(0, first).every((i) => i.text.startsWith('s')));
  assert.ok(m.items.slice(first).every((i) => i.text.includes('s')));
  assert.ok(m.ghost !== '');
});

test('suggest: at most 8 items', () => {
  assert.ok(data.features.length > 8);
  assert.strictEqual(T.suggest('features ', data).items.length, 8);
  assert.ok(T.suggest('', data).items.length <= 8);
  assert.ok(T.suggest('e', data).items.length <= 8);
});

test('suggest: only when the cursor is at the end', () => {
  assert.deepStrictEqual(T.suggest('fea', data, 1), { ghost: '', items: [] });
  assert.strictEqual(T.suggest('fea', data, 3).ghost, 'tures');
  assert.strictEqual(T.suggest('fea', data, undefined).ghost, 'tures');
});

test('the autocomplete markup is built with DOM calls and carries the combobox attributes', () => {
  for (const needle of ["'role', 'combobox'", "'aria-autocomplete', 'both'", "'aria-expanded'", "'aria-controls', 'tui-list'",
    "'aria-activedescendant'", "'role', 'listbox'", "'role', 'option'", "'role', 'status'", "'aria-live', 'polite'"]) {
    assert.ok(source.includes(needle), needle);
  }
});

test('the script has no dynamic code or HTML strings, and one network call', () => {
  for (const bad of ['eval(', 'new Function', 'innerHTML', 'outerHTML', 'insertAdjacentHTML', 'document.write', 'XMLHttpRequest',
    'WebSocket', 'sendBeacon', 'document.cookie', 'sessionStorage', 'indexedDB', 'import(', 'importScripts', "setAttribute('style'"]) {
    assert.ok(!source.includes(bad), bad);
  }
  assert.strictEqual(source.split('fetch(').length - 1, 1);
  assert.ok(source.includes("fetch('data.json'"));
});

test('output carries classes for colour, never styles', () => {
  const cls = (res) => res.lines.flatMap((l) => (Array.isArray(l) ? l : [l])).filter((s) => typeof s === 'object').map((s) => s.c);
  assert.ok(cls(T.run('nonsense', data)).includes('err'));
  assert.ok(cls(T.run('features nope', data)).includes('err'));
  assert.ok(cls(T.run('help', data)).includes('cmd'));
  assert.ok(cls(T.run('features', data)).includes('head'));
  assert.ok(cls(T.run('features', data)).includes('id'));
  assert.ok(cls(T.run('features', data)).some((c) => /^tag /.test(c)));
  assert.ok(cls(T.run('settings addr', data)).includes('flag'));
  assert.ok(!/\.style\b/.test(source));
});

test('there is one dark theme: no theme command, switch or stored choice', () => {
  assert.ok(T.run('theme', data).error);
  assert.ok(!source.includes('data-theme'));
  assert.ok(!source.includes('applyTheme'));
  assert.ok(!source.includes('syncControls'));
  assert.ok(source.includes("document.querySelector('.banner') || document.querySelector('.skip')"));
  assert.ok(!source.includes('setItem'));
  assert.ok(source.includes("removeItem('re-psn-theme')"));
});

// ---------------------------------------------------------------- the numbered file

test('each answer has a file name; errors and clear have none', () => {
  assert.strictEqual(T.run('features', data).file, 'features');
  assert.strictEqual(T.run('features --status planned', data).file, 'features/planned');
  assert.strictEqual(T.run('features ' + data.features[0].id, data).file, 'features/' + data.features[0].id);
  assert.strictEqual(T.run('settings', data).file, 'settings');
  assert.strictEqual(T.run('settings -addr', data).file, 'settings/addr');
  assert.strictEqual(T.run('help', data).file, 'help');
  assert.strictEqual(T.run('--help', data).file, 'help');
  assert.strictEqual(T.run('features nope', data).file, undefined);
  assert.strictEqual(T.run('nonsense', data).file, undefined);
  assert.strictEqual(T.run('clear', data).file, undefined);
});



test('gutter width follows the digits of the line count', () => {
  assert.strictEqual(T.gutterWidth(0), 1);
  assert.strictEqual(T.gutterWidth(9), 1);
  assert.strictEqual(T.gutterWidth(10), 2);
  assert.strictEqual(T.gutterWidth(99), 2);
  assert.strictEqual(T.gutterWidth(100), 3);
  assert.strictEqual(T.gutterWidth(999), 3);
  assert.strictEqual(T.gutterWidth(12345), 4);
});


test('classify: options, variables, numbers and a leading label; the text is unchanged', () => {
  const t = (spans) => spans.map((s) => (typeof s === 'string' ? s : s.t)).join('');
  const c = (spans) => spans.filter((s) => typeof s === 'object').map((s) => s.c + ':' + s.t);
  const s = 'Evidence: use -addr or --status with RE_PSN_ADDR on :36100, v0.3.0 and 1.5 but not ps3 or a-b';
  const r = T.classify(s, true);
  assert.strictEqual(t(r), s);
  assert.deepStrictEqual(c(r), ['label:Evidence:', 'opt:-addr', 'opt:--status', 'env:RE_PSN_ADDR', 'num:36100', 'num:v0.3.0', 'num:1.5']);
  assert.deepStrictEqual(c(T.classify('Evidence: x', false)), []);
  assert.deepStrictEqual(c(T.classify('each is also a RE_PSN_<FLAG> variable', false)), ['env:RE_PSN_<FLAG>']);
  assert.deepStrictEqual(T.classify('', true), []);
});

// A small stand-in for the document: elements with children, attributes and textContent, enough for renderFile.
function fakeDoc() {
  function Text(t) { this.data = String(t); }
  Object.defineProperty(Text.prototype, 'textContent', { get() { return this.data; } });
  function El(tag) { this.tagName = tag.toUpperCase(); this.children = []; this.attrs = {}; this.className = ''; }
  El.prototype.appendChild = function (c) { this.children.push(c); return c; };
  El.prototype.setAttribute = function (k, v) { this.attrs[k] = String(v); };
  El.prototype.getAttribute = function (k) { return k in this.attrs ? this.attrs[k] : null; };
  Object.defineProperty(El.prototype, 'textContent', {
    get() { return this.children.map((c) => c.textContent).join(''); },
    set(v) { this.children = [new Text(v)]; },
  });
  El.prototype.all = function (cls) {
    const out = [];
    const walk = (n) => { if (n.children) { if (n.className.split(' ').includes(cls)) out.push(n); n.children.forEach(walk); } };
    walk(this);
    return out;
  };
  return { createElement: (t) => new El(t), createTextNode: (t) => new Text(t) };
}

test('copy: the gutter is not part of the text of the output, and has no text of its own', () => {
  const doc = fakeDoc();
  const lines = ['alpha beta', ['gamma ', { t: 'delta', c: 'id' }], '', 'epsilon'];
  const f = T.renderFile(doc, { name: 'demo', lines });
  const body = f.all('file-body')[0];
  assert.strictEqual(body.textContent, 'alpha betagamma deltaepsilon');
  assert.ok(!/\d/.test(f.textContent), f.textContent);
  const rows = f.all('fl');
  assert.strictEqual(rows.length, 4);
  rows.forEach((row, i) => {
    const g = row.children[0];
    assert.strictEqual(g.className, 'gut');
    assert.strictEqual(g.children.length, 0);
    assert.strictEqual(g.getAttribute('aria-hidden'), 'true');
    assert.strictEqual(g.getAttribute('data-n'), String(i + 1));
    assert.strictEqual(row.children[1].textContent, T.lineText(lines[i]));
  });
  const head = f.all('file-head')[0];
  assert.strictEqual(head.getAttribute('aria-hidden'), 'true');
  assert.strictEqual(head.textContent, 'File: demo');
  assert.strictEqual(f.getAttribute('aria-label'), 'demo, 4 lines');
  assert.strictEqual(f.className, 'file gw1');
});

test('renderFile: every line is numbered and the gutter width follows the line count', () => {
  const doc = fakeDoc();
  const lines = Array.from({ length: 120 }, (_, i) => 'line ' + String.fromCharCode(97 + (i % 26)));
  const f = T.renderFile(doc, { name: 'long', lines });
  const rows = f.all('fl');
  assert.strictEqual(rows.length, 120);
  assert.strictEqual(rows[0].children[0].getAttribute('data-n'), '1');
  assert.strictEqual(rows[119].children[0].getAttribute('data-n'), '120');
  assert.strictEqual(f.className, 'file gw3');
  assert.ok(!f.all('file-body')[0].textContent.includes('101'));
});

test('renderFile colours the plain text and keeps span classes and safe links', () => {
  const doc = fakeDoc();
  const res = T.run('settings', data);
  const f = T.renderFile(doc, { name: res.file, lines: res.lines });
  assert.ok(f.all('flag').length > 0);
  assert.ok(f.all('env').length > 0);
  assert.strictEqual(f.all('file-body')[0].textContent, res.lines.map(T.lineText).join(''));
  const a = T.renderFile(doc, { name: 'x', lines: [[{ t: 'bad', href: 'javascript:alert(1)' }, { t: 'ok', href: 'docs/api.md' }]] });
  const spans = a.all('fl')[0].children[1].children;
  assert.strictEqual(spans[0].tagName, 'SPAN');
  assert.strictEqual(spans[1].tagName, 'A');
  assert.strictEqual(spans[1].getAttribute('rel'), 'noopener noreferrer');
});
