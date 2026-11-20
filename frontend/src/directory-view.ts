import {modifiedTimestamp} from './directory-modified.ts'
export type DirectoryEntry = {Name:string, Path:string, Size:number, IsDir:boolean,ModTime?:string|null}
export type DirectoryOrder = 'nameAsc'|'nameDesc'|'sizeAsc'|'sizeDesc'|'modifiedAsc'|'modifiedDesc'

const names = new Intl.Collator('zh-CN', {numeric:true, sensitivity:'base'})
const folded = (value:string) => value.normalize('NFC').toLocaleLowerCase('zh-CN')

export function sortDirectory<T extends DirectoryEntry>(entries:readonly T[], order:DirectoryOrder):T[] {
  const byModified=order==='modifiedAsc'||order==='modifiedDesc'
  const times=byModified?new Map(entries.map(entry=>[entry,modifiedTimestamp(entry.ModTime)])):undefined
  return [...entries].sort((a,b)=>{
    const directories = Number(b.IsDir)-Number(a.IsDir)
    if(directories)return directories
    if(times){
      const aTime=times.get(a),bTime=times.get(b)
      if((aTime===undefined)!==(bTime===undefined))return aTime===undefined?1:-1
      if(aTime!==undefined&&bTime!==undefined&&aTime!==bTime)return order==='modifiedAsc'?aTime-bTime:bTime-aTime
    }
    if(!a.IsDir && (order==='sizeAsc'||order==='sizeDesc')){
      const aKnown=Number.isFinite(a.Size)&&a.Size>=0,bKnown=Number.isFinite(b.Size)&&b.Size>=0
      if(aKnown!==bKnown)return aKnown?-1:1
      if(aKnown&&a.Size!==b.Size)return order==='sizeAsc'?a.Size-b.Size:b.Size-a.Size
    }
    const byName=names.compare(a.Name,b.Name)||names.compare(a.Path,b.Path)
    return order==='nameDesc'?-byName:byName
  })
}

export function filterDirectory<T extends DirectoryEntry>(entries:readonly T[], query:string):readonly T[] {
  const needle=folded(query.trim())
  return needle?entries.filter(entry=>folded(entry.Name).includes(needle)):entries
}
