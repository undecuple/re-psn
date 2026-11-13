/* re-psn project site: the terminal-style interface. Hand-written, no dependencies, no build step.

   Without this file the page is an ordinary document. With it, the page is laid out as menu, content and a command line.
   The commands answer from data.json (the one request, to the same origin); nothing is sent anywhere, no cookies are set,
   and nothing is stored. Text is built with textContent and DOM nodes,
   never HTML strings, and a link is made only for a github.com address or a path in this repository.

   Each answer is shown as a numbered file: a name, a line-number gutter and a frame.

   The first half is pure (run, complete, suggest and the numbered file) and is tested with `node --test tools/terminal.test.js`. The second half touches the page. */
(function (root) {
  'use strict';

  var HINT = 'type --help for options';
  var HELP = [
    ['--help, help', 'this list'],
    ['features [--status S] [id]', 'features with their status; an id gives the detail and evidence'],
    ['roadmap', 'done, now, next, later, not planned'],
    ['settings [name]', 'flags and RE_PSN_* variables with defaults'],
    ['install', 'archives, container, from source'],
    ['connect', 'the ways to connect'],
    ['action [scenario]', 'simulated runs: direct, punch, relayed, blocked; a name gives its steps'],
    ['version', 'the version'],
    ['changelog', 'the latest changes'],
    ['about', 'what re-psn is'],
    ['credits', 'who wrote what'],
    ['licence', 'the licence'],
    ['docs', 'the documentation'],
    ['links', 'repository and related links'],
    ['clear', 'clear the screen'],
    ['history', 'commands typed in this tab']
  ];
  var COMMANDS = ['--help', 'help', 'features', 'roadmap', 'settings', 'install', 'connect', 'action', 'version', 'changelog',
    'about', 'credits', 'licence', 'license', 'docs', 'links', 'clear', 'history'];
  var CANONICAL = { '--help': 'help', license: 'licence' };

  // ------------------------------------------------------------------ pure part

  function hrefFor(url, data) {
    // Only github.com or a path in the repository. Anything else is shown as text, not as a link.
    if (typeof url !== 'string') { return null; }
    if (/^https:\/\/github\.com\//.test(url)) { return url; }
    if (/^[a-z][a-z0-9+.-]*:/i.test(url) || url.slice(0, 2) === '//') { return null; }
    if (url.charAt(0) === '#') { return url; }
    if (url === 'action.html') { return url; }
    return (data && data.repository ? data.repository : 'https://github.com/undecuple/re-psn') + '/blob/main/' + url.replace(/^\/+/, '');
  }

  // `code` and [text](link) in a string -> spans. A span is a string or {t, c, href}.
  function md(text, data) {
    var out = [];
    var re = /`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)\)/g;
    var last = 0;
    var m;
    text = String(text);
    while ((m = re.exec(text)) !== null) {
      if (m.index > last) { out.push(text.slice(last, m.index)); }
      if (m[1] !== undefined) {
        out.push({ t: m[1], c: 'code' });
      } else {
        var h = hrefFor(m[3], data);
        out.push(h ? { t: m[2], href: h } : m[2]);
      }
      last = re.lastIndex;
    }
    if (last < text.length) { out.push(text.slice(last)); }
    return out;
  }

  function link(text, url, data) {
    var h = hrefFor(url, data);
    return h ? { t: text, href: h } : text;
  }

  function err(text) { return [{ t: text, c: 'err' }]; }

  function tagSpan(f) {
    return { t: '[' + f.status_label + ']', c: 'tag ' + (f.status_class || 'dim') };
  }

  function statusList(data) {
    return Object.keys(data.status_counts || {});
  }

  function cmdHelp() {
    var lines = ['Commands', ''];
    HELP.forEach(function (h) { lines.push([{ t: h[0], c: 'cmd' }, '  ' + h[1]]); });
    lines.push('', 'Keys: arrows move, Enter opens, Tab switches pane, / jumps to the command line, Esc goes back, F1 to F4 are shortcuts for help, features, install and settings (F5 still reloads the page). While you type, matches are listed above the prompt: Tab or Right accepts, Up and Down choose, Esc closes.');
    return lines;
  }

  function actionData(data) { return (data && data.action && data.action.scenarios) || []; }

  // The scripted runs of the "in action" page, as text. Nothing here is measured: the page's data is a fixed script.
  function cmdAction(args, data) {
    var list = actionData(data);
    var more = [link('The animated version', 'action.html', data), ' has a diagram, Play and Step.'];
    if (!args.length) {
      var out = ['Simulated runs, not live traffic. action <name> gives the steps.', ''];
      list.forEach(function (s) { out.push([{ t: s.id, c: 'cmd' }, '  ' + s.title], ['    ' + s.summary]); });
      out.push('', more);
      return { lines: out };
    }
    var s = list.filter(function (x) { return x.id === args[0]; })[0];
    if (!s) { return { lines: [err('action: no scenario ' + args[0].slice(0, 40) + ', use ' + list.map(function (x) { return x.id; }).join(', '))] }; }
    var who = { A: 'player A', B: 'player B', S: 'server' };
    var lines = [[{ t: s.title, c: 'strong' }], [s.summary], ''];
    s.steps.forEach(function (st, i) {
      lines.push([(i + 1) + '. ' + who[st.from] + ' to ' + who[st.to] + '  ', st.message ? { t: st.message, c: 'code' } : '(game traffic)']);
      lines.push(['   ' + st.caption]);
    });
    lines.push('', more);
    return { lines: lines };
  }

  function cmdFeatures(args, data) {
    var status = null;
    var id = null;
    for (var i = 0; i < args.length; i++) {
      var a = args[i];
      if (a === '--status') {
        status = args[i + 1];
        i++;
        if (status === undefined) { return { lines: [err('--status needs a value: ' + statusList(data).join(', '))] }; }
      } else if (a.indexOf('--status=') === 0) {
        status = a.slice(9);
      } else if (a.charAt(0) === '-') {
        return { lines: [err('features: unknown option ' + a.slice(0, 40) + ', use --status ' + statusList(data).join('|'))] };
      } else if (id === null) {
        id = a;
      }
    }
    if (id !== null) {
      var f = data.features.filter(function (x) { return x.id === id; })[0];
      if (!f) { return { lines: [err('features: no feature with id ' + id.slice(0, 40) + ', run features to list them')] }; }
      var lines = [[{ t: f.feature, c: 'strong' }, ' ', { t: f.id, c: 'id' }], [tagSpan(f), ' since ' + (f.since || 'not released')], ''];
      if (f.detail) { lines.push(md(f.detail, data)); }
      lines.push('', ['Evidence: ' + (f.evidence || 'none listed')]);
      if (f.note) { lines.push(['Note: '].concat(md(f.note, data))); }
      lines.push('', [link('All evidence', 'docs/features.md', data)]);
      return { lines: lines };
    }
    if (status !== null && statusList(data).indexOf(status) < 0) {
      return { lines: [err('features: unknown status ' + String(status).slice(0, 40) + ', use ' + statusList(data).join(', '))] };
    }
    var rows = data.features.filter(function (x) { return status === null || x.status === status; });
    var out = [status === null ? rows.length + ' features. features <id> gives the detail.' : rows.length + ' features with status ' + status + '.'];
    var area = null;
    rows.forEach(function (r) {
      if (r.area !== area) { area = r.area; out.push('', [{ t: '# ' + area, c: 'head' }]); }
      out.push([tagSpan(r), ' ' + r.feature + ' ', { t: r.id, c: 'id' }]);
    });
    if (!rows.length) { out.push('Nothing with that status.'); }
    return { lines: out };
  }

  function cmdRoadmap(data) {
    var byId = {};
    data.features.forEach(function (f) { byId[f.id] = f; });
    var out = ['No dates are promised.'];
    Object.keys(data.roadmap).forEach(function (name) {
      var ids = data.roadmap[name];
      if (!ids.length) { return; }
      out.push('', [{ t: '# ' + name + ' (' + ids.length + ')', c: 'head' }]);
      ids.forEach(function (i) {
        var f = byId[i];
        out.push(['  ' + f.feature + ' ', { t: f.id, c: 'id' }].concat(f.status === 'partial' || f.status === 'unverified' ? [' ', tagSpan(f)] : []));
      });
    });
    out.push('', [link('ROADMAP.md has the order and the scope', 'ROADMAP.md', data)]);
    return out;
  }

  function settingLine(s) {
    return [{ t: s.flag, c: 'flag' }, '  ', { t: s.variable, c: 'flag' }, '  default ', { t: s.default, c: 'code' }];
  }

  function cmdSettings(args, data) {
    if (args.length) {
      var q = args[0].toLowerCase().replace(/^-+/, '');
      var hit = data.settings.filter(function (s) {
        return s.flag.toLowerCase().replace(/^-+/, '') === q || s.variable.toLowerCase() === q.toLowerCase();
      })[0];
      if (!hit) { return { lines: [err('settings: no setting named ' + args[0].slice(0, 40) + ', run settings to list them')] }; }
      return { lines: [settingLine(hit), ''].concat([md(hit.description, data)]) };
    }
    var out = [data.settings.length + ' flags; each is also a RE_PSN_<FLAG> variable. A flag wins over the variable. settings <name> shows one.'];
    data.settings.forEach(function (s) {
      out.push('', settingLine(s), '  ');
      out[out.length - 1] = ['  '].concat(md(s.description, data));
    });
    out.push('', [link('docs/running.md has the full text', 'docs/running.md', data)]);
    return { lines: out };
  }

  function cmdInstall(data) {
    var i = data.install;
    var out = [['Latest release: ', link(i.release_tag, i.release_url, data), ' (' + data.stage + ')'], '', [{ t: '# Release archives', c: 'head' }], md(i.archives, data)];
    i.verify_unix.forEach(function (l) { out.push([{ t: l, c: 'code' }]); });
    out.push(md(i.verify_windows, data), '', [{ t: '# Container', c: 'head' }]);
    i.container.forEach(function (l) { out.push([{ t: l, c: 'code' }]); });
    out.push(md(i.container_note, data), '', [{ t: '# From source', c: 'head' }], i.source_note);
    i.source.forEach(function (l) { out.push([{ t: l, c: 'code' }]); });
    return out;
  }

  function cmdConnect(data) {
    var c = data.connect;
    var out = [c.intro, ''];
    c.points.forEach(function (p) { out.push(['- '].concat(md(p, data))); });
    out.push('', md(c.client, data));
    return out;
  }

  function cmdChangelog(data) {
    var ch = data.changelog;
    var out = [];
    var u = ch.unreleased;
    if (u) {
      out.push([{ t: '# Unreleased', c: 'head' }]);
      Object.keys(u.sections).forEach(function (sec) {
        out.push('', sec);
        u.sections[sec].forEach(function (b) { out.push(['- '].concat(md(b, data))); });
      });
      out.push('');
    }
    var l = ch.latest;
    var counts = Object.keys(l.counts).map(function (k) { return l.counts[k] + ' under ' + k; }).join(', ');
    out.push([{ t: '# ' + l.version + (l.date ? ' (' + l.date + ')' : ''), c: 'head' }], counts ? counts + '.' : '');
    out.push('', [link('CHANGELOG.md has the full history', 'CHANGELOG.md', data)]);
    return out;
  }

  function cmdAbout(data) {
    var n = data.status_counts;
    var parts = Object.keys(n).filter(function (k) { return n[k]; }).map(function (k) { return n[k] + ' ' + k; });
    var out = [];
    data.about.paragraphs.forEach(function (p) { out.push(md(p, data), ''); });
    out.push('Version ' + data.version + ' (' + data.stage + '). ' + data.features.length + ' features: ' + parts.join(', ') + '.', '', data.about.notice);
    return out;
  }

  function cmdList(items, data) {
    return items.map(function (p) { return ['- '].concat(md(p, data)); });
  }

  function run(input, data, ctx) {
    ctx = ctx || {};
    var tokens = String(input).trim().split(/\s+/).filter(Boolean);
    if (!tokens.length) { return { lines: [], cmd: '' }; }
    var word = CANONICAL[tokens[0]] || tokens[0];
    var args = tokens.slice(1);
    var res;
    switch (word) {
      case 'help': res = { lines: cmdHelp() }; break;
      case 'features': res = cmdFeatures(args, data); break;
      case 'roadmap': res = { lines: cmdRoadmap(data) }; break;
      case 'settings': res = cmdSettings(args, data); break;
      case 'install': res = { lines: cmdInstall(data) }; break;
      case 'connect': res = { lines: cmdConnect(data) }; break;
      case 'action': res = cmdAction(args, data); break;
      case 'version':
        res = { lines: ['re-psn v' + data.version + ' (' + data.stage + ')', data.go ? 'Go ' + data.go + ' or newer to build.' : ''].filter(Boolean) };
        break;
      case 'changelog': res = { lines: cmdChangelog(data) }; break;
      case 'about': res = { lines: cmdAbout(data) }; break;
      case 'credits': res = { lines: cmdList(data.credits, data) }; break;
      case 'licence': res = { lines: [md(data.licence, data)] }; break;
      case 'docs':
        res = { lines: data.docs.map(function (d) { return [link(d.title, d.url, data), '  ' + d.summary]; }) };
        break;
      case 'links':
        res = { lines: data.links.map(function (l) { return [l.title + '  ', link(l.url, l.url, data)]; }) };
        break;
      case 'clear': res = { lines: [], clear: true }; break;
      case 'history':
        res = { lines: (ctx.history && ctx.history.length) ? ctx.history.map(function (h, i) { return (i + 1) + '  ' + h; }) : ['No history yet.'] };
        break;
      default:
        return { lines: [err('command not found: ' + tokens[0].slice(0, 60) + ', ' + HINT)], cmd: '', error: true };
    }
    res.cmd = word;
    var name = fileName(word, args, res);
    if (name) { res.file = name; }
    return res;
  }

  // The name in the header of an answer, like a file name: the command, and the item asked for. An answer that is only an error,
  // and clear, have none (they are not framed).
  function fileName(word, args, res) {
    if (!res.lines.length || (res.lines.length === 1 && isErrorLine(res.lines[0]))) { return null; }
    if (word === 'features') {
      var i = args.indexOf('--status');
      var st = i >= 0 ? args[i + 1] : (args.filter(function (a) { return a.indexOf('--status=') === 0; })[0] || '').slice(9);
      var id = args.filter(function (a, j) { return a.charAt(0) !== '-' && args[j - 1] !== '--status'; })[0];
      if (id) { return 'features/' + id; }
      if (st) { return 'features/' + st; }
    }
    if (word === 'action' && args.length) { return 'action/' + args[0]; }
    if (word === 'settings' && args.length) { return 'settings/' + args[0].replace(/^-+/, ''); }
    return word;
  }

  function isErrorLine(line) {
    return Array.isArray(line) && line.length === 1 && typeof line[0] === 'object' && line[0].c === 'err';
  }

  function commonPrefix(list) {
    var p = list[0];
    list.forEach(function (s) { while (s.indexOf(p) !== 0) { p = p.slice(0, -1); } });
    return p;
  }

  // Tab completion: {value, candidates}. value is the new text of the input.
  function complete(input, data) {
    var endsWithSpace = /\s$/.test(input);
    var tokens = input.split(/\s+/).filter(Boolean);
    var cur = endsWithSpace ? '' : (tokens.pop() || '');
    var head = tokens.join(' ');
    var pool;
    if (!tokens.length) {
      pool = COMMANDS;
    } else {
      var word = CANONICAL[tokens[0]] || tokens[0];
      var prev = tokens[tokens.length - 1];
      if (word === 'features') {
        pool = prev === '--status' ? statusList(data) : ['--status'].concat(data.features.map(function (f) { return f.id; }));
      } else if (word === 'settings') {
        pool = data.settings.map(function (s) { return s.flag; });
      } else if (word === 'action') {
        pool = tokens.length === 1 ? actionData(data).map(function (x) { return x.id; }) : [];
      } else {
        pool = [];
      }
    }
    var c = pool.filter(function (x) { return x.indexOf(cur) === 0; });
    if (!c.length) { return { value: input, candidates: [] }; }
    var pre = c.length === 1 ? c[0] + ' ' : commonPrefix(c);
    return { value: (head ? head + ' ' : '') + pre, candidates: c.length > 1 ? c : [] };
  }

  // Suggestions while typing: {ghost, items:[{text, replace, desc}]}. ghost is the rest of the best match when it starts with what is
  // typed (else ''); items are up to LIMIT matches, prefix matches first and then substring matches, case-insensitive. replace is the
  // whole new text of the input. Nothing is offered when the cursor is not at the end (pass its position as `cursor`), when the input
  // is empty, or when nothing matches. The page only draws what this returns.
  var LIMIT = 8;
  var CMD_DESC = {};
  HELP.forEach(function (h) {
    h[0].split(', ').forEach(function (n) { CMD_DESC[n.split(' ')[0]] = h[1]; });
  });
  CMD_DESC.license = CMD_DESC.licence;

  function plain(text) { return String(text).replace(/`/g, '').replace(/\[([^\]]+)\]\([^)\s]+\)/g, '$1').replace(/\s+/g, ' ').trim(); }

  function pick(pool, cur, head) {
    var q = cur.toLowerCase();
    var starts = [];
    var inside = [];
    pool.forEach(function (x) {
      var t = x.text.toLowerCase();
      if (t === q) { return; }
      if (t.indexOf(q) === 0) { starts.push(x); } else if (q && t.indexOf(q) > 0) { inside.push(x); }
    });
    var items = starts.concat(inside).slice(0, LIMIT).map(function (x) {
      return { text: x.text, replace: (head ? head + ' ' : '') + x.text + ' ', desc: x.desc || '' };
    });
    return { ghost: starts.length ? starts[0].text.slice(cur.length) : '', items: items };
  }

  function suggest(input, data, cursor) {
    var none = { ghost: '', items: [] };
    input = String(input);
    if (!input.trim() || (cursor !== undefined && cursor < input.length)) { return none; }
    var tokens = input.split(/\s+/).filter(Boolean);
    var cur = /\s$/.test(input) ? '' : tokens.pop();
    var head = tokens.join(' ');
    var pool = [];
    if (!tokens.length) {
      pool = COMMANDS.filter(function (c) { return c !== 'license'; }).map(function (c) { return { text: c, desc: CMD_DESC[c] }; });
    } else {
      var word = CANONICAL[tokens[0]] || tokens[0];
      var prev = tokens[tokens.length - 1];
      if (word === 'features') {
        if (prev === '--status') {
          pool = statusList(data).map(function (k) { return { text: k, desc: data.status_counts[k] + ' features' }; });
        } else {
          pool = [{ text: '--status', desc: 'only features with this status' }].concat(data.features.map(function (f) { return { text: f.id, desc: f.feature }; }));
        }
      } else if (word === 'action' && tokens.length === 1) {
        pool = actionData(data).map(function (x) { return { text: x.id, desc: x.title }; });
      } else if (word === 'settings' && tokens.length === 1) {
        pool = data.settings.map(function (s) { return { text: s.flag, desc: plain(s.description) }; });
      }
    }
    return pick(pool, cur, head);
  }

  // ------------------------------------------------------------------ the numbered file (pure)

  // The text of one output line (a string or a list of spans).
  function lineText(line) {
    return (Array.isArray(line) ? line : [line]).map(function (sp) { return typeof sp === 'string' ? sp : sp.t; }).join('');
  }

  // Digits in the gutter: enough for the last line number of the output, 1 to 4.
  function gutterWidth(total) {
    return Math.min(4, String(Math.max(1, Math.floor(total) || 1)).length);
  }

  // Colouring of the plain text of a line, like a syntax theme: option names, RE_PSN_ variables, numbers and versions, and a label
  // at the start of a line ("Evidence:"). Spans that already have a class (headings, ids, values, links) keep it.
  var TOKEN = /(^|[\s(\[,|])(--?[a-z][a-z0-9-]*)|\b(RE_PSN_(?:[A-Z0-9_]|<[A-Z]+>)+)|(\bv?\d+(?:[.:]\d+)*\b)/g;
  function classify(text, atStart) {
    var out = [];
    var last = 0;
    var m;
    text = String(text);
    var lab = atStart ? /^([A-Z][A-Za-z ]{0,24}:)(?= )/.exec(text) : null;
    if (lab) { out.push({ t: lab[1], c: 'label' }); last = lab[1].length; }
    TOKEN.lastIndex = last;
    while ((m = TOKEN.exec(text)) !== null) {
      var start = m.index + (m[1] ? m[1].length : 0);
      if (start > last) { out.push(text.slice(last, start)); }
      if (m[2]) {
        out.push({ t: m[2], c: 'opt' });
      } else if (m[3]) {
        out.push({ t: m[3], c: 'env' });
      } else {
        out.push({ t: m[4], c: 'num' });
      }
      last = TOKEN.lastIndex;
    }
    if (last < text.length) { out.push(text.slice(last)); }
    return out;
  }

  // ---- drawing, given a document (the page's, or a small stand-in in the tests). Text only through textContent and text nodes.

  function node(doc, tag, cls, text) {
    var n = doc.createElement(tag);
    if (cls) { n.className = cls; }
    if (text !== undefined) { n.textContent = text; }
    return n;
  }

  function spansInto(doc, parent, line, colour) {
    (Array.isArray(line) ? line : [line]).forEach(function (sp, i) {
      if (typeof sp === 'string') {
        (colour ? classify(sp, i === 0) : [sp]).forEach(function (x) {
          parent.appendChild(typeof x === 'string' ? doc.createTextNode(x) : node(doc, 'span', x.c, x.t));
        });
        return;
      }
      var h = sp.href ? hrefFor(sp.href, null) : null;
      var n;
      if (h) {
        n = node(doc, 'a', sp.c, sp.t);
        n.setAttribute('href', h);
        n.setAttribute('rel', 'noopener noreferrer');
      } else {
        n = node(doc, 'span', sp.c, sp.t);
      }
      parent.appendChild(n);
    });
    return parent;
  }

  // One line without a frame (the command echo, an error, the Tab candidates).
  function renderLine(doc, line) { return spansInto(doc, node(doc, 'div', 'ln'), line, false); }

  // The numbered file: a header with the name, then one row per line, a gutter cell and the text. A gutter cell holds no text: it
  // is hidden from assistive technology and not selectable, and the stylesheet draws the number from data-n, so the number is never
  // part of the text that is read or copied. A line that wraps keeps its one gutter cell, so the rows it wraps onto have an empty
  // gutter. opts: {name, lines}.
  function renderFile(doc, opts) {
    var lines = opts.lines;
    var f = node(doc, 'div', 'file gw' + gutterWidth(lines.length));
    f.setAttribute('role', 'group');
    f.setAttribute('aria-label', opts.name + ', ' + lines.length + (lines.length === 1 ? ' line' : ' lines'));
    var head = node(doc, 'div', 'file-head');
    head.setAttribute('aria-hidden', 'true');
    head.appendChild(node(doc, 'span', 'gut'));
    var hn = node(doc, 'span', 'tx');
    hn.appendChild(node(doc, 'span', 'file-label', 'File: '));
    hn.appendChild(node(doc, 'span', 'file-name', opts.name));
    head.appendChild(hn);
    f.appendChild(head);
    var body = node(doc, 'div', 'file-body');
    for (var i = 0; i < lines.length; i++) {
      var row = node(doc, 'div', 'fl');
      var g = node(doc, 'span', 'gut');
      g.setAttribute('aria-hidden', 'true');
      g.setAttribute('data-n', String(i + 1));
      row.appendChild(g);
      row.appendChild(spansInto(doc, node(doc, 'span', 'tx'), lines[i], true));
      body.appendChild(row);
    }
    f.appendChild(body);
    return f;
  }

  var api = { run: run, complete: complete, suggest: suggest, md: md, hrefFor: hrefFor, COMMANDS: COMMANDS, HINT: HINT,
    lineText: lineText, gutterWidth: gutterWidth,
    classify: classify,
    renderLine: renderLine, renderFile: renderFile };
  if (typeof module !== 'undefined' && module.exports) { module.exports = api; }
  root.rePsnTerminal = api;

  // ------------------------------------------------------------------ the page

  if (typeof document === 'undefined' || typeof fetch !== 'function') { return; }

  var MENU = [['about', 'Overview'], ['features', 'Features'], ['roadmap', 'Roadmap'], ['install', 'Install'],
    ['settings', 'Settings'], ['connect', 'Connect'], ['action', 'In action'], ['changelog', 'Changelog'], ['docs', 'Docs']];
  var FKEYS = { F1: 'help', F2: 'features', F3: 'install', F4: 'settings', F5: 'connect' };

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) { n.className = cls; }
    if (text !== undefined) { n.textContent = text; }
    return n;
  }

  // The site has one theme. A choice stored by an earlier version of this page is removed.
  try { root.localStorage.removeItem('re-psn-theme'); } catch (e) { /* storage is optional */ }
  document.documentElement.classList.add('js');

  function mount(data) {
    var history = [];
    var histPos = 0;
    var current = '';

    var tui = el('div', 'tui');
    tui.id = 'tui';
    tui.setAttribute('data-view', 'content');

    var title = el('div', 'tui-title');
    title.appendChild(el('span', 'tui-name', 're:psn'));
    title.appendChild(el('span', 'tui-ver', 'v' + data.version + ' · ' + data.stage));
    var plain = el('button', 'tui-plain', 'plain view');
    plain.type = 'button';
    title.appendChild(plain);
    tui.appendChild(title);

    var fk = el('ul', 'tui-fkeys');
    fk.setAttribute('aria-label', 'Shortcuts');
    Object.keys(FKEYS).forEach(function (k) {
      var li = el('li');
      var b = el('button', 'fk', k === 'F5' ? '[' + FKEYS[k] + ']' : '[' + k + ' ' + FKEYS[k] + ']');
      b.type = 'button';
      b.setAttribute('data-cmd', FKEYS[k]);
      li.appendChild(b);
      fk.appendChild(li);
    });
    tui.appendChild(fk);

    var panes = el('div', 'tui-panes');
    var nav = el('nav', 'tui-menu');
    nav.setAttribute('aria-label', 'Sections');
    var ul = el('ul');
    var menuBtns = MENU.map(function (m) {
      var li = el('li');
      var b = el('button', 'mi', m[1]);
      b.type = 'button';
      b.setAttribute('data-cmd', m[0]);
      li.appendChild(b);
      ul.appendChild(li);
      return b;
    });
    nav.appendChild(ul);
    panes.appendChild(nav);

    var content = el('div', 'tui-content');
    var back = el('button', 'chip tui-back', '← Back to menu');
    back.type = 'button';
    content.appendChild(back);
    var out = el('div', 'tui-out');
    out.setAttribute('role', 'region');
    out.setAttribute('aria-label', 'Output');
    out.setAttribute('aria-live', 'polite');
    out.tabIndex = 0;
    content.appendChild(out);

    panes.appendChild(content);
    tui.appendChild(panes);

    var form = el('form', 'tui-cmd');
    form.setAttribute('autocomplete', 'off');
    form.setAttribute('action', '#');
    var hint = el('p', 'tui-hint', HINT);
    hint.id = 'tui-hint';
    form.appendChild(hint);
    var row = el('div', 'tui-row');
    var ps = el('span', 'tui-ps', '$');
    ps.setAttribute('aria-hidden', 'true');
    var label = el('label', 'vh', 'Command');
    label.setAttribute('for', 'tui-input');
    var input = el('input');
    input.id = 'tui-input';
    input.type = 'text';
    input.maxLength = 200;
    input.spellcheck = false;
    input.setAttribute('autocapitalize', 'off');
    input.setAttribute('autocorrect', 'off');
    input.setAttribute('autocomplete', 'off');
    input.setAttribute('aria-describedby', 'tui-hint');
    input.setAttribute('role', 'combobox');
    input.setAttribute('aria-autocomplete', 'both');
    input.setAttribute('aria-expanded', 'false');
    input.setAttribute('aria-controls', 'tui-list');
    // The input sits in a field with the ghost text behind it: the typed text (invisible) and the rest of the best match (dimmed).
    var field = el('div', 'tui-field');
    var ghost = el('span', 'tui-ghost');
    ghost.setAttribute('aria-hidden', 'true');
    var ghostPad = el('span', 'tui-ghost-pad');
    var ghostRest = el('span', 'tui-ghost-rest');
    ghost.appendChild(ghostPad);
    ghost.appendChild(ghostRest);
    field.appendChild(ghost);
    field.appendChild(input);
    var cursor = el('span', 'cursor', '█');
    cursor.setAttribute('aria-hidden', 'true');
    row.appendChild(ps);
    row.appendChild(label);
    row.appendChild(field);
    row.appendChild(cursor);
    var list = el('ul', 'tui-list');
    list.id = 'tui-list';
    list.setAttribute('role', 'listbox');
    list.setAttribute('aria-label', 'Suggestions');
    list.hidden = true;
    row.appendChild(list);
    form.appendChild(row);
    var status = el('div', 'vh');
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    form.appendChild(status);
    tui.appendChild(form);

    var chips = el('ul', 'tui-chips');
    chips.setAttribute('aria-label', 'Commands');
    ['help', 'features', 'roadmap', 'settings', 'install', 'connect', 'changelog', 'docs', 'links'].forEach(function (c) {
      var li = el('li');
      var b = el('button', 'chip', c);
      b.type = 'button';
      b.setAttribute('data-cmd', c);
      li.appendChild(b);
      chips.appendChild(li);
    });
    tui.appendChild(chips);

    tui.appendChild(el('p', 'tui-keys', '↑↓ move · Enter open · Tab switch pane · / command · Esc back'));

    var toTui = el('div', 'plain-bar');
    var tb = el('button', 'tui-plain', 'terminal view');
    tb.type = 'button';
    toTui.appendChild(tb);

    function setView(v) { tui.setAttribute('data-view', v); }

    // The answer drawn as a numbered file.
    function drawFile(res) {
      out.appendChild(renderFile(document, { name: res.file, lines: res.lines }));
    }

    function paint(res, echo) {
      out.textContent = '';
      out.setAttribute('aria-live', 'polite');
      if (res.clear) { return; }
      if (echo) { out.appendChild(renderLine(document, [{ t: '$ ', c: 'ps' }, { t: echo, c: 'echo' }])); }
      if (res.file) {
        drawFile(res);
      } else {
        res.lines.forEach(function (l) { out.appendChild(renderLine(document, l)); });
      }
      out.scrollTop = 0;
    }

    function mark(cmd) {
      menuBtns.forEach(function (b) {
        if (b.getAttribute('data-cmd') === cmd) { b.setAttribute('aria-current', 'page'); } else { b.removeAttribute('aria-current'); }
      });
    }

    function exec(text, focusOut) {
      var t = String(text).trim();
      if (!t) { return; }
      if (history[history.length - 1] !== t) { history.push(t); }
      histPos = history.length;
      var res = run(t, data, { history: history });
      paint(res, t);
      if (res.cmd) { current = res.cmd; mark(current); }
      setView('content');
      if (focusOut) { out.focus(); }
    }

    function menuFocus() {
      var b = menuBtns.filter(function (x) { return x.getAttribute('aria-current') === 'page'; })[0] || menuBtns[0];
      setView('menu');
      b.focus();
    }

    function syncCursor() {
      var empty = input.value === '';
      tui.classList.toggle('empty', empty);
    }

    // Autocomplete: suggest() decides, this only draws. sel is the item chosen with the arrows (-1 for none).
    var sug = { ghost: '', items: [] };
    var sel = -1;
    var shown = false;
    var announce = 0;

    function say(text) {
      root.clearTimeout(announce);
      announce = root.setTimeout(function () { status.textContent = text; }, 600);
    }

    function drawGhost() {
      var fits = sug.ghost && input.scrollWidth <= input.clientWidth && sel < 0;
      ghostPad.textContent = fits ? input.value : '';
      ghostRest.textContent = fits ? sug.ghost : '';
    }

    function drawList() {
      list.textContent = '';
      sug.items.forEach(function (it, i) {
        var li = el('li', 'ac-item');
        li.id = 'tui-opt-' + i;
        li.setAttribute('role', 'option');
        li.setAttribute('aria-selected', i === sel ? 'true' : 'false');
        li.setAttribute('data-i', String(i));
        li.appendChild(el('span', 'ac-name', it.text));
        if (it.desc) { li.appendChild(el('span', 'ac-desc', it.desc)); }
        list.appendChild(li);
      });
      list.hidden = !shown;
      input.setAttribute('aria-expanded', shown ? 'true' : 'false');
      if (shown && sel >= 0) { input.setAttribute('aria-activedescendant', 'tui-opt-' + sel); } else { input.removeAttribute('aria-activedescendant'); }
      drawGhost();
    }

    function closeList() {
      shown = false;
      sel = -1;
      sug = { ghost: '', items: [] };
      drawList();
      say('');
    }

    function refresh() {
      var atEnd = input.selectionStart === input.value.length && input.selectionEnd === input.value.length;
      sug = atEnd && document.activeElement === input ? suggest(input.value, data) : { ghost: '', items: [] };
      sel = -1;
      var was = shown;
      shown = sug.items.length > 0;
      drawList();
      if (shown) { say(sug.items.length + (sug.items.length === 1 ? ' suggestion' : ' suggestions')); } else if (was) { say(''); }
    }

    function accept(i) {
      var it = sug.items[i];
      if (!it) { return false; }
      input.value = it.replace;
      input.setSelectionRange(input.value.length, input.value.length);
      syncCursor();
      refresh();
      return true;
    }

    tui.addEventListener('click', function (e) {
      var b = e.target.closest ? e.target.closest('button[data-cmd]') : null;
      if (b && tui.contains(b)) { exec(b.getAttribute('data-cmd'), true); }
    });
    back.addEventListener('click', menuFocus);
    plain.addEventListener('click', function () {
      document.documentElement.classList.add('plain');
      tb.focus();
    });
    tb.addEventListener('click', function () {
      document.documentElement.classList.remove('plain');
      input.focus();
    });

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var v = input.value;
      input.value = '';
      syncCursor();
      closeList();
      exec(v, false);
    });

    input.addEventListener('input', function () { syncCursor(); refresh(); });
    input.addEventListener('focus', refresh);
    input.addEventListener('blur', closeList);
    input.addEventListener('click', refresh);
    input.addEventListener('keyup', function (e) {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'Home' || e.key === 'End') { refresh(); }
    });
    // A tap or click on a suggestion accepts it; the mousedown is stopped so the input keeps its focus (and the phone keyboard).
    list.addEventListener('mousedown', function (e) { e.preventDefault(); });
    list.addEventListener('click', function (e) {
      var li = e.target.closest ? e.target.closest('li[data-i]') : null;
      if (li) { accept(Number(li.getAttribute('data-i'))); input.focus(); }
    });
    input.addEventListener('keydown', function (e) {
      if (e.altKey || e.ctrlKey || e.metaKey) { return; }
      var atEnd = input.selectionStart === input.value.length && input.selectionEnd === input.value.length;
      if (shown && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
        e.preventDefault();
        var n = sug.items.length;
        sel = e.key === 'ArrowDown' ? (sel + 1) % n : (sel < 0 ? n - 1 : (sel + n - 1) % n);
        drawList();
      } else if (shown && e.key === 'Enter' && sel >= 0) {
        e.preventDefault();
        accept(sel);
      } else if (shown && e.key === 'ArrowRight' && atEnd && (sel >= 0 || sug.ghost)) {
        e.preventDefault();
        accept(sel >= 0 ? sel : 0);
      } else if (shown && e.key === 'Tab' && !e.shiftKey && (sel >= 0 || sug.ghost)) {
        e.preventDefault();
        accept(sel >= 0 ? sel : 0);
      } else if (shown && e.key === 'Escape') {
        e.preventDefault();
        closeList();
      } else if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
        if (!history.length) { return; }
        e.preventDefault();
        histPos += e.key === 'ArrowUp' ? -1 : 1;
        histPos = Math.max(0, Math.min(history.length, histPos));
        input.value = history[histPos] || '';
        syncCursor();
        closeList();
      } else if (e.key === 'Tab' && !e.shiftKey && input.value.trim() !== '') {
        var c = complete(input.value, data);
        if (c.value !== input.value || c.candidates.length) {
          e.preventDefault();
          input.value = c.value;
          syncCursor();
          refresh();
          if (c.candidates.length) { paint({ lines: [c.candidates.join('  ')] }, null); }
        }
      } else if (e.key === 'Escape') {
        e.preventDefault();
        menuFocus();
      }
    });

    nav.addEventListener('keydown', function (e) {
      var i = menuBtns.indexOf(document.activeElement);
      if (i < 0 || e.altKey || e.ctrlKey || e.metaKey) { return; }
      var n = null;
      if (e.key === 'ArrowDown') { n = (i + 1) % menuBtns.length; }
      if (e.key === 'ArrowUp') { n = (i + menuBtns.length - 1) % menuBtns.length; }
      if (e.key === 'Home') { n = 0; }
      if (e.key === 'End') { n = menuBtns.length - 1; }
      if (n !== null) { e.preventDefault(); menuBtns[n].focus(); }
    });

    document.addEventListener('keydown', function (e) {
      if (e.altKey || e.ctrlKey || e.metaKey) { return; }
      if (document.documentElement.classList.contains('plain')) { return; }
      var t = e.target;
      var typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable);
      // Leave selection shortcuts to the browser.
      if (e.shiftKey) { return; }
      if (FKEYS[e.key] && e.key !== 'F5') { e.preventDefault(); exec(FKEYS[e.key], false); return; }
      if (e.key === '/' && !typing) { e.preventDefault(); input.focus(); }
      if (e.key === 'Escape' && !typing) { menuFocus(); }
    });

    // The terminal view goes right under the banner (after the skip link when there is none).
    var anchor = document.querySelector('.banner') || document.querySelector('.skip');
    document.body.insertBefore(toTui, anchor ? anchor.nextSibling : document.body.firstChild);
    toTui.parentNode.insertBefore(tui, toTui.nextSibling);
    document.documentElement.classList.add('tui-on');
    syncCursor();
    paint(run('about', data, {}), null);
    current = 'about';
    mark(current);
    // On a phone the plain page is the better start; the "terminal view" button switches the interface on (nothing is stored).
    if (root.matchMedia && root.matchMedia('(max-width: 44rem)').matches) { document.documentElement.classList.add('plain'); }
  }

  function start() {
    fetch('data.json', { credentials: 'same-origin' })
      .then(function (r) { if (!r.ok) { throw new Error('data.json'); } return r.json(); })
      .then(mount)
      .catch(function () { /* the plain page stays as it is */ });
  }

  if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', start); } else { start(); }
})(typeof window !== 'undefined' ? window : globalThis);
