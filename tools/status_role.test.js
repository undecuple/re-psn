const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {statusRole, hostRows, parseStats} = require('../server/status.js');
test('status view intent is only a bounded browser preference', () => {
  for (const role of ['play', 'host', 'both']) assert.equal(statusRole(role), role);
  for (const value of [null, undefined, '', 'admin', '<script>', {role: 'host'}, ['host']]) assert.equal(statusRole(value), 'play');
});
test('host observation projects existing counters without inventing missing values or losing integers', () => {
  const missing = new Map(hostRows({})); assert.equal(missing.get('Relay'), 'Not reported'); assert.equal(missing.get('Connection limit'), 'Not reported'); assert.equal(missing.get('Relay payload'), 'Not reported');
  const value = parseStats('{"version":"synthetic","relay":true,"relay_bytes":18446744073709551615,"limits":{"max_conns":0,"max_req_rate":200},"tcp_refusals":{"conns":9007199254740993}}');
  const rows = new Map(hostRows(value)); assert.equal(rows.get('Relay'), 'Enabled'); assert.equal(rows.get('Relay payload'), '18446744073709551615 bytes'); assert.equal(rows.get('Connection limit'), 'Unlimited'); assert.equal(rows.get('Refused connections'), '9007199254740993');
  assert.equal(new Map(hostRows({relay: false})).get('Relay'), 'Disabled');
});
test('status role UI preserves original tables, tab controls, appearance and no-JavaScript fallback', () => {
  const html = fs.readFileSync(path.join(__dirname, '../server/status.html'), 'utf8'), js = fs.readFileSync(path.join(__dirname, '../server/status.js'), 'utf8');
  for (const id of ['overview-tab', 'rooms-tab', 'boards-tab', 'overview-view', 'rooms-view', 'boards-view', 'rooms-panel', 'boards-panel', 'rooms-body', 'boards-body', 'appearance', 'theme-controls', 'refresh', 'pause', 'fallback-refresh']) assert.match(html, new RegExp('id="' + id + '"'));
  for (const role of ['play', 'host', 'both']) assert.match(html, new RegExp('data-role="' + role + '"'));
  assert.match(html, /id="role-controls"[^>]*hidden/); assert.match(html, /id="host-view"[^>]*hidden/); assert.match(html, /<noscript>/); assert.match(html, /no administrative authority/);
  assert.match(js, /re-psn-status-role-v1/); assert.match(js, /re-psn-status-appearance-v1/); assert.match(js, /new URL\('api\/stats', location.href\)/);
  assert.doesNotMatch(js, /\/api\/host|\/api\/settings|\/api\/friends|\/admin\/|method:\s*['"]POST|innerHTML/);
});
