/* re:psn "see it in action" page: plays the scenarios of data.json ("action") on the diagrams of action.html.
   Without this script the page is the static one: all four scenarios, their diagrams and numbered steps. With it one scenario shows at a
   time, with Play/Pause, Back, Step, Restart, a speed choice, a clickable step list and the keys Space, Right, Left and R.
   The first half is pure (the state machine and the packet position) and is tested with `node --test tools/action.test.js`; the second
   half builds the controls with DOM calls and moves the packet with SVG attributes only. No library, one fetch of data.json, nothing
   stored, no style attribute, no HTML strings. With prefers-reduced-motion nothing moves and nothing plays by itself: Step and Back
   show each step at once. */
(function (root) {
  'use strict';

  var STEP_MS = 1100; // a packet crosses its path in this long at speed 1
  var HOLD_MS = 600;  // after it arrives, while playing, the pause before the next step starts
  var SPEEDS = [0.5, 1, 2];
  var LOST_AT = 0.55; // a packet that does not get through stops this far along its path

  // ---- the state machine. cur is the step shown (-1 before the first), t how far its packet is (0 to 1). ----
  function create(n) { return { n: n, cur: -1, t: 0, hold: 0, playing: false, speed: 1 }; }
  function atEnd(s) { return s.n > 0 && s.cur === s.n - 1 && s.t >= 1; }
  function restart(s) { s.cur = -1; s.t = 0; s.hold = 0; s.playing = false; return s; }
  function play(s) {
    if (s.n === 0) { return s; }
    if (atEnd(s)) { restart(s); }
    if (s.cur < 0) { s.cur = 0; s.t = 0; s.hold = 0; }
    s.playing = true;
    return s;
  }
  function pause(s) { s.playing = false; return s; }
  // One step forward: finish the packet on its way, else start the next step. Never plays on by itself.
  function forward(s) {
    s.playing = false;
    if (s.cur >= 0 && s.t < 1) { s.t = 1; return s; }
    if (s.cur < s.n - 1) { s.cur++; s.t = 0; s.hold = 0; }
    return s;
  }
  function back(s) {
    s.playing = false; s.hold = 0;
    if (s.cur >= 0) { s.cur--; s.t = s.cur < 0 ? 0 : 1; }
    return s;
  }
  function jump(s, i) {
    if (i < 0 || i >= s.n) { return s; }
    s.cur = i; s.t = 1; s.hold = 0; s.playing = false;
    return s;
  }
  function setSpeed(s, v) { if (SPEEDS.indexOf(v) >= 0) { s.speed = v; } return s; }
  // Time passes by dt ms. With reduced motion every packet is at its end at once and nothing advances by itself.
  function tick(s, dt, reduced) {
    if (s.cur < 0) { return s; }
    if (reduced) { s.t = 1; s.playing = false; return s; }
    if (s.t < 1) {
      s.t = Math.min(1, s.t + dt * s.speed / STEP_MS);
      if (s.t >= 1) { s.hold = 0; }
      return s;
    }
    if (s.playing) {
      if (s.cur >= s.n - 1) { s.playing = false; return s; }
      s.hold += dt * s.speed;
      if (s.hold >= HOLD_MS) { s.cur++; s.t = 0; s.hold = 0; }
    }
    return s;
  }
  function needsFrame(s) { return s.cur >= 0 && (s.t < 1 || s.playing); }

  // ---- where the packet is: a point on the step's path (a list of [x, y] points), by length ----
  function along(path, t) {
    var lens = [], total = 0, i, d;
    for (i = 1; i < path.length; i++) {
      d = Math.sqrt(Math.pow(path[i][0] - path[i - 1][0], 2) + Math.pow(path[i][1] - path[i - 1][1], 2));
      lens.push(d); total += d;
    }
    var want = total * Math.max(0, Math.min(1, t));
    for (i = 0; i < lens.length; i++) {
      if (want <= lens[i] || i === lens.length - 1) {
        var f = lens[i] ? Math.min(1, want / lens[i]) : 1;
        return [path[i][0] + (path[i + 1][0] - path[i][0]) * f, path[i][1] + (path[i + 1][1] - path[i][1]) * f];
      }
      want -= lens[i];
    }
    return path[0];
  }
  // The packet of a step at progress t, or null when nothing moves (a note).
  function place(step, t) {
    if (!step.path || step.path.length < 2) { return null; }
    var p = along(step.path, step.lost ? Math.min(t, LOST_AT) : t);
    return { x: Math.round(p[0] * 10) / 10, y: Math.round(p[1] * 10) / 10, kind: step.kind, lost: !!step.lost && t >= 1 };
  }
  function captionFor(sc, s) {
    if (s.cur < 0) { return 'Press Play or Step to start.'; }
    var st = sc.steps[s.cur];
    return 'Step ' + (s.cur + 1) + ' of ' + sc.steps.length + ': ' + (st.message ? st.message + '. ' : '') + st.caption;
  }

  var api = { STEP_MS: STEP_MS, HOLD_MS: HOLD_MS, SPEEDS: SPEEDS, LOST_AT: LOST_AT, create: create, atEnd: atEnd, restart: restart,
    play: play, pause: pause, forward: forward, back: back, jump: jump, setSpeed: setSpeed, tick: tick, needsFrame: needsFrame,
    along: along, place: place, captionFor: captionFor };
  if (typeof module !== 'undefined' && module.exports) { module.exports = api; }
  if (typeof document === 'undefined') { return; }

  // ---- the page ----
  var NS = 'http://www.w3.org/2000/svg';

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) { e.className = cls; }
    if (text !== undefined) { e.textContent = text; }
    return e;
  }
  function button(label, onClick) {
    var b = el('button', 'ctl', label);
    b.type = 'button';
    b.addEventListener('click', onClick);
    return b;
  }

  function mount(data) {
    var scenarios = data.action && data.action.scenarios;
    var host = document.getElementById('scenarios');
    if (!scenarios || !scenarios.length || !host) { return; }
    var mq = root.matchMedia && root.matchMedia('(prefers-reduced-motion: reduce)');
    var reduced = !!(mq && mq.matches);

    // Plain lookup tables without a prototype, so a hash such as #constructor or #__proto__ is simply not a scenario.
    var views = Object.create(null), states = Object.create(null), active = null, raf = 0, last = 0;
    scenarios.forEach(function (sc) {
      var sec = document.getElementById(sc.id);
      var svg = sec && sec.querySelector('svg.action-diagram');
      var items = sec ? sec.querySelectorAll('ol.steps > li') : [];
      if (!sec || !svg || items.length !== sc.steps.length) { return; }
      var dot = document.createElementNS(NS, 'circle');
      dot.setAttribute('class', 'packet off');
      dot.setAttribute('r', '7');
      var label = document.createElementNS(NS, 'text');
      label.setAttribute('class', 'packet-label off');
      label.setAttribute('text-anchor', 'middle');
      svg.appendChild(dot);
      svg.appendChild(label);
      views[sc.id] = { sc: sc, sec: sec, dot: dot, label: label, items: items };
      states[sc.id] = create(sc.steps.length);
      Array.prototype.forEach.call(items, function (li, i) {
        var who = li.querySelector('.who');
        if (!who) { return; }
        var b = el('button', 'jump');
        b.type = 'button';
        b.setAttribute('aria-label', 'Show step ' + (i + 1));
        li.insertBefore(b, who);
        b.appendChild(who);
        b.addEventListener('click', function () { jump(states[sc.id], i); after(); });
      });
    });
    var ids = scenarios.map(function (sc) { return sc.id; }).filter(function (id) { return views[id]; });
    if (!ids.length) { return; }

    // controls
    var bar = el('div', 'action-controls');
    bar.setAttribute('role', 'group');
    bar.setAttribute('aria-label', 'Scenario and playback');
    var tabs = el('p', 'ctl-row');
    var tabBtns = Object.create(null);
    ids.forEach(function (id) {
      var b = button(views[id].sc.title.split(':')[0], function () { select(id); });
      tabBtns[id] = b;
      tabs.appendChild(b);
    });
    var row = el('p', 'ctl-row');
    var playBtn = button('Play', function () {
      var s = states[active];
      if (s.playing) { pause(s); } else { play(s); }
      after();
    });
    var backBtn = button('Back', function () { back(states[active]); after(); });
    var stepBtn = button('Step', function () { forward(states[active]); after(); });
    var restartBtn = button('Restart', function () { restart(states[active]); after(); });
    var speed = el('select', 'ctl');
    speed.setAttribute('aria-label', 'Speed');
    SPEEDS.forEach(function (v) {
      var o = el('option', '', v + '×');
      o.value = String(v);
      if (v === 1) { o.selected = true; }
      speed.appendChild(o);
    });
    speed.addEventListener('change', function () { setSpeed(states[active], parseFloat(speed.value)); });
    row.appendChild(playBtn); row.appendChild(backBtn); row.appendChild(stepBtn); row.appendChild(restartBtn);
    row.appendChild(el('label', 'ctl-speed', 'Speed '));
    row.lastChild.appendChild(speed);
    var caption = el('p', 'action-caption');
    caption.id = 'action-caption';
    caption.setAttribute('role', 'status');
    caption.setAttribute('aria-live', 'polite');
    var hint = el('p', 'action-hint', reduced
      ? 'Your system asks for reduced motion, so nothing moves by itself: use Step and Back, or pick a step in the list.'
      : 'Keys: Space plays or pauses, Right steps, Left goes back, R restarts.');
    bar.appendChild(tabs); bar.appendChild(row); bar.appendChild(caption); bar.appendChild(hint);
    // The bar sits in the scenario that is showing, just above its diagram (select() moves it).
    if (reduced) { playBtn.disabled = true; speed.disabled = true; }

    function render() {
      var v = views[active], s = states[active];
      Array.prototype.forEach.call(v.items, function (li, i) {
        li.classList.toggle('now', i === s.cur);
        li.classList.toggle('done', i < s.cur);
        if (i === s.cur) { li.setAttribute('aria-current', 'step'); } else { li.removeAttribute('aria-current'); }
      });
      var p = s.cur >= 0 ? place(v.sc.steps[s.cur], s.t) : null;
      v.dot.setAttribute('class', 'packet ' + (p ? (p.lost ? 'p-lost' : 'p-' + p.kind) : 'off'));
      v.label.setAttribute('class', 'packet-label' + (p ? '' : ' off'));
      if (p) {
        var st = v.sc.steps[s.cur];
        v.dot.setAttribute('cx', String(p.x)); v.dot.setAttribute('cy', String(p.y));
        v.label.setAttribute('x', String(p.x)); v.label.setAttribute('y', String(p.y - 12));
        v.label.textContent = (st.message || 'game traffic') + (p.lost ? ' (lost)' : '');
      }
      playBtn.textContent = s.playing ? 'Pause' : (atEnd(s) ? 'Replay' : 'Play');
      backBtn.disabled = s.cur < 0;
      stepBtn.disabled = atEnd(s);
      restartBtn.disabled = s.cur < 0;
      caption.textContent = captionFor(v.sc, s);
    }
    function frame(ts) {
      raf = 0;
      var s = states[active];
      tick(s, last ? Math.min(100, ts - last) : 0, reduced);
      last = ts;
      render();
      if (needsFrame(s)) { raf = root.requestAnimationFrame(frame); } else { last = 0; }
    }
    function after() {
      if (reduced) { tick(states[active], 0, true); }
      render();
      if (!raf && needsFrame(states[active]) && !reduced) { last = 0; raf = root.requestAnimationFrame(frame); }
    }
    function select(id) {
      if (!views[id]) { return; }
      if (active) { pause(states[active]); }
      active = id;
      views[id].sec.insertBefore(bar, views[id].sec.querySelector('svg.action-diagram'));
      restart(states[id]);
      ids.forEach(function (k) {
        views[k].sec.hidden = k !== id;
        tabBtns[k].setAttribute('aria-pressed', k === id ? 'true' : 'false');
      });
      speed.value = String(states[id].speed);
      after();
    }

    document.addEventListener('keydown', function (e) {
      if (e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) { return; }
      var t = e.target, tag = t && t.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (t && t.isContentEditable)) { return; }
      var s = states[active];
      if (e.key === ' ' && tag !== 'BUTTON' && tag !== 'A' && !reduced) { e.preventDefault(); if (s.playing) { pause(s); } else { play(s); } after(); }
      else if (e.key === 'ArrowRight') { e.preventDefault(); forward(s); after(); }
      else if (e.key === 'ArrowLeft') { e.preventDefault(); back(s); after(); }
      else if (e.key === 'r' || e.key === 'R') { restart(s); after(); }
    });
    root.addEventListener('hashchange', function () { select(root.location.hash.slice(1)); });
    select(views[root.location.hash.slice(1)] ? root.location.hash.slice(1) : ids[0]);
  }

  function start() {
    fetch('data.json', { credentials: 'same-origin' })
      .then(function (r) { if (!r.ok) { throw new Error('data.json'); } return r.json(); })
      .then(mount)
      .catch(function () { /* the static page stays as it is */ });
  }
  if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', start); } else { start(); }
})(typeof window !== 'undefined' ? window : globalThis);
