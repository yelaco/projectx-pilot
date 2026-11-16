'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const {runInNewContext} = require('node:vm');
const script = readFileSync(join(__dirname, '../internal/dashboard/admin.js'), 'utf8');

function load({automation = false, selectedManager = 'practice', session = false} = {}) {
  const listeners = {};
  const account = {disabled: false, required: true, value: '26567907'};
  const profileAccount = {disabled: false, required: true, value: '42'};
  const manager = {value: selectedManager, addEventListener: (type, handler) => {listeners[type] = handler;}};
  const sessionListeners = {};
  const sessionMode = {value: 'normal', addEventListener: (type, handler) => {sessionListeners[type] = handler;}};
  const customHours = {disabled: false, hidden: false};
  const form = {querySelector: selector => selector === '[name="trade_manager_id"]' ? manager : selector === '[name="account_id"]' ? profileAccount : null};
  // Global selectors deliberately resolve to the execution form. This catches
  // account-disabling code that leaks across forms or runs on execution pages.
  const document = {
    querySelector(selector) {
      if (selector === '[data-session-mode]') return session ? sessionMode : null;
      if (selector === '[data-session-custom]') return session ? customHours : null;
      if (selector === 'form[action="/automation"]') return automation ? form : null;
      if (selector === '[name="account_id"]') return account;
      if (selector === '[name="trade_manager_id"]') return manager;
      return null;
    },
    querySelectorAll: () => [],
    addEventListener() {},
  };
  runInNewContext(script, {document});
  return {account, profileAccount, manager, listeners, sessionMode, customHours, sessionListeners};
}

test('Practice execution submits account ID with a selected Trade Manager', () => {
  const {account} = load();
  assert.equal(account.disabled, false);
  assert.equal(account.required, true);
  // Native forms include enabled controls and omit disabled controls.
  const submitted = new URLSearchParams();
  if (!account.disabled) submitted.set('account_id', account.value);
  assert.equal(submitted.get('account_id'), '26567907');
});

test('automation profiles still derive their account from a Trade Manager', () => {
  const {account, profileAccount, manager, listeners} = load({automation: true});
  assert.equal(profileAccount.disabled, true);
  assert.equal(profileAccount.required, false);
  assert.equal(account.disabled, false);
  manager.value = '';
  listeners.change();
  assert.equal(profileAccount.disabled, false);
  assert.equal(profileAccount.required, true);
  assert.equal(account.disabled, false);
});

test('direct-account automation keeps its account selectable', () => {
  const {profileAccount} = load({automation: true, selectedManager: ''});
  assert.equal(profileAccount.disabled, false);
  assert.equal(profileAccount.required, true);
});

test('normal-hours preset only enables custom date/time inputs when selected', () => {
  const {sessionMode, customHours, sessionListeners} = load({session: true});
  assert.equal(customHours.disabled, true);
  assert.equal(customHours.hidden, true);
  sessionMode.value = 'custom';
  sessionListeners.change();
  assert.equal(customHours.disabled, false);
  assert.equal(customHours.hidden, false);
});

test('running through market closure keeps Pause visible and Start hidden', async () => {
  const badge = {dataset: {profileArming: 'swift'}, textContent: ''};
  const start = {dataset: {profileStart: 'swift'}, hidden: false};
  const pause = {dataset: {profilePause: 'swift'}, hidden: true};
  const mode = {textContent: ''};
  const document = {
    hidden: false,
    querySelector() { return null; },
    querySelectorAll(selector) {
      if (selector === '[data-profile-arming]') return [badge];
      if (selector === '[data-profile-start]') return [start];
      if (selector === '[data-profile-pause]') return [pause];
      if (selector === '[data-monitor="execution-mode"]') return [mode];
      return [];
    },
    addEventListener() {},
  };
  const payload = {State: {connected: true, updated_at: '2026-11-13T21:10:00Z'}, Tunnel: {State: 'connected'}, Execution: {Configured: true, RunRequested: true, RunProfileID: 'swift', RunMessage: 'Running · market closed', Armed: false, Automated: true, AutomationProfileID: 'swift'}};
  const context = {document, fetch: async () => ({ok: true, json: async () => payload}), setTimeout() {}};
  runInNewContext(script, context);
  await context.monitor();
  assert.equal(badge.textContent, 'Running · market closed');
  assert.equal(start.hidden, true);
  assert.equal(pause.hidden, false);
  assert.equal(mode.textContent, 'Automation running');
  payload.Execution.RunRequested = false;
  payload.Execution.RunMessage = 'Paused';
  await context.monitor();
  assert.equal(badge.textContent, 'Paused');
  assert.equal(start.hidden, false);
  assert.equal(pause.hidden, true);
  payload.Execution.Armed = true;
  payload.Execution.Automated = false;
  await context.monitor();
  assert.equal(badge.textContent, 'Paused · manual Practice session armed');
  assert.equal(start.hidden, false);
  payload.Execution.RunRequested = true;
  payload.Execution.RunProfileID = 'other-profile';
  payload.Execution.Armed = false;
  await context.monitor();
  assert.equal(badge.textContent, 'Blocked · automation other-profile is running');
});

