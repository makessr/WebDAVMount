import {test} from 'node:test'
import assert from 'node:assert/strict'
import {connectionGroups,groupMoveAllowed} from '../src/connection-groups.ts'
import {dependencyInventory} from '../build/dependency-inventory.mjs'
import {mkdtempSync,writeFileSync,rmSync,realpathSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join,resolve} from 'node:path'

test('groups preserve peer order, place ungrouped last, and handle object-like names safely',()=>{
 const p=[{id:'a',name:'甲',drive:'W:'},{id:'b',name:'乙',drive:'V:',group:'__proto__'},{id:'c',name:'丙',drive:'U:',group:'工作'},{id:'d',name:'丁',drive:'T:',group:'__proto__'}]
 assert.deepEqual(connectionGroups(p).map(g=>[g.name,g.items.map(p=>p.id)]),[['__proto__',['b','d']],['工作',['c']],['',['a']]])
 assert.equal(groupMoveAllowed(p,'b',-1),false);assert.equal(groupMoveAllowed(p,'b',1),true);assert.equal(groupMoveAllowed(p,'d',1),false);assert.equal(groupMoveAllowed(p,'a',1),false);assert.equal(groupMoveAllowed(p,'missing',1),false)
 assert.equal(connectionGroups(p,'乙')[0].items[0].id,'b');assert.equal(connectionGroups(p,'工作')[0].items[0].id,'c');assert.equal(connectionGroups(p,'w:')[0].items[0].id,'a');assert.equal(connectionGroups(p,'missing').length,0)
})
test('Vue generated SFC helper retains exact plugin license while unknown helpers remain refused',()=>{
 const root=mkdtempSync(join(tmpdir(),'wdm-group-license-'))
 try{
  const packages=Object.fromEntries(['vue','vite','rolldown','@vitejs/plugin-vue'].map(n=>['node_modules/'+n,{version:'test'}]));writeFileSync(join(root,'package-lock.json'),JSON.stringify({packages}))
  const plugin=dependencyInventory();plugin.configResolved({root});let output
  const bundle=id=>({'test.js':{type:'chunk',code:'export {};',imports:[],dynamicImports:[],modules:{[id]:{}}}})
  plugin.generateBundle.call({emitFile(x){output=JSON.parse(x.source)}},{},bundle('\0plugin-vue:export-helper'))
  assert.ok(output.packages.some(p=>p.key==='node_modules/@vitejs/plugin-vue'));assert.ok(output.virtualModules.includes('\0plugin-vue:export-helper'))
  assert.throws(()=>plugin.generateBundle.call({emitFile(){}},{},bundle('\0plugin-vue:unreviewed')),/requires license review/)
 }finally{assert.equal(realpathSync(root),resolve(root));assert.ok(root.startsWith(join(tmpdir(),'wdm-group-license-')));rmSync(root,{recursive:true})}
})
