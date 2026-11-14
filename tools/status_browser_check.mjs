// Optional status UI acceptance check; use only against an owned loopback fixture.
// node tools/status_browser_check.mjs DEBUG_PORT FIXTURE_URL ARTIFACT_DIRECTORY
import {mkdir, writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {connect} from './site_browser_cdp.mjs';
import theme from '../server/status.js';
import framing from './art/status-frame.js';

const [port, url, directory] = process.argv.slice(2);
if (new URL(url).hostname !== '127.0.0.1') throw new Error('An owned loopback fixture is required');
await mkdir(directory, {recursive:true});
const c = await connect(Number(port));
const evaluate = c.evaluate;
let assertions = 0;
function ok(value, message) { assertions++; if (!value) throw new Error(message); }
const click = selector => evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);
const screenshot = name => c.screenshot(resolve(directory, name));
async function navigate() {
  const previous = await evaluate('performance.timeOrigin');
  await c.call('Page.navigate', {url});
  await c.waitFor(`performance.timeOrigin !== ${previous} && document.readyState === 'complete'`);
  await c.waitFor("!document.querySelector('#toolbar').hidden");
}
async function viewport(width, height) {
  await c.call('Emulation.setDeviceMetricsOverride', {width,height,deviceScaleFactor:1,mobile:false});
}
const fits = () => evaluate('document.documentElement.scrollWidth <= innerWidth + 1');
async function contrast() {
  const colours = await evaluate(`(()=>{
    const result={};
    for(const slot of ['page','surface','text','muted','accent','good','warn','bad']){
      const e=document.createElement('span');e.style.color='var(--'+slot+')';document.body.append(e);
      result[slot]='#'+getComputedStyle(e).color.match(/\\d+/g).slice(0,3).map(x=>Number(x).toString(16).padStart(2,'0')).join('');e.remove();
    }return result;
  })()`);
  ok(!theme.colourError(colours), 'semantic colours meet 4.5:1 contrast: '+JSON.stringify(colours));
}
try {
  await viewport(1200, 800);
  await c.call('Emulation.setEmulatedMedia', {features:[{name:'prefers-color-scheme',value:'dark'}]});
  await navigate();
  await evaluate('localStorage.clear()');
  await navigate();
  ok(await evaluate("getComputedStyle(document.documentElement).backgroundColor === 'rgb(16, 17, 19)'"), 'project dark palette');
  ok(await evaluate("document.title === 're:psn · server status'"), 'updated title');
  ok(await evaluate("document.querySelector('#rooms-body').textContent.includes('alice')"), 'real loopback room is rendered');
  ok(await evaluate("document.querySelector('#boards-body').textContent.includes('100')"), 'real loopback score is rendered');
  ok(await evaluate("document.querySelector('#boards-panel').hidden"), 'rooms tab starts selected');
  ok(await fits(), 'desktop fits');
  ok(await evaluate("document.querySelector('#host-view').hidden"), 'Play hides operator counters');
  await click('button[data-role="host"]');
  ok(await evaluate("!document.querySelector('#host-view').hidden && document.querySelector('#status-tabs').hidden"), 'Host focuses real server observation');
  ok(await evaluate("document.querySelector('#host-view').textContent.includes('read-only') || document.querySelector('#host-view').textContent.includes('administrative')"), 'Host grants no administration');
  await navigate();
  ok(await evaluate("document.querySelector('button[data-role=host]').getAttribute('aria-pressed')==='true'"), 'Host view preference survives reload');
  await click('button[data-role="both"]');
  ok(await evaluate("!document.querySelector('#host-view').hidden && !document.querySelector('#status-tabs').hidden"), 'Both shows player views and host counters');
  await click('button[data-role="play"]');

  await contrast();
  await screenshot('desktop-dark.png');
  const readmeFrame = await framing.prepareStatusScreenshot({evaluate, viewport});
  ok(readmeFrame.width === 1000, 'README capture matches the original width');
  ok(await evaluate("!document.querySelector('#rooms-panel').hidden && !document.querySelector('#boards-panel').hidden"), 'Overview shows both real status tables');
  ok(await evaluate("document.querySelector('#rooms-body').textContent.includes('alice') && document.querySelector('#boards-body').textContent.includes('alice') && document.querySelector('#boards-body').textContent.includes('100')"), 'README capture includes the room and leading score');
  await screenshot('readme-status.png');
  await writeFile(resolve(directory, 'readme-frame.json'), JSON.stringify(readmeFrame, null, 2)+'\n');
  await evaluate("document.querySelector('#overview-tab').focus()");
  await c.key('ArrowLeft');
  ok(await evaluate("document.activeElement.id === 'boards-tab' && !document.querySelector('#boards-view').hidden"), 'tab keys wrap from Overview to Leaderboards');
  await c.key('Home');
  ok(await evaluate("document.activeElement.id === 'overview-tab' && !document.querySelector('#overview-view').hidden"), 'Home selects Overview');
  await click('#rooms-tab');
  await viewport(1200, 800);
  await click('#boards-tab');
  ok(await evaluate("!document.querySelector('#boards-panel').hidden && document.querySelector('#rooms-panel').hidden"), 'leaderboards tab switches panel');
  await evaluate("document.querySelector('#boards-tab').focus()");
  await c.key('ArrowLeft');
  ok(await evaluate("document.activeElement.id === 'rooms-tab' && !document.querySelector('#rooms-panel').hidden"), 'arrow keys select and focus tabs');
  await click('button[data-theme="light"]');
  ok(await evaluate("getComputedStyle(document.documentElement).backgroundColor === 'rgb(251, 241, 199)'"), 'project light palette');
  await contrast();
  await screenshot('desktop-light.png');
  await navigate();
  ok(await evaluate("document.documentElement.dataset.theme === 'light'"), 'choice survives reload');
  await click('button[data-theme="system"]');
  ok(await evaluate("!document.documentElement.hasAttribute('data-theme')"), 'system clears forced mode');
  await c.call('Emulation.setEmulatedMedia', {features:[{name:'prefers-color-scheme',value:'light'}]});
  await c.waitFor("getComputedStyle(document.documentElement).backgroundColor === 'rgb(251, 241, 199)'");
  ok(true, 'system follows a running browser change');
  await click('button[data-theme="dark"]');
  await evaluate("document.querySelector('#appearance').open=true; document.querySelector('#colour-text').value='#282828';document.querySelector('#colours').requestSubmit()");
  ok(await evaluate("document.querySelector('#appearance-message').textContent.includes('4.5:1')"), 'unreadable custom colour rejected');
  ok(await evaluate("getComputedStyle(document.documentElement).color === 'rgb(159, 158, 153)'"), 'failed custom change retains good theme');
  await evaluate("document.querySelector('#colour-text').value='#ffffff';document.querySelector('#colour-page').value='#181818';document.querySelector('#colours').requestSubmit()");
  ok(await evaluate("getComputedStyle(document.documentElement).backgroundColor === 'rgb(24, 24, 24)' && getComputedStyle(document.documentElement).color === 'rgb(255, 255, 255)'"), 'valid custom colours apply under CSP');
  await navigate();
  ok(await evaluate("getComputedStyle(document.documentElement).backgroundColor === 'rgb(24, 24, 24)'"), 'custom theme survives reload');
  await click('button[data-theme="light"]');
  ok(await evaluate("getComputedStyle(document.documentElement).backgroundColor === 'rgb(251, 241, 199)'"), 'custom dark colours do not leak into light mode');
  await click('button[data-theme="dark"]');
  ok(await evaluate("getComputedStyle(document.documentElement).backgroundColor === 'rgb(24, 24, 24)'"), 'custom dark colours retained across mode changes');
  await evaluate("document.querySelector('#appearance').open=true;document.querySelector('#density').value='compact';document.querySelector('#density').dispatchEvent(new Event('change'));document.querySelector('#text-size').value='18';document.querySelector('#text-size').dispatchEvent(new Event('change'))");
  await navigate();
  ok(await evaluate("document.documentElement.dataset.density === 'compact' && getComputedStyle(document.documentElement).fontSize === '18px'"), 'spacing and size persist');
  await click('#refresh');
  await c.waitFor("document.querySelector('#refresh-state').textContent.startsWith('Updated ')");
  ok(true, 'authenticated live refresh works');
  ok(await evaluate("!localStorage.getItem('re-psn-status-appearance-v1').includes('synthetic-status-token')"), 'token is not stored with preferences');
  await click('#pause');
  ok(await evaluate("document.querySelector('#pause').getAttribute('aria-pressed') === 'true'"), 'automatic refresh can pause');
  // Failure retains the last rendered snapshot; no fabricated zero counts.
  await evaluate("window.originalFetch=window.fetch;window.fetch=async()=>({ok:false,status:401})");
  await click('#refresh');
  await c.waitFor("document.querySelector('#refresh-state').hasAttribute('data-error')");
  ok(await evaluate("document.querySelector('#rooms-body').textContent.includes('alice')"), 'unauthorized refresh retains previous snapshot');
  await evaluate(`window.fetch=async()=>({ok:true,text:async()=>' {"uptime":"1m","clients":1,"connections_total":2,"unlisted_rooms":0,"rooms":[],"boards":[{"title":"test","board":1,"entries":1,"leader":"alice","best":9223372036854775807},{"title":"test","board":2,"entries":1,"leader":"bob","best":-9223372036854775808}]} '})`);
  await click('#refresh');
  await c.waitFor("!document.querySelector('#refresh-state').hasAttribute('data-error')");
  ok(await evaluate("document.querySelector('#boards-body').textContent.includes('9223372036854775807')"), 'polling preserves maximum signed 64-bit score');
  ok(await evaluate("document.querySelector('#boards-body').textContent.includes('-9223372036854775808')"), 'polling preserves minimum signed 64-bit score');
  await evaluate(`window.fetch=async()=>({ok:true,text:async()=>JSON.stringify({uptime:'1m',clients:1,connections_total:2,unlisted_rooms:1,rooms:[{id:1,title:'<img src=x onerror=alert(1)>',members:['<svg onload=alert(1)>'],max:2,age:'1m'}],boards:[]})})`);
  await click('#refresh');
  await c.waitFor("!document.querySelector('#refresh-state').hasAttribute('data-error')");
  ok(await evaluate("!document.querySelector('#rooms-body img, #rooms-body svg') && document.querySelector('#rooms-body').textContent.includes('<img')"), 'polled names render as text');
  await evaluate("window.fetch=async()=>({ok:true,text:async()=>JSON.stringify({uptime:'1m',clients:0,connections_total:2,unlisted_rooms:0,rooms:[],boards:[]})})");
  await click('#refresh');
  await c.waitFor("!!document.querySelector('#rooms-body .empty')");
  ok(await evaluate("document.querySelector('#boards-body').textContent.includes('No leaderboards')"), 'empty tables explain state');
  await evaluate("window.fetch=window.originalFetch;document.querySelector('#appearance').open=true");
  await click('#reset');
  ok(await evaluate("document.querySelector('#text-size').value === '16' && document.querySelector('#density').value === 'comfortable'"), 'reset restores defaults');
  await click('#refresh');
  await c.waitFor("document.querySelector('#rooms-body').textContent.includes('alice')");
  await evaluate("document.querySelector('#appearance').open=false");
  await click('button[data-theme="dark"]');
  await viewport(390, 844);
  ok(await fits(), 'phone fits viewport');
  await screenshot('mobile-dark.png');
  await evaluate("document.querySelector('#appearance').open=true");
  await viewport(320, 740);
  ok(await fits(), '320px appearance controls fit');
  await evaluate("document.querySelector('#text-size').value='18';document.querySelector('#text-size').dispatchEvent(new Event('change'))");
  ok(await fits(), 'large text at 320px has no page overflow');
  await c.call('Page.addScriptToEvaluateOnNewDocument', {source:"Storage.prototype.getItem=function(){throw new Error('blocked')};Storage.prototype.setItem=function(){throw new Error('blocked')}"});
  await navigate();
  await click('button[data-theme="light"]');
  ok(await evaluate("document.documentElement.dataset.theme === 'light'"), 'blocked storage still allows appearance changes');
  ok(await evaluate("document.querySelector('#appearance-message').textContent.includes('unavailable')"), 'blocked storage is explained');
  await c.call('Emulation.setScriptExecutionDisabled', {value:true});
  await c.call('Page.navigate', {url});
  await c.waitFor("document.readyState === 'complete' && !!document.querySelector('#fallback-refresh')");
  ok(await evaluate("!document.querySelector('#rooms-panel').hidden && !document.querySelector('#boards-panel').hidden"), 'without JavaScript both tables remain');
  ok(await evaluate("document.querySelector('#rooms-body').textContent.includes('alice')"), 'without JavaScript server renders real data');
  const errors = c.events.filter(e => e.method === 'Runtime.exceptionThrown');
  const remote = c.events.filter(e => e.method === 'Network.requestWillBeSent' && /^https?:/.test(e.params.request.url) && new URL(e.params.request.url).origin !== new URL(url).origin);
  const policy = c.events.filter(e => e.method === 'Network.loadingFailed' && e.params.blockedReason === 'csp');
  ok(!policy.length, 'CSP permits only the intended UI resources');
  ok(!errors.length, 'no uncaught browser exceptions');
  ok(!remote.length, 'no external network requests');
  const result = {assertions, errors:errors.length, remoteRequests:remote.length};
  await writeFile(resolve(directory, 'browser-result.json'), JSON.stringify(result, null, 2)+'\n');
  console.log(JSON.stringify(result));
} finally { await c.close(); }
