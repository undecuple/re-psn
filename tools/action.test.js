// Tests for site/action.js (the pure part: the state machine and the packet position). Run from the repository root:
//   node --test tools/action.test.js
// They live in tools/, not site/, because the Pages workflow publishes everything under site/.
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const A = require(path.join(root, 'site', 'action.js'));
const data = JSON.parse(fs.readFileSync(path.join(root, 'site', 'data.json'), 'utf8'));
const scenarios = data.action.scenarios;

test('a new player has shown nothing; Step starts the first step and finishes it before the next', () => {
  const s = A.create(3);
  assert.deepStrictEqual([s.cur, s.t, s.playing], [-1, 0, false]);
  A.forward(s);
  assert.deepStrictEqual([s.cur, s.t], [0, 0]);
  A.forward(s); // the packet of step 0 is on its way: finish it
  assert.deepStrictEqual([s.cur, s.t], [0, 1]);
  A.forward(s);
  assert.deepStrictEqual([s.cur, s.t], [1, 0]);
  assert.strictEqual(s.playing, false);
});

test('Step stops at the last step and never goes past it', () => {
  const s = A.create(2);
  for (let i = 0; i < 10; i++) { A.forward(s); }
  assert.deepStrictEqual([s.cur, s.t], [1, 1]);
  assert.ok(A.atEnd(s));
});

test('Back goes to the end of the step before, and to the start from the first step', () => {
  const s = A.create(3);
  A.jump(s, 2);
  A.back(s);
  assert.deepStrictEqual([s.cur, s.t], [1, 1]);
  A.back(s); A.back(s);
  assert.deepStrictEqual([s.cur, s.t], [-1, 0]);
  A.back(s);
  assert.strictEqual(s.cur, -1);
});

test('Play runs every step in order, then stops by itself at the end', () => {
  const s = A.create(3);
  A.play(s);
  const seen = [];
  for (let i = 0; i < 1000 && s.playing; i++) {
    A.tick(s, 50, false);
    if (seen[seen.length - 1] !== s.cur) { seen.push(s.cur); }
  }
  assert.deepStrictEqual(seen, [0, 1, 2]);
  assert.strictEqual(s.playing, false);
  assert.ok(A.atEnd(s));
});

test('while playing, an arrived packet rests for the hold time before the next step starts', () => {
  const s = A.create(3);
  A.play(s);
  A.tick(s, A.STEP_MS, false); // the packet of step 0 arrives
  assert.deepStrictEqual([s.cur, s.t], [0, 1]);
  A.tick(s, A.HOLD_MS - 1, false);
  assert.strictEqual(s.cur, 0);
  A.tick(s, 1, false);
  assert.deepStrictEqual([s.cur, s.t], [1, 0]);
  // at double speed the same rest is half as long
  const f = A.create(3);
  A.setSpeed(f, 2); A.play(f);
  A.tick(f, A.STEP_MS / 2, false);
  A.tick(f, A.HOLD_MS / 2, false);
  assert.strictEqual(f.cur, 1);
});

test('Pause holds the state, and Play after the end starts again from the first step', () => {
  const s = A.create(3);
  A.play(s);
  A.tick(s, 500, false);
  const t = s.t;
  A.pause(s);
  A.tick(s, 5000, false);
  assert.ok(s.t >= t); // an arrived packet may finish, but no step starts
  assert.strictEqual(s.cur, 0);
  A.jump(s, 2);
  assert.ok(A.atEnd(s));
  A.play(s);
  assert.deepStrictEqual([s.cur, s.t, s.playing], [0, 0, true]);
});

test('Restart goes back to before the first step and stops', () => {
  const s = A.create(3);
  A.play(s); A.tick(s, 300, false);
  A.restart(s);
  assert.deepStrictEqual([s.cur, s.t, s.playing, s.hold], [-1, 0, false, 0]);
});

test('speed changes how fast a packet travels, and only the listed speeds are taken', () => {
  const a = A.create(2), b = A.create(2);
  A.setSpeed(b, 2);
  A.setSpeed(b, 3);
  assert.strictEqual(b.speed, 2);
  A.forward(a); A.forward(b);
  A.tick(a, 100, false); A.tick(b, 100, false);
  assert.ok(Math.abs(b.t - 2 * a.t) < 1e-9);
});

test('reduced motion: every packet is at its end at once and nothing plays by itself', () => {
  const s = A.create(3);
  A.forward(s);
  A.tick(s, 0, true);
  assert.deepStrictEqual([s.cur, s.t, s.playing], [0, 1, false]);
  A.play(s);
  A.tick(s, 10000, true);
  assert.deepStrictEqual([s.cur, s.t, s.playing], [0, 1, false]);
});

test('the clock needs frames only while something moves', () => {
  const s = A.create(2);
  assert.strictEqual(A.needsFrame(s), false);
  A.forward(s);
  assert.strictEqual(A.needsFrame(s), true);
  A.forward(s);
  assert.strictEqual(A.needsFrame(s), false);
});

test('jump shows the whole step at once and ignores a step that does not exist', () => {
  const s = A.create(3);
  A.jump(s, 1);
  assert.deepStrictEqual([s.cur, s.t, s.playing], [1, 1, false]);
  A.jump(s, 7); A.jump(s, -2);
  assert.strictEqual(s.cur, 1);
});

test('the packet follows its path by length, over a path with a bend', () => {
  const p = [[0, 0], [100, 0], [100, 100]];
  assert.deepStrictEqual(A.along(p, 0), [0, 0]);
  assert.deepStrictEqual(A.along(p, 0.5), [100, 0]);
  assert.deepStrictEqual(A.along(p, 0.75), [100, 50]);
  assert.deepStrictEqual(A.along(p, 1), [100, 100]);
  assert.deepStrictEqual(A.along(p, 7), [100, 100]);
  assert.deepStrictEqual(A.along(p, -1), [0, 0]);
});

test('a packet that does not get through stops part of the way and is marked lost at the end', () => {
  const step = { path: [[0, 0], [100, 0]], kind: 'udp', lost: true };
  assert.strictEqual(A.place(step, 0.2).lost, false);
  const end = A.place(step, 1);
  assert.strictEqual(end.lost, true);
  assert.strictEqual(end.x, 55); // 0.55 of the path, to one decimal
  assert.strictEqual(A.place({ path: [], kind: 'note', lost: false }, 1), null);
});

test('every step of every scenario has a place for its packet, or is a note', () => {
  assert.strictEqual(scenarios.length, 4);
  for (const sc of scenarios) {
    for (let i = 0; i < sc.steps.length; i++) {
      const st = sc.steps[i];
      const p = A.place(st, 1);
      if (st.kind === 'note') { assert.strictEqual(p, null, sc.id + ' ' + i); } else { assert.ok(p, sc.id + ' ' + i); }
    }
  }
});

test('the caption names the step, its message and its place in the list', () => {
  const sc = scenarios[2];
  const s = A.create(sc.steps.length);
  assert.match(A.captionFor(sc, s), /Press Play or Step/);
  A.jump(s, 7);
  const c = A.captionFor(sc, s);
  assert.ok(c.startsWith('Step 8 of ' + sc.steps.length + ': ROUTE. '));
});
