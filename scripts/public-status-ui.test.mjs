import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {parseHTML} from 'linkedom';
const source=fs.readFileSync(new URL('../internal/publicstatus/status.js',import.meta.url),'utf8');
function browser(slug=''){
 const {document}=parseHTML('<html><body><button id="refresh"></button><p id="freshness"></p><section id="accounts"></section><section id="detail"></section></body></html>');document.body.dataset.account=slug;
 const context={document,console,Intl,Date,AbortController,setInterval:()=>{},setTimeout:()=>{},clearTimeout:()=>{},fetch:async()=>{throw Error('offline')}};
 vm.createContext(context);vm.runInContext(source+'\n globalThis.ui={setSnapshot:(s)=>{snapshot=s;failed=false;render();},refresh};',context);return {document,context,ui:context.ui};
}
function snapshot(){return {captured_at:new Date().toISOString(),accounts:[{slug:'opaque',alias:'Account A',state:'Running',state_at:new Date().toISOString(),updated_at:new Date().toISOString(),data_available:true,today_start:new Date().toISOString(),total:null,total_status:'Lifetime unavailable: balance not reconciled',today:0,trades:[]}]};}
test('public overview distinguishes unavailable lifetime from zero today',()=>{const b=browser();b.ui.setSnapshot(snapshot());const text=b.document.querySelector('#accounts').textContent;assert.match(text,/Unavailable/);assert.match(text,/\$0\.00/);assert.match(text,/balance not reconciled/);assert.equal(b.document.querySelector('.card').getAttribute('href'),'/accounts/opaque');});
test('stale financial data and failed browser refresh stay visible',async()=>{const b=browser();const s=snapshot();s.accounts[0].refresh_failed=true;b.ui.setSnapshot(s);assert.match(b.document.querySelector('#accounts').textContent,/Stale/);await b.ui.refresh();assert.match(b.document.querySelector('#freshness').textContent,/Refresh failed/);assert.match(b.document.querySelector('#accounts').textContent,/Account A/);});
test('public account details render text safely and label opening legs',()=>{const b=browser('opaque');const s=snapshot();s.accounts[0].alias='<img src=x onerror=bad()>';s.accounts[0].trades=[{at:new Date().toISOString(),instrument:'MCL',side:'Buy',quantity:1,price:60,gross:null,costs:null}];b.ui.setSnapshot(s);assert.equal(b.document.querySelector('img'),null);assert.match(b.document.querySelector('#detail').textContent,/Opening leg/);assert.match(b.document.querySelector('#detail').textContent,/Unavailable/);});
test('revoked public account removes trade details on refresh',()=>{const b=browser('opaque');b.ui.setSnapshot(snapshot());assert.equal(b.document.querySelector('#detail').hidden,false);b.ui.setSnapshot({captured_at:new Date().toISOString(),accounts:[]});assert.equal(b.document.querySelector('#detail').hidden,true);assert.match(b.document.querySelector('#accounts').textContent,/no longer published/);});
