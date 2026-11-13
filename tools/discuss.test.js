// Tests for site/discuss.js (the pure part: which settings are passed on to giscus). Run from the repository root:
//   node --test tools/discuss.test.js
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const D = require(path.join(root, 'site', 'discuss.js'));
const source = fs.readFileSync(path.join(root, 'site', 'discuss.js'), 'utf8');
const page = fs.readFileSync(path.join(root, 'site', 'discuss.html'), 'utf8');

test('only the listed names are passed on, in order, with plain short values', () => {
  const all = { repo: 'a/b', 'repo-id': 'R_x1', evil: 'x', src: 'https://evil.example/x.js', term: 're-psn' };
  assert.deepStrictEqual(D.attrsFor((n) => (n in all ? all[n] : null)), [['repo', 'a/b'], ['repo-id', 'R_x1'], ['term', 're-psn']]);
  assert.deepStrictEqual(D.attrsFor(() => null), []);
});

test('values with markup, quotes, newlines, control characters, or over 100 characters are dropped', () => {
  for (const bad of ['<b>', 'a"b', "a'b", 'a\nb', '', 'x'.repeat(101), 'a;b', 5, undefined]) {
    assert.deepStrictEqual(D.attrsFor(() => bad), [], String(bad));
  }
  assert.strictEqual(D.attrsFor(() => 'x'.repeat(100)).length, D.ATTRS.length);
});

test('the client address is fixed and is the only outside address in the script', () => {
  assert.strictEqual(D.CLIENT, 'https://giscus.app/client.js');
  assert.deepStrictEqual([...new Set(source.match(/https?:\/\/[^\s'")]+/g).filter((u) => !u.startsWith('https://github.com/giscus')))], ['https://giscus.app/client.js']);
  for (const bad of ['eval(', 'new Function', 'innerHTML', 'document.write', 'fetch(', 'XMLHttpRequest', 'localStorage', 'document.cookie', '.style.']) {
    assert.ok(!source.includes(bad), bad);
  }
});

test('every name the page puts on the button is one the script passes on', () => {
  const names = [...page.matchAll(/ data-giscus-([a-z-]+)="/g)].map((m) => m[1]);
  assert.ok(names.length > 0);
  assert.deepStrictEqual(names.slice().sort(), D.ATTRS.slice().sort());
});
