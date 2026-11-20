import test from 'node:test'
import assert from 'node:assert/strict'
import {spawnSync} from 'node:child_process'
import {fileURLToPath} from 'node:url'
test('build parser guard rejects repeated extglobs before compiling or matching',()=>{
 const root=fileURLToPath(new URL('..',import.meta.url))
 const script=`
 const assert=require('node:assert/strict');
 const p=require('./node_modules/picomatch');const parse=require('./node_modules/picomatch/lib/parse');
 for(const pattern of ['+(*)','+(*a)','*(a|aa)','**/+(*)','!+(*)','{x,+(*)}','+(a|aa)']){
   for(const run of [()=>p(pattern),()=>p.makeRe(pattern),()=>p.parse(pattern),()=>parse(pattern),()=>parse.fastpaths(pattern)])assert.throws(run,/rejects repeated extglob/);
 }
 assert.equal(p('src/**/*.vue')('src/components/测试.vue'),true);
 assert.equal(p('**/*.{ts,vue}')('src/App.vue'),true);
 assert.equal(p('**/*.ts')('src/App.vue'),false);
 `
 const r=spawnSync(process.execPath,['-e',script],{cwd:root,timeout:3000,encoding:'utf8'})
 assert.equal(r.status,0,r.stderr||r.error?.message)
})