test('webhook controls use canonical server URLs and clear removed profiles', async () => {
  const base = {value: '', hasAttribute: name => name === 'data-public-url'};
  const profile = {dataset: {profileUrl: 'swift'}, value: '', hasAttribute: name => name === 'data-profile-url'};
  const removed = {dataset: {profileUrl: 'removed'}, value: 'old-address', hasAttribute: name => name === 'data-profile-url'};
  const fields = {base, profile, removed};
  const buttons = Object.keys(fields).map(copy => ({dataset: {copy}, disabled: true}));
  const kind = {textContent: ''};
  const document = {
    hidden: false, querySelector() {return null;}, addEventListener() {},
    getElementById(id) {return fields[id];},
    querySelectorAll(selector) {
      if (selector === '[data-public-url]') return [base];
      if (selector === '[data-profile-url]') return [profile, removed];
      if (selector === '[data-copy]') return buttons;
      if (selector === '[data-monitor="public-url-kind"]') return [kind];
      return [];
    }
  };
  const payload = {State: {}, Tunnel: {}, PublicURL: 'https://alerts.example.com', PublicURLKind: 'Permanent hostname', WebhookURLs: {swift: 'https://alerts.example.com/tradingview/swift'}};
  const context = {document, fetch: async () => ({ok:true, json:async()=>payload}), setTimeout() {}};
  runInNewContext(script, context);
  await context.monitor();
  assert.equal(base.value, payload.PublicURL);
  assert.equal(profile.value, payload.WebhookURLs.swift);
  assert.equal(removed.value, '');
  assert.equal(kind.textContent, 'Permanent hostname');
  assert.equal(buttons[0].disabled, false);
  assert.equal(buttons[1].disabled, false);
  assert.equal(buttons[2].disabled, true);
});

test('independent profile status never inherits another profile block', async () => {
 const nodes=['a','b'].map(id=>({dataset:{profileArming:id},textContent:''}));
 const document={hidden:false,querySelector(){return null},querySelectorAll(s){return s==='[data-profile-arming]'?nodes:[]},addEventListener(){}};
 const payload={State:{},Tunnel:{},Execution:{RunRequested:true,RunProfileID:'a'},Profiles:{a:{RunRequested:true,RunProfileID:'a',RunMessage:'A running'},b:{RunRequested:true,RunProfileID:'b',RunMessage:'B running'}}};
 const context={document,fetch:async()=>({ok:true,json:async()=>payload}),setTimeout(){}};
 runInNewContext(script,context);await context.monitor();
 assert.equal(nodes[0].textContent,'A running');assert.equal(nodes[1].textContent,'B running');
 delete payload.Profiles.b;await context.monitor();assert.equal(nodes[1].textContent,'Paused');
});

test('account runner monitor renders safe lifecycle text and measured acceptance',async()=>{
 const state={textContent:''},management={textContent:''},stream={textContent:''},gate={textContent:''};
 const tbody={children:[],replaceChildren(){this.children=[]},appendChild(n){this.children.push(n)}};
 const document={hidden:false,addEventListener(){},querySelectorAll(){return []},createElement(){return {children:[],appendChild(n){this.children.push(n)},textContent:''}},querySelector(s){
  if(s==='[data-runner="42"]')return {querySelector(k){return k==='[data-runner-state]'?state:management}};
  if(s==='[data-user-stream]')return stream;if(s==='[data-global-gate]')return gate;if(s==='#target-job-rows')return tbody;return null;
 }};
 const at='2026-11-13T15:00:00.000Z',accepted='2026-11-13T15:00:00.125Z';
 const payload={State:{},Tunnel:{},GlobalPaused:true,UserData:{State:'subscribed',Accounts:2},Runners:[{AccountID:42,Status:{Armed:true,RunMessage:'Ready',Intents:[{State:'protected',Signal:{symbol:'MNQ'},OrderID:7,Message:'<script>literal text</script>'}]}}],TargetJobs:[{DeliveryID:'one',ProfileID:'a',AccountID:42,State:'submitted',Message:'Accepted',ReceivedAt:at,DispatchedAt:at,ChecksAt:at,SubmitAt:at,AcceptedAt:accepted}]};
 const context={document,fetch:async()=>({ok:true,json:async()=>payload}),setTimeout(){},Date};
 runInNewContext(script,context);await context.monitor();
 assert.match(state.textContent,/Armed/);assert.match(management.textContent,/MNQ protected · order #7/);
 assert.match(management.textContent,/<script>literal text<\/script>/);
 assert.equal(gate.textContent,'Global pause active');assert.match(stream.textContent,/2 subscribed accounts/);
 assert.equal(tbody.children[0].children[6].textContent,'125.0 ms');
});
