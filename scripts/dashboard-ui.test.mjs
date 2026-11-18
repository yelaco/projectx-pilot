// Development-only DOM tests: actual dashboard JavaScript, no broker/Discord I/O.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {parseHTML} from 'linkedom';
const source=fs.readFileSync(new URL('../internal/dashboard/admin.js',import.meta.url),'utf8');
function browser(html, response={}) {
 const {document,window}=parseHTML('<html><body>'+html+'</body></html>');
 Object.defineProperty(document,'hidden',{value:false,writable:true});
 // linkedom does not initialize the browser's checkbox checked property.
 document.querySelectorAll('input[type="checkbox"]').forEach(node=>{if(typeof node.checked!=='boolean')node.checked=node.hasAttribute('checked');});
 const timers=[];
 const context={document,window,console,AbortController,navigator:{clipboard:{writeText:async()=>{}}},setTimeout:(fn,ms)=>{timers.push({fn,ms});return timers.length},clearTimeout:()=>{},fetch:async()=>({ok:true,json:async()=>response})};
 vm.createContext(context);vm.runInContext(source+'\n globalThis.ui={monitor,updateRunnerLock,updateReportStatus,renderSystemHealth,filterSystemHealth};',context);
 return {document,context,timers,ui:context.ui};
}
const reason='market data stale while managing exposure';
const session='2026-11-16';
const rowHTML=()=>`<table><tr data-runner="42"><td><p data-runner-lock-summary class="error">Local lock</p><small data-runner-lock-guidance></small><p data-runner-lock-changed hidden>Refresh controls</p><details data-lock-review data-lock-reason="${reason}" data-lock-session="${session}" data-lock-intent="old"><form><input name="current_password" type="password" value="typed-password"></form></details><div data-runner-history></div><div data-runner-management></div></td></tr></table>`;
function runner(overrides={}){return {AccountID:42,LockReason:reason,LockSession:session,LockPolicy:{Category:'Stale quote',Guidance:'Review'},Status:{Intents:[{ID:'old',Session:session,State:'closed',OrderID:77,HaltReason:reason}]},...overrides}}
test('cleared lock removes old action but retains clearly separate history',()=>{
 const b=browser(rowHTML());const row=b.document.querySelector('tr');b.ui.updateRunnerLock(row,runner({LockReason:''}));
 assert.equal(row.querySelector('[data-lock-review]'),null);assert.equal(row.querySelector('[data-runner-lock-summary]').textContent,'No current-session lock.');assert.match(row.querySelector('[data-runner-history]').textContent,/market data stale/);assert.equal(row.querySelector('[data-runner-lock-summary]').classList.contains('error'),false);
});
test('live refresh preserves an unchanged review form and typed password',()=>{
 const b=browser(rowHTML());const row=b.document.querySelector('tr');const input=row.querySelector('input');b.ui.updateRunnerLock(row,runner());assert.equal(row.querySelector('input'),input);assert.equal(input.value,'typed-password');assert.equal(row.querySelector('[data-runner-lock-changed]').hidden,true);
});
test('new lock or trading session invalidates the old review form',()=>{
 for(const change of [{LockReason:'daily loss limit reached'},{LockSession:'2026-11-17'}]){const b=browser(rowHTML());const row=b.document.querySelector('tr');b.ui.updateRunnerLock(row,runner(change));assert.equal(row.querySelector('[data-lock-review]'),null);assert.equal(row.querySelector('[data-runner-lock-changed]').hidden,false);}
});
test('lock messages are rendered as text, never HTML',()=>{
 const b=browser(rowHTML());const row=b.document.querySelector('tr');b.ui.updateRunnerLock(row,runner({LockReason:'<img src=x onerror="bad()">'}));assert.equal(row.querySelector('img'),null);assert.match(row.textContent,/<img/);
});
function monitorResponse(overrides={}){return {Execution:{Configured:true,ModeLabel:'Automatic execution armed'},Profiles:{},Runners:[],State:{connected:true},Tunnel:{State:'stopped'},Activity:[],...overrides}}
test('armed profile keeps Start hidden even when RunRequested is false',async()=>{
 const html='<span data-profile-arming="p"></span><form data-profile-start="p"></form><form data-profile-pause="p" hidden></form>';
 const b=browser(html,monitorResponse({Profiles:{p:{Armed:true,Automated:true,AutomationProfileID:'p',RunProfileID:'p',RunRequested:false}}}));await b.ui.monitor();assert.equal(b.document.querySelector('[data-profile-start]').hidden,true);assert.equal(b.document.querySelector('[data-profile-pause]').hidden,false);
});
test('profile list and global badge use actual current automation state',async()=>{
 const b=browser('<span data-profile-summary="p" data-routing-mode="automatic">Draft</span><span data-monitor="execution-mode"></span>',monitorResponse({Profiles:{p:{RunRequested:true}}}));await b.ui.monitor();assert.equal(b.document.querySelector('[data-profile-summary]').textContent,'Running');assert.equal(b.document.querySelector('[data-monitor="execution-mode"]').textContent,'Automatic execution armed');
});
test('global Resume follows global pause without a page reload',async()=>{
 const b=browser('<form data-global-resume hidden></form>',monitorResponse({GlobalPaused:true}));await b.ui.monitor();assert.equal(b.document.querySelector('form').hidden,false);
});
test('report delivery refresh never overwrites secret or edited enabled fields',()=>{
 const b=browser('<p data-report-summary="system"></p><p data-report-error="system">old error</p><p data-report-last="system"></p><p data-report-dropped="system"></p><input name="discord_webhook" value="typed-secret"><input name="current_password" value="typed-password"><input name="enabled" type="checkbox">');
 b.ui.updateReportStatus('system',{Enabled:true,Configured:true,LastError:'',LastSent:'2026-11-16T21:00:00Z',Dropped:0});assert.equal(b.document.querySelector('[name="discord_webhook"]').value,'typed-secret');assert.equal(b.document.querySelector('[name="current_password"]').value,'typed-password');assert.equal(b.document.querySelector('[data-report-error]').hidden,true);assert.match(b.document.querySelector('[data-report-last]').textContent,/2026-11-16/);
});
test('failed refresh visibly marks the entire dashboard stale',async()=>{
 const b=browser('<p data-live-stale hidden></p>');b.context.fetch=async()=>{throw new Error('offline')};await b.ui.monitor();assert.equal(b.document.querySelector('[data-live-stale]').hidden,false);
});
test('health filters preserve distinctions between history and active problems',()=>{
 const b=browser('<div id="system-health-checks"></div><input id="health-search"><input id="health-issues-only" type="checkbox"><p id="health-empty" hidden></p><p id="health-stale-warning"></p>');
 b.ui.renderSystemHealth({CapturedAt:'2026-11-16T21:00:00Z',Checks:[{ID:'history',State:'history',Group:'Account #42',Name:'Original trigger',Symbol:'•',Label:'History',Summary:'old fault'},{ID:'lock',State:'blocked',Group:'Account #42',Name:'Current lock',Symbol:'!',Label:'Blocked',Summary:'current fault'}]});
 b.document.querySelector('#health-issues-only').checked=true;b.ui.filterSystemHealth();const cards=b.document.querySelectorAll('.health-check');assert.equal(cards[0].hidden,true);assert.equal(cards[1].hidden,false);assert.equal(b.document.querySelector('#health-stale-warning').hidden,true);
});
test('form helpers only disable derived automation account, not Practice account',()=>{
 const b=browser('<form action="/automation"><select name="trade_manager_id"><option value="tm" selected>manager</option></select><input name="account_id"></form><form action="/execution/settings"><input name="account_id" required></form>');assert.equal(b.document.querySelector('form[action="/automation"] [name="account_id"]').disabled,true);assert.equal(b.document.querySelector('form[action="/execution/settings"] [name="account_id"]').disabled,false);
});
test('Practice monitor uses manual exposure instead of automatic account exposure',async()=>{
 const b=browser('<section data-execution-scope="manual"><span data-monitor="execution-state"></span><span data-monitor="execution-exposure"></span></section>',monitorResponse({Execution:{State:'protected',Orders:2,Positions:1},ManualExecution:{State:'idle',Orders:0,Positions:0}}));await b.ui.monitor();assert.equal(b.document.querySelector('[data-monitor="execution-state"]').textContent,'idle');assert.equal(b.document.querySelector('[data-monitor="execution-exposure"]').textContent,'0 / 0');
});
test('profile rejection details come from that profile',async()=>{
 const b=browser('<section data-profile-execution-scope="p"><p data-submission-reason="one"></p></section>',monitorResponse({Execution:{SubmissionDetail:{IntentID:'other',Reason:'wrong account'}},Profiles:{p:{SubmissionDetail:{IntentID:'one',Reason:'scoped reason'}}}}));await b.ui.monitor();assert.match(b.document.querySelector('p').textContent,/scoped reason/);assert.doesNotMatch(b.document.querySelector('p').textContent,/wrong account/);
});
test('multi-account mode control follows routing without overwriting the chosen mode',()=>{
 const b=browser('<select data-trade-manager-routing><option value="single" selected>Single</option><option value="multiple">Multiple</option></select><label data-multi-account-options><select name="multi_account_mode"><option value="round_robin" selected>Distribution</option></select></label>');
 const group=b.document.querySelector('[data-multi-account-options]');const mode=group.querySelector('select');assert.equal(group.hidden,true);assert.equal(mode.disabled,true);
 const routing=b.document.querySelector('[data-trade-manager-routing]');routing.querySelector('[value="single"]').removeAttribute('selected');routing.querySelector('[value="multiple"]').setAttribute('selected','');routing.dispatchEvent(new b.context.window.Event('change'));
 assert.equal(group.hidden,false);assert.equal(mode.disabled,false);assert.equal(mode.value,'round_robin');
});
test('unassigned distribution outcome never displays fictitious account zero',async()=>{
 const b=browser('<table><tbody id="target-job-rows"></tbody></table>',monitorResponse({TargetJobs:[{AccountID:0,DeliveryID:'no-ready',ProfileID:'p',RoutingMode:'round_robin',State:'blocked',Message:'No ready account'}]}));await b.ui.monitor();const text=b.document.querySelector('tbody').textContent;assert.match(text,/No account selected/);assert.match(text,/Distribution/);assert.doesNotMatch(text,/#0/);
});
test('account risk inputs follow selected accounts and routing while retaining edits',()=>{
 const b=browser('<select data-trade-manager-routing><option value="multiple" selected>Multiple</option><option value="single">Single</option></select><input type="checkbox" name="account_ids" value="42" checked><input type="checkbox" name="account_ids" value="43"><div data-account-risk-overrides><input data-account-risk="42" value="36"><input data-account-risk="43" value="72"></div>');
 const first=b.document.querySelector('[data-account-risk="42"]');const second=b.document.querySelector('[data-account-risk="43"]');assert.equal(first.disabled,false);assert.equal(second.disabled,true);
 const selected=b.document.querySelector('[name="account_ids"][value="43"]');selected.checked=true;selected.dispatchEvent(new b.context.window.Event('change'));assert.equal(second.disabled,false);assert.equal(second.value,'72');
});
