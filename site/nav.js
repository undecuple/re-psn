/* re-psn project site: the way back from the pages that stand apart from the main page (in action, discussion).

   Every such page has a "Main page" link that needs no script. The "Previous page" button is hidden until this script finds that the
   reader came here from another page of this site; pressing it goes back one step in the browser's history. Nothing is requested,
   stored or sent. cameFromSite is pure and tested with `node --test tools/nav.test.js`. */
(function (root) {
  'use strict';

  // True when `referrer` is a page of the same site as `here` and not this very page.
  function cameFromSite(referrer, here) {
    if (typeof referrer !== 'string' || referrer === '' || typeof here !== 'string') { return false; }
    try {
      var r = new URL(referrer);
      var h = new URL(here);
      return r.origin === h.origin && r.origin !== 'null' && (r.pathname !== h.pathname || r.search !== h.search);
    } catch (e) {
      return false;
    }
  }

  var api = { cameFromSite: cameFromSite };
  if (typeof module !== 'undefined' && module.exports) { module.exports = api; }
  if (typeof document === 'undefined') { return; }

  var buttons = document.querySelectorAll('.prev-page');
  if (!buttons.length || !cameFromSite(document.referrer, document.location.href)) { return; }
  Array.prototype.forEach.call(buttons, function (b) {
    b.hidden = false;
    b.addEventListener('click', function () { root.history.back(); });
  });
})(this);
