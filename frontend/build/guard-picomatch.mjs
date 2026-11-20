// Enforce the desktop build's supported glob syntax. GitHub withdrew
// GHSA-fc5q-39xc-vjpf on 2026-11-18. Keep this version-pinned input check.
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import {fileURLToPath} from 'node:url'
const frontend=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..')
const lock=JSON.parse(fs.readFileSync(path.join(frontend,'package-lock.json'),'utf8'))
const copies=Object.entries(lock.packages).filter(([key])=>key.endsWith('/picomatch'))
if(copies.length!==1||copies[0][0]!=='node_modules/picomatch'||copies[0][1].version!=='4.0.7')throw new Error('picomatch dependency layout changed; review every parser before building')
const original='4e1e05447668aa1e94366c5516a2bcd8f5fff800cc92b1d0cd7d77703b8618c4'
const file=path.join(frontend,'node_modules/picomatch/lib/parse.js')
const guard="\n  // WebDAVMount build mitigation: repeated extglobs are not supported.\n  if (typeof input === 'string' && /[+*]\\(/.test(input)) {\n    throw new SyntaxError('WebDAVMount build rejects repeated extglob syntax');\n  }\n"
function patched(source){
  for(const entry of ['const parse = (input, options) => {','parse.fastpaths = (input, options) => {']){
    if(source.split(entry).length!==2)throw new Error('picomatch parser entry changed; review mitigation')
    source=source.replace(entry,entry+guard)
  }
  return source
}
const source=fs.readFileSync(file,'utf8')
const hash=crypto.createHash('sha256').update(source).digest('hex')
if(hash===original){
  fs.writeFileSync(file,patched(source))
}else{
  const clean=source.split(guard).join('')
  if(crypto.createHash('sha256').update(clean).digest('hex')!==original||source!==patched(clean))throw new Error('Unexpected picomatch bytes; no patch applied')
}
console.log('picomatch build input guard verified')
