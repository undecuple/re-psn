// Optional real-browser acceptance against owned synthetic local fixtures only.
// node tools/client_browser_check.mjs DEBUG_PORT PRIVATE_LINK_FILE FIXTURE_DIR PLAY_ADDR HOST_ADDR APP SERVER CATALOG NATIVE_SDK_SAMPLE
// Needs Node24+, OpenSSL and a headless Chromium CDP session. All accounts, keys,
// catalogs, host processes and downloads are disposable synthetic test fixtures.
import assert from 'node:assert/strict';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
import {execFileSync,spawn} from 'node:child_process';
import {connect} from './site_browser_cdp.mjs';

const [debugPort,linkFile,rootDir,playAddress,hostAddress,appPath,serverPath,catalogPath,nativePath]=process.argv.slice(2);
await mkdir(rootDir+'/screenshots',{recursive:true});
const readLink=async file=>{const value=(await readFile(file,'utf8')).trim();assert(/^http:\/\/127\.0\.0\.1:\d+\/#token=[a-f0-9]{64}$/.test(value),'private local launch link');return value;};
let c=await connect(Number(debugPort)), companion;
const errors=[], requests=[];let checks=0;
const appOrigin=new URL(await readLink(linkFile)).origin;
const ok=(value,message)=>{assert.ok(value,message);checks++;};
const eq=(value,want,message)=>{assert.deepEqual(value,want,message);checks++;};
const click=async selector=>{await c.evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);};
const fill=async(selector,value)=>{await c.evaluate(`{const x=document.querySelector(${JSON.stringify(selector)});x.value=${JSON.stringify(value)};x.dispatchEvent(new Event('input',{bubbles:true}));}`);};
const submit=async id=>{await c.evaluate(`document.getElementById(${JSON.stringify(id)}).requestSubmit()`);};
const idle=()=>c.waitFor(`!document.querySelector('#refresh-state').disabled && ![...document.querySelectorAll('#error:not([hidden])')].length`);
async function navigate(link){await c.call('Emulation.setDeviceMetricsOverride',{width:1280,height:960,deviceScaleFactor:1,mobile:false});await c.call('Page.navigate',{url:link});await c.waitFor(`document.querySelector('#bridge-state')?.textContent.includes('Private app session')`);}
try {
 await navigate(await readLink(linkFile));
 ok(await c.evaluate(`!document.querySelector('#welcome').hidden`),'explicit three-role welcome');
 eq(await c.evaluate(`location.hash`),'','bootstrap removed from URL');
 ok(await c.evaluate(`!document.body.textContent.includes(sessionStorage.getItem('re-psn-ui-token-v1'))`),'persistent capability absent from DOM');
 const colours=await c.evaluate(`({page:getComputedStyle(document.documentElement).getPropertyValue('--page').trim(),bg:getComputedStyle(document.body).backgroundColor,font:getComputedStyle(document.body).fontFamily,text:getComputedStyle(document.body).color})`);
 await writeFile(rootDir+'/browser-colours.json',JSON.stringify(colours,null,2)+'\n');
 await c.screenshot(rootDir+'/screenshots/welcome.png');
 await click('button[data-role="host"]');await c.waitFor(`!document.querySelector('#workspace').hidden && document.querySelector('#workspace-badge').textContent==='Host'`);
 ok(await c.evaluate(`[...document.querySelectorAll('nav [data-personal], .sidebar [data-personal]')].every(x=>x.hidden)`),'Host intent hides personal navigation');
 await click('button[data-page="host"]');await fill('#host-bind',hostAddress);await submit('host-form');
 await c.waitFor(`document.querySelector('#host-badge').textContent==='Running'`);
 ok(await c.evaluate(`!document.querySelector('#host-stop').disabled`),'real host can be stopped');
 await c.screenshot(rootDir+'/screenshots/host.png');
 const started=await c.evaluate(`document.querySelector('#host-started').textContent`);
 await click('button[data-role="both"]');await c.waitFor(`document.querySelector('#workspace-badge').textContent==='Both'`);
 await click('button[data-page="play"]');await fill('#play-server',playAddress);await submit('play-form');
 await c.waitFor(`document.querySelector('#play-message').textContent.includes('Reachable')`);
 eq(await c.evaluate(`document.querySelector('#host-bind').value`),hostAddress,'Both keeps separate host bind');
 eq(await c.evaluate(`document.querySelector('#play-server').value`),playAddress,'Both keeps selected remote target');
 await click('button[data-page="profile"]');await fill('#profile-name','Synthetic browser profile');await submit('profile-create-form');await c.waitFor(`!document.querySelector('#profile-details').hidden`);
 await click('button[data-page="trophies"]');
 const doc=await c.call('DOM.getDocument');const file=await c.call('DOM.querySelector',{nodeId:doc.root.nodeId,selector:'#catalog-file'});await c.call('DOM.setFileInputFiles',{nodeId:file.nodeId,files:[catalogPath]});
 await c.waitFor(`document.querySelector('#catalog-preview').textContent.includes('RE-PSN-SDK-DEMO')`);await submit('catalog-form');
 await c.waitFor(`document.querySelector('#trophy-title').options.length===1`);await click('#grant-start');
 await c.waitFor(`!document.querySelector('#grant-path').hidden`);
 const grant=(await c.evaluate(`document.querySelector('#grant-path').textContent`)).replace(/^Private descriptor: /,'');
 execFileSync(nativePath,[grant,'unlock','1'],{stdio:['ignore','pipe','pipe']});
 await click('#refresh-state');await c.waitFor(`document.querySelector('#trophies-body').textContent.includes('Reported') || document.querySelector('#trophies-body').textContent.includes('reported')`);
 ok(await c.evaluate(`document.querySelector('#trophies-body').textContent.includes('Locked') || document.querySelector('#trophies-body').textContent.includes('locked')`),'remaining trophies stay locked');
 await click('#grant-revoke');await c.waitFor(`document.querySelector('#grant-path').hidden`);
 await c.screenshot(rootDir+'/screenshots/trophies.png');
 // Exercise real TLS social actions through the settings and Friends forms.
 const me=await c.evaluate(`document.querySelector('#profile-id').value`);
 const peerDir=rootDir+'/peer';
 const peer=JSON.parse(execFileSync(appPath,['profile','create','--dir',peerDir,'--name','Synthetic peer'],{encoding:'utf8'})).account_id;
 const ca=rootDir+'/social.crt',key=rootDir+'/social.key';
 execFileSync('openssl',['req','-x509','-newkey','ed25519','-nodes','-days','1','-subj','/CN=Synthetic loopback fixture','-addext','subjectAltName=IP:127.0.0.1','-addext','basicConstraints=critical,CA:TRUE','-keyout',key,'-out',ca],{stdio:'ignore'});
 // Reserve a free listener through Node, then let the real companion bind it.
 const net=await import('node:net');const listener=net.createServer();await new Promise(r=>listener.listen(0,'127.0.0.1',r));const socialAddress='127.0.0.1:'+listener.address().port;await new Promise(r=>listener.close(r));const socialOrigin='https://'+socialAddress;
 companion=spawn(appPath,['account-relay','serve','--state',rootDir+'/social','--listen',socialAddress,'--allow',me+','+peer,'--tls-cert',ca,'--tls-key',key],{stdio:['ignore','pipe','pipe']});
 await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('companion startup timeout')),10000);companion.stdout.on('data',data=>{if(data.toString().includes('ready')){clearTimeout(timer);resolve();}});companion.on('exit',()=>{clearTimeout(timer);reject(new Error('companion exited early'));});});
 const peerAction=(action,id)=>JSON.parse(execFileSync(appPath,['friends',action,'--dir',peerDir,'--relay',socialOrigin,'--ca-file',ca,...(id?['--id',id]:[])],{encoding:'utf8'}));
 peerAction('register');
 await click('button[data-page="settings"]');await fill('#setting-social-origin',socialOrigin);await fill('#setting-social-ca',ca);await submit('settings-form');await idle();
 await click('button[data-page="friends"]');await click('#friend-register');await c.waitFor(`document.querySelector('#confirm-dialog').open`);await click('#confirm-accept');await idle();
 await fill('#friend-id',peer);await submit('friend-request-form');await idle();
 ok(peerAction('list').incoming.some(x=>x.account_id===me),'browser registration consent and request reach authenticated peer');
 peerAction('accept',me);await click('#friends-refresh');await idle();await c.waitFor(`document.querySelector('button[data-friend-action="block"]')`);
 ok(await c.evaluate(`document.querySelector('[aria-label="Friends"]')?.textContent.includes('Synthetic peer')`),'recipient acceptance appears after real UI refresh');
 await click('button[data-friend-action="block"]');await idle();
 eq((peerAction('list').friends || []).length,0,'browser block removes the peer friendship');
 ok(await c.evaluate(`document.querySelector('[aria-label="Blocked"]')?.textContent.includes(${JSON.stringify(peer)})`),'blocked account appears in browser snapshot');
 // Check the actual download handler and that password inputs clear.
 await click('button[data-page="profile"]');await c.call('Page.setDownloadBehavior',{behavior:'allow',downloadPath:rootDir+'/downloads'});await fill('#export-passphrase','Synthetic browser backup phrase');await fill('#export-confirm','Synthetic browser backup phrase');await submit('export-form');await idle();
 await c.waitFor(`document.querySelector('#export-passphrase').value==='' && document.querySelector('#export-confirm').value===''`);
 let backup;for(let i=0;i<100;i++){try{backup=await readFile(rootDir+'/downloads/re-psn-profile.backup');break;}catch{await new Promise(r=>setTimeout(r,30));}}
 ok(backup?.subarray(0,8).toString()==='RPSNBAK1' && !backup.includes(me),'browser downloads encrypted portable backup');
 const passFile=rootDir+'/backup-passphrase';await writeFile(passFile,'Synthetic browser backup phrase',{mode:0o600});
 const restored=JSON.parse(execFileSync(appPath,['profile','restore','--dir',rootDir+'/restored','--input',rootDir+'/downloads/re-psn-profile.backup','--passphrase-file',passFile],{encoding:'utf8'}));
 eq(restored.account_id,me,'downloaded UI backup restores the same portable account');
 // Closing the only tab must not strand the still-owned host.
 errors.push(...c.events.filter(x=>x.method==='Runtime.exceptionThrown'));requests.push(...c.events.filter(x=>x.method==='Network.requestWillBeSent'));
 await c.close();
 const reopened=rootDir+'/dispatched-file-uri';
 await mkdir(rootDir+'/bin');await writeFile(rootDir+'/bin/xdg-open','#!/bin/sh\nprintf \"%s\\n\" \"$1\" > \"$REPSN_TEST_LAUNCH\"\n',{mode:0o700});
 execFileSync(appPath,['ui','--config',rootDir+'/state/re-psn.ini','--profile-dir',rootDir+'/state/profile','--server-binary',serverPath],{env:{...process.env,PATH:rootDir+'/bin:'+process.env.PATH,REPSN_TEST_LAUNCH:reopened},stdio:['ignore','pipe','pipe']});
 const fileURI=(await readFile(reopened,'utf8')).trim();ok(fileURI.startsWith('file://') && !fileURI.includes('token='),'OS dispatcher receives private file URI without bootstrap');
 c=await connect(Number(debugPort));await navigate(fileURI);
 eq(await c.evaluate(`document.querySelector('#host-started').textContent`),started,'reopen preserves actual hosted server');
 ok(await c.evaluate(`document.querySelector('#activity-host').textContent.includes('running on')`),'host still running after tab closure');
 await c.call('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true});
 await click('button[data-page="trophies"]');
 ok(await c.evaluate(`document.documentElement.scrollWidth<=innerWidth+1`),'390px layout has no page overflow');
 await c.screenshot(rootDir+'/screenshots/narrow.png');
 await c.call('Emulation.setDeviceMetricsOverride',{width:1280,height:960,deviceScaleFactor:1,mobile:false});
 await click('button[data-page="host"]');await click('#host-stop');await c.waitFor(`document.querySelector('#confirm-dialog').open`);await click('#confirm-accept');await c.waitFor(`document.querySelector('#host-badge').textContent==='Not running'`);
 ok(await c.evaluate(`document.querySelector('#play-server').value===${JSON.stringify(playAddress)}`),'owned Stop preserves remote target');
 errors.push(...c.events.filter(x=>x.method==='Runtime.exceptionThrown'));
 eq(errors.length,0,'browser no runtime exceptions across both tabs');
 requests.push(...c.events.filter(x=>x.method==='Network.requestWillBeSent'));
 eq(requests.filter(x=>/^https?:/.test(x.params.request.url) && new URL(x.params.request.url).origin!==appOrigin).length,0,'app browser makes no external HTTP requests');
 await writeFile(rootDir+'/browser-result.json',JSON.stringify({status:'passed',checks,scope:'Actual Linux app backend, real host process, separate remote server, native C trophy caller, browser roles/reopen/narrow; synthetic data only'},null,2)+'\n');
 console.log('Actual-browser checks passed:',checks);
} finally {if(companion && companion.exitCode===null){companion.kill('SIGTERM');await new Promise(r=>companion.once('exit',r));}await c.close().catch(()=>{});}
