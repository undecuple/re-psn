/* re-psn project site: the discussion page (site/discuss.html).

   Nothing is requested from another site when the page loads. When the reader presses the button, this script adds one script element
   for the giscus client (https://giscus.app/client.js), which draws a frame of a GitHub Discussions thread. The settings come from
   data-giscus-* attributes of the button, and only the names listed in ATTRS are passed on. The address of the client is fixed here,
   not read from the page. This is the one script of the site that loads anything from another site; the page's CSP allows
   giscus.app for scripts, styles and frames, and no other page's does.

   attrsFor is pure and tested with `node --test tools/discuss.test.js`. */
(function (root) {
  'use strict';

  var CLIENT = 'https://giscus.app/client.js';
  var ATTRS = ['repo', 'repo-id', 'category', 'category-id', 'mapping', 'term', 'strict', 'reactions-enabled', 'emit-metadata',
    'input-position', 'theme', 'lang', 'loading'];

  // [[name, value], ...] for the names of ATTRS that `get` (name -> string or null) gives a short, plain value for.
  function attrsFor(get) {
    var out = [];
    ATTRS.forEach(function (name) {
      var v = get(name);
      if (typeof v === 'string' && v.length > 0 && v.length <= 100 && /^[A-Za-z0-9_.\/ :-]+$/.test(v)) { out.push([name, v]); }
    });
    return out;
  }

  var api = { attrsFor: attrsFor, CLIENT: CLIENT, ATTRS: ATTRS };
  if (typeof module !== 'undefined' && module.exports) { module.exports = api; }
  if (typeof document === 'undefined') { return; }

  var button = document.getElementById('discuss-load');
  if (!button) { return; }
  button.addEventListener('click', function () {
    var s = document.createElement('script');
    s.src = CLIENT;
    s.async = true;
    s.crossOrigin = 'anonymous';
    attrsFor(function (name) { return button.getAttribute('data-giscus-' + name); }).forEach(function (a) {
      s.setAttribute('data-' + a[0], a[1]);
    });
    button.disabled = true;
    button.textContent = 'Loading the discussion';
    document.body.appendChild(s);
  });
})(this);
