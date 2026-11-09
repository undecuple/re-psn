// Optional browser acceptance check. See docs/website.md for local-only setup.
import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve, sep} from 'node:path';
import {pathToFileURL} from 'node:url';
import {connect} from './site_browser_cdp.mjs';
const port = Number(process.argv[2]);
if (!Number.isInteger(port) || port < 1 || port > 65535 || !process.argv[3]) {
  throw new Error('usage: node tools/site_browser_check.mjs DEBUG_PORT LOCAL_URL [ARTIFACT_DIR]');
}
const base = new URL(process.argv[3]);
if (base.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(base.hostname)) {
  throw new Error('Use a loopback HTTP preview and a fresh browser profile.');
}
const url = base.href.endsWith('/') ? base.href : base.href + '/';
const artifacts = pathToFileURL(resolve(process.argv[4] || 'work/site-browser-check') + sep);
const data = await (await fetch(url + 'data.json',{signal:AbortSignal.timeout(10000)})).json();
const featureLineCount = data.features.length + new Set(data.features.map(f => f.area)).size * 2 + 1;
const c = await connect(port);
await mkdir(artifacts,{recursive:true});
let assertions = 0;
const ok = (value,message) => {assert.ok(value,message); assertions++;};
const eq = (value,want,message) => {assert.deepEqual(value,want,message); assertions++;};
const evaluate = c.evaluate;
async function viewport(width,height) {
  await c.call('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:false});
}
async function navigate(tui=true) {
  const old = await evaluate('performance.timeOrigin');
  await c.call('Page.navigate',{url});
  await c.waitFor(`performance.timeOrigin !== ${old} && document.readyState === 'complete'`);
  if(tui) await c.waitFor("!!document.querySelector('#tui')");
}
async function click(selector) {await evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);}
async function typed(text) {
  await evaluate("document.querySelector('#tui-input').focus(); document.querySelector('#tui-input').value=''; document.querySelector('#tui-input').dispatchEvent(new Event('input',{bubbles:true}))");
  for(const letter of text) await c.key(letter,'Key'+letter.toUpperCase(),{text:letter});
}
async function fits() {
  return evaluate(`(()=>{const o=document.querySelector('.tui-out');return {horizontal:document.documentElement.scrollWidth<=innerWidth+1 && o.scrollWidth<=o.clientWidth+1}})()`);
}
try {
  await viewport(1200,900);
  await c.call('Emulation.setEmulatedMedia',{features:[{name:'prefers-color-scheme',value:'dark'}]});
  await navigate();
  await evaluate('localStorage.clear()');
  await navigate();
  await click('.tui-menu [data-cmd="features"]');
  eq(await evaluate("document.activeElement.className"),'tui-out','menu focuses output');
  eq(await evaluate("document.querySelectorAll('.fl').length"),featureLineCount,'all lines are shown');
  await typed('features');
  eq(await evaluate("document.querySelector('#tui-input').value"),'features','typed letters reach the command line');
  await c.key('Enter');
  eq(await evaluate("document.querySelector('.file-name').textContent"),'features','typed command executes');
  await typed('features --status pl');
  eq(await evaluate("document.querySelector('.tui-ghost-rest').textContent"),'anned','autocomplete works after a numbered answer');
  await c.key('Tab');
  eq(await evaluate("document.querySelector('#tui-input').value"),'features --status planned ','Tab accepts suggestion');
  await c.key('Enter');
  eq(await evaluate("document.querySelector('.file-name').textContent"),'features/planned','filtered command is framed');
  await c.key('ArrowUp');
  eq(await evaluate("document.querySelector('#tui-input').value"),'features --status planned','history works in command input');
  await c.command('features');
  await click('.tui-title [data-theme-set="light"]');
  ok((await fits()).horizontal,'light theme stays inside the viewport');
  await c.screenshot(new URL('desktop-light.png',artifacts));
  await click('.tui-title [data-theme-set="dark"]');
  ok((await fits()).horizontal,'dark theme stays inside the viewport');
  await c.screenshot(new URL('desktop-dark.png',artifacts));
  await click('.tui-title [data-theme-set="system"]');
  await c.call('Emulation.setEmulatedMedia',{features:[{name:'prefers-color-scheme',value:'light'}]});
  ok((await fits()).horizontal,'system theme change stays inside the viewport');
  await click('.tui-title [data-theme-set="dark"]');
  const selected=await evaluate(`(()=>{const s=getSelection(),r=document.createRange();r.selectNodeContents(document.querySelector('.file'));s.removeAllRanges();s.addRange(r);return s.toString()})()`);
  ok(!selected.includes('File: features'),'selection omits file header');
  ok(!selected.split('\n').some(s=>/^\d+$/.test(s.trim())),'selection omits numeric gutters');
  await evaluate('getSelection().removeAllRanges()');
  await c.call('Accessibility.enable');
  const documentRoot=(await c.call('DOM.getDocument')).root.nodeId;
  for(const selector of ['.gut','.file-head']) {
    const {nodeId}=await c.call('DOM.querySelector',{nodeId:documentRoot,selector});
    const ax=await c.call('Accessibility.getPartialAXTree',{nodeId,fetchRelatives:false});
    ok(ax.nodes[0].ignored,selector+' is hidden from the accessibility tree');
  }
  await c.command('features');
  await viewport(900,600);
  ok((await fits()).horizontal,'resized page stays inside the viewport');
  for(const name of ['help','roadmap','install','settings','connect','changelog','docs','links','version','credits','licence']) {
    await c.command(name);
    ok(!!await evaluate("document.querySelector('.file-name')"),name+' renders a file');
    ok((await fits()).horizontal,name+' stays inside the viewport');
  }
  await viewport(390,844);
  await navigate();
  ok(await evaluate("document.documentElement.classList.contains('plain')"),'phone starts in plain view');
  await click('.plain-bar button');
  await click('.tui-menu [data-cmd="features"]');
  ok((await fits()).horizontal,'phone terminal has no horizontal page overflow');
  const wrap=await evaluate(`(()=>{const row=[...document.querySelectorAll('.fl')].find(r=>r.getBoundingClientRect().height>35);return !!row && row.querySelectorAll('.gut').length===1 && row.querySelector('.gut').textContent===''})()`);
  ok(wrap,'wrapped phone line has one text-free gutter');
  await evaluate('scrollTo(0,0)');
  await c.screenshot(new URL('phone-dark.png',artifacts));
  await click('.tui-title [data-theme-set="light"]');
  await c.screenshot(new URL('phone-light.png',artifacts));
  await click('.tui-title .tui-plain');
  ok(await evaluate("document.documentElement.classList.contains('plain')"),'plain-view switch works');
  ok((await fits()).horizontal,'phone plain view has no horizontal page overflow');
  await viewport(320,568);
  await click('.plain-bar button');
  await c.command('settings');
  ok((await fits()).horizontal,'320px terminal stays inside viewport');
  await c.call('Emulation.setEmulatedMedia',{features:[{name:'forced-colors',value:'active'},{name:'prefers-reduced-motion',value:'reduce'}]});
  eq(await evaluate("getComputedStyle(document.querySelector('.cursor')).animationName"),'none','reduced motion stops cursor animation');
  ok((await fits()).horizontal,'forced-colors view stays inside viewport');
  await c.call('Emulation.setEmulatedMedia',{features:[{name:'prefers-color-scheme',value:'dark'}]});
  await c.call('Network.setBlockedURLs',{urls:['*data.json*']});
  await navigate(false);
  ok(!await evaluate("!!document.querySelector('#tui')"),'data failure leaves static document');
  ok(await evaluate("getComputedStyle(document.querySelector('main')).display !== 'none'"),'static content remains visible after data failure');
  await c.call('Network.setBlockedURLs',{urls:[]});
  const deniedStorage=await c.call('Page.addScriptToEvaluateOnNewDocument',{
    source:"Object.defineProperty(window,'localStorage',{get(){throw new Error('storage disabled for test')}})",
  });
  await navigate();
  await click('.plain-bar button');
  await c.command('features');
  ok(!!await evaluate("document.querySelector('.file-name')"),'commands work when local storage is blocked');
  await c.call('Page.removeScriptToEvaluateOnNewDocument',{identifier:deniedStorage.identifier});
  await c.call('Emulation.setScriptExecutionDisabled',{value:true});
  await navigate(false);
  ok(await evaluate("getComputedStyle(document.querySelector('main')).display !== 'none'"),'page works without JavaScript');
  await c.call('Emulation.setScriptExecutionDisabled',{value:false});
  const errors=c.events.filter(e=>e.method==='Runtime.exceptionThrown');
  eq(errors,[],'no uncaught browser exceptions');
  const remote=c.events.filter(e=>e.method==='Network.requestWillBeSent').map(e=>e.params.request.url).filter(u=>!u.startsWith(url)&&!u.startsWith('data:'));
  eq(remote,[],'page makes no external requests');
  console.log(`PASS ${assertions} browser assertions`);
  await writeFile(new URL('browser-result.json',artifacts),JSON.stringify({assertions,errors:errors.length,remoteRequests:remote.length},null,2)+'\n');
} finally {await c.call('Emulation.setScriptExecutionDisabled',{value:false});await c.close();}
