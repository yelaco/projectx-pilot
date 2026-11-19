import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {parseHTML} from 'linkedom';
const source=fs.readFileSync(new URL('../internal/publicstatus/install.js',import.meta.url),'utf8');
function browser(installed=false){const {document}=parseHTML('<html><body><button id="install" hidden></button><p id="install-help" hidden></p></body></html>');const handlers={};const media={matches:installed,addEventListener:()=>{}};const window={matchMedia:()=>media,addEventListener:(name,fn)=>{handlers[name]=fn;}};const context={document,window};vm.createContext(context);vm.runInContext(source,context);return {document,handlers,button:document.getElementById('install'),help:document.getElementById('install-help')};}
test('install fallback explains browser menu without auto prompting',()=>{const b=browser();assert.equal(b.button.hidden,false);b.button.click();assert.equal(b.help.hidden,false);assert.equal(browser(true).button.hidden,true);});
test('native installation requires a button click and accepted install hides control',async()=>{const b=browser();let calls=0;b.handlers.beforeinstallprompt({preventDefault(){},prompt:async()=>{calls++;},userChoice:Promise.resolve({outcome:'accepted'})});assert.equal(calls,0);b.button.click();await new Promise(resolve=>setImmediate(resolve));assert.equal(calls,1);assert.equal(b.button.hidden,true);assert.equal(b.button.disabled,false);});
test('failed installation shows fallback; appinstalled hides both controls',async()=>{const b=browser();b.handlers.beforeinstallprompt({preventDefault(){},prompt:async()=>{throw Error('unsupported');}});b.button.click();await new Promise(resolve=>setImmediate(resolve));assert.equal(b.help.hidden,false);assert.equal(b.button.disabled,false);b.handlers.appinstalled();assert.equal(b.button.hidden,true);assert.equal(b.help.hidden,true);});
