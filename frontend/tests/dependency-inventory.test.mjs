import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createHash} from 'node:crypto'
import {execFileSync} from 'node:child_process'
import {fileURLToPath} from 'node:url'
import {selectPackages, verifyInventory} from '../build/dependency-inventory.mjs'

const sha = x => createHash('sha256').update(x).digest('hex')
const tool = new URL('../build/dependency-inventory.mjs', import.meta.url)
function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'wdm-inventory-'))
  t.after(() => rmSync(root, {recursive:true, force:true}))
  mkdirSync(join(root, 'frontend/dist/assets'), {recursive:true})
  const lock = JSON.stringify({packages:{'':{},'node_modules/vue':{version:'1'},'node_modules/test-tool':{version:'2',dev:true}}})
  writeFileSync(join(root, 'frontend/package-lock.json'), lock)
  writeFileSync(join(root, 'frontend/dist/assets/main.js'), 'runtime')
  const inventory = {format:1, lockSha256:sha(lock), packages:[{key:'node_modules/vue',version:'1'}],files:[{name:'assets/main.js',sha256:sha('runtime')}]}
  writeFileSync(join(root, 'frontend/dist/frontend-dependencies.json'), JSON.stringify(inventory))
  const exe = join(root, 'candidate.exe');writeFileSync(exe, 'synthetic binary')
  const record = () => execFileSync(process.execPath, [fileURLToPath(tool), 'record',root,exe], {stdio:'pipe'})
  return {root, exe, inventory, record}
}
test('legacy executable retains production superset', t => {
  const f=fixture(t);assert.deepEqual(selectPackages(f.root,f.exe).packages,[{key:'node_modules/vue',version:'1'}])
})
test('record binds selected packages to executable and bundle', t => {
  const f=fixture(t);f.record();assert.equal(selectPackages(f.root,f.exe).mode,'bundle-inventory')
  assert.throws(f.record) // immutable receipt
})
test('changed executable is rejected', t => {
  const f=fixture(t);f.record();writeFileSync(f.exe,'different');assert.throws(()=>selectPackages(f.root,f.exe),/executable/)
})
test('changed assets are rejected', t => {
  const f=fixture(t);f.record();writeFileSync(join(f.root,'frontend/dist/assets/main.js'),'tampered');assert.throws(()=>selectPackages(f.root,f.exe),/bundle changed/)
})
test('changed lock version is rejected', t => {
  const f=fixture(t);writeFileSync(join(f.root,'frontend/package-lock.json'),'{}');assert.throws(()=>verifyInventory(f.root,f.inventory),/build inputs/)
})
test('path traversal and duplicate assets are rejected', t => {
  const f=fixture(t);const invalid=structuredClone(f.inventory);invalid.files[0].name='../outside';assert.throws(()=>verifyInventory(f.root,invalid),/Unsafe/)
  f.inventory.files.push(f.inventory.files[0]);assert.throws(()=>verifyInventory(f.root,f.inventory),/bundle changed/)
})
test('receipt cannot silently drop a package from the build inventory', t => {
  const f=fixture(t);f.record();const path=f.exe+'.frontend.json';const receipt=JSON.parse(readFileSync(path));receipt.inventory.packages=[];writeFileSync(path,JSON.stringify(receipt));assert.throws(()=>selectPackages(f.root,f.exe),/differs/)
})
test('unknown package version is rejected', t => {
  const f=fixture(t);f.inventory.packages[0].version='unknown';assert.throws(()=>verifyInventory(f.root,f.inventory),/package inventory/)
})
test('new unlisted files cannot enter the embedded bundle silently', t => {
  const f=fixture(t);writeFileSync(join(f.root,'frontend/dist/extra.js'),'new dependency');assert.throws(()=>verifyInventory(f.root,f.inventory),/Unlisted/)
})
