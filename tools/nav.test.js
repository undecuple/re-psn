// Tests for site/nav.js (the pure part: when "Previous page" is offered). Run from the repository root:
//   node --test tools/nav.test.js
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const N = require(path.join(root, 'site', 'nav.js'));
const source = fs.readFileSync(path.join(root, 'site', 'nav.js'), 'utf8');
const here = 'https://undecuple.github.io/re-psn/action.html';

test('offered only after a visit to another page of the same site', () => {
  assert.strictEqual(N.cameFromSite('https://undecuple.github.io/re-psn/', here), true);
  assert.strictEqual(N.cameFromSite('https://undecuple.github.io/re-psn/index.html#features', here), true);
  assert.strictEqual(N.cameFromSite('https://undecuple.github.io/re-psn/discuss.html', here), true);
});

test('not offered for no referrer, another site, another origin, this page itself, or junk', () => {
  for (const ref of ['', undefined, null, 5, 'not a url', 'https://example.org/re-psn/', 'http://undecuple.github.io/re-psn/',
    'https://undecuple.github.io:8443/re-psn/', 'https://undecuple.github.io/re-psn/action.html', 'https://undecuple.github.io/re-psn/action.html#x',
    'javascript:alert(1)', 'data:text/html,x', 'file:///tmp/a.html']) {
    assert.strictEqual(N.cameFromSite(ref, here), false, String(ref));
  }
  assert.strictEqual(N.cameFromSite('https://undecuple.github.io/re-psn/', 'file:///tmp/a.html'), false);
  assert.strictEqual(N.cameFromSite('https://undecuple.github.io/re-psn/', undefined), false);
});

test('the script reads nothing and sends nothing', () => {
  for (const bad of ['eval(', 'new Function', 'innerHTML', 'document.write', 'fetch(', 'XMLHttpRequest', 'localStorage', 'sessionStorage',
    'document.cookie', 'sendBeacon', '.style.', 'location.href =', 'location.assign', 'location.replace']) {
    assert.ok(!source.includes(bad), bad);
  }
  assert.strictEqual((source.match(/history\.back\(\)/g) || []).length, 1);
});
