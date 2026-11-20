import {readFileSync, writeFileSync, existsSync, readdirSync} from 'node:fs'
import {resolve, relative, isAbsolute} from 'node:path'
import {createHash} from 'node:crypto'
import {fileURLToPath} from 'node:url'

const hash = data => createHash('sha256').update(data).digest('hex')
const read = path => JSON.parse(readFileSync(path, 'utf8'))
const safeFile = (root, name) => {
  if (!name || name.includes('\\') || name.includes(':') || name.split('/').some(x => !x || x === '.' || x === '..')) throw Error('Unsafe bundle path')
  const path = resolve(root, name)
  if (isAbsolute(relative(root, path)) || relative(root, path).startsWith('..')) throw Error('Unsafe bundle path')
  return path
}

// Include even zero-length module entries conservatively. Generated runtime
// helpers are covered by retaining both bundlers and their nested notices.
export function dependencyInventory() {
  let root
  return {
    name: 'webdavmount-dependency-inventory', apply: 'build', enforce: 'post',
    configResolved(config) { root = config.root },
    generateBundle(_, bundle) {
      const lockBytes = readFileSync(resolve(root, 'package-lock.json'))
      const lock = JSON.parse(lockBytes)
      const keys = new Set(['node_modules/vue', 'node_modules/vite', 'node_modules/rolldown'])
      const virtualModules = new Set()
      const files = []
      for (const [name, output] of Object.entries(bundle)) {
        const bytes = output.type === 'chunk' ? output.code : output.source
        files.push({name, sha256: hash(bytes)})
        if (output.type !== 'chunk') continue
        if ([...output.imports, ...output.dynamicImports].some(x => !bundle[x])) throw Error('External runtime dependency requires review')
        for (const id of Object.keys(output.modules)) {
          const normalized = id.replaceAll('\\', '/')
          if (normalized.includes('/node_modules/')) {
            const local = relative(root, id.split('?')[0]).replaceAll('\\', '/')
            const match = local.match(/^(node_modules\/(?:.*\/node_modules\/)?(?:@[^/]+\/)?[^/]+)(?:\/|$)/)
            if (!match || !lock.packages[match[1]]) throw Error('Unmapped bundled dependency: ' + normalized)
            keys.add(match[1])
          } else if (id.includes('\0') || !isAbsolute(id)) {
            if (id === '\0plugin-vue:export-helper') {
              // Vue's generated SFC option/scope helper comes from this exact
              // locked package; retain its MIT attribution with the bundle.
              keys.add('node_modules/@vitejs/plugin-vue')
            } else if (id !== '\0vite/modulepreload-polyfill.js' && !/^\0?rolldown[:/]runtime/.test(id)) throw Error('Unknown generated runtime requires license review: ' + id)
            // No workstation paths are written into the public inventory.
            virtualModules.add(normalized.replaceAll(root.replaceAll('\\', '/'), '<frontend>'))
          } else if (relative(root, id).startsWith('..')) throw Error('Bundled source outside frontend requires review')
        }
      }
      const packages = [...keys].sort().map(key => {
        const value = lock.packages[key]
        if (!value?.version) throw Error('Missing locked dependency: ' + key)
        return {key, version: value.version}
      })
      this.emitFile({type: 'asset', fileName: 'frontend-dependencies.json', source: JSON.stringify({format: 1, lockSha256: hash(lockBytes), packages, virtualModules: [...virtualModules].sort(), files: files.sort((a,b) => a.name.localeCompare(b.name))}, null, 2)})
    }
  }
}

export function verifyInventory(project, inventory) {
  const root = resolve(project, 'frontend')
  const lockBytes = readFileSync(resolve(root, 'package-lock.json'))
  const lock = JSON.parse(lockBytes)
  if (inventory.format !== 1 || inventory.lockSha256 !== hash(lockBytes) || !inventory.packages?.length || !inventory.files?.length) throw Error('Frontend inventory does not match build inputs')
  const keys = new Set()
  for (const item of inventory.packages) {
    if (keys.has(item.key) || !item.key.startsWith('node_modules/') || lock.packages[item.key]?.version !== item.version) throw Error('Invalid frontend package inventory')
    safeFile(root, item.key)
    keys.add(item.key)
  }
  const files = new Set()
  for (const item of inventory.files) {
    if (files.has(item.name) || hash(readFileSync(safeFile(resolve(root, 'dist'), item.name))) !== item.sha256) throw Error('Frontend bundle changed')
    files.add(item.name)
  }
  for (const entry of readdirSync(resolve(root, 'dist'), {recursive:true, withFileTypes:true})) {
    if (!entry.isFile()) continue
    const name = relative(resolve(root, 'dist'), resolve(entry.parentPath, entry.name)).replaceAll('\\', '/')
    if (name !== 'frontend-dependencies.json' && !files.has(name)) throw Error('Unlisted frontend bundle file')
  }
  return inventory.packages
}

export function selectPackages(project, exe) {
  const receiptPath = exe + '.frontend.json'
  if (!existsSync(receiptPath)) {
    const lock = read(resolve(project, 'frontend/package-lock.json'))
    return {mode: 'legacy-production-superset', packages: Object.entries(lock.packages).filter(([key,p]) => key && !p.dev).map(([key,p]) => ({key, version:p.version}))}
  }
  const receipt = read(receiptPath)
  if (receipt.binarySha256 !== hash(readFileSync(exe))) throw Error('Frontend receipt does not match executable')
  if (JSON.stringify(read(resolve(project, 'frontend/dist/frontend-dependencies.json'))) !== JSON.stringify(receipt.inventory)) throw Error('Frontend receipt inventory differs from bundle inventory')
  verifyInventory(project, receipt.inventory)
  if (hash(readFileSync(resolve(project, 'frontend/dist/frontend-dependencies.json'))) !== receipt.inventorySha256) throw Error('Frontend inventory changed')
  return {mode: 'bundle-inventory', packages: receipt.inventory.packages, receipt: receiptPath}
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [action, project, exe] = process.argv.slice(2)
  if (action === 'record') {
    const inventoryBytes = readFileSync(resolve(project, 'frontend/dist/frontend-dependencies.json'))
    const inventory = JSON.parse(inventoryBytes)
    verifyInventory(project, inventory)
    writeFileSync(exe + '.frontend.json', JSON.stringify({binarySha256: hash(readFileSync(exe)), inventorySha256: hash(inventoryBytes), inventory}, null, 2), {flag:'wx'})
  } else if (action === 'select') {
    process.stdout.write(JSON.stringify(selectPackages(project, exe)))
  } else throw Error('Unknown inventory operation')
}
