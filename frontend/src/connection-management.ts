import type {GroupedProfile} from './connection-groups'
import type {StorageProvider} from './storage-providers'
export type ManagedProfile=GroupedProfile&{autoMount?:boolean,provider?:StorageProvider}
export type MountPreset = {id: string, name: string, connectionIDs: string[]}
export type ManagerFilter={query:string,group:string|null,provider:StorageProvider|null,mount:'all'|'mounted'|'unmounted',automatic:'all'|'on'|'off'}
export type ManagerOrder='saved'|'name'|'drive'
const connectionOrder=new Intl.Collator('zh-CN',{numeric:true,sensitivity:'base'})
export type ConnectionCheckItem={
 id:string
 name:string
 drive:string
 mount:string
 state:'success'|'failed'|'skipped'
 message:string
 checkedAt:string
}
export function managementRows<T extends ManagedProfile>(profiles:readonly T[],mounted:readonly string[],filter:ManagerFilter):T[]{
 const q=filter.query.trim().toLocaleLowerCase(),ids=new Set(mounted)
 return profiles.filter(p=>
  (filter.group===null||(p.group||'')===filter.group) &&
  (filter.provider===null||(p.provider||'webdav')===filter.provider) &&
  (filter.mount==='all'||ids.has(p.id)===(filter.mount==='mounted')) &&
  (filter.automatic==='all'||!!p.autoMount===(filter.automatic==='on')) &&
  (!q||[p.name,p.drive,p.group||'未分组',p.note||''].some(value=>value.toLocaleLowerCase().includes(q))))
}
export function orderManagementRows<T extends ManagedProfile>(rows:T[],order:ManagerOrder):T[]{
 if(order==='saved')return rows
 return [...rows].sort((a,b)=>connectionOrder.compare(a[order],b[order]))
}
export function visibleSelection(ids:readonly string[],rows:readonly ManagedProfile[]):string[]{const visible=new Set(rows.map(p=>p.id));return [...new Set(ids)].filter(id=>visible.has(id))}
export function selectedManagementRows<T extends ManagedProfile>(rows:readonly T[],ids:readonly string[]):T[]{const selected=new Set(ids);return rows.filter(p=>selected.has(p.id))}
export function mergeConnectionChecks(previous:readonly ConnectionCheckItem[],completed:readonly ConnectionCheckItem[],profiles:readonly ManagedProfile[]):ConnectionCheckItem[]{
 const byId=new Map([...previous,...completed].map(item=>[item.id,item]))
 return profiles.flatMap(profile=>{
  const item=byId.get(profile.id)
  return item?[item]:[]
 })
}
export async function managementChangeFeedback(message:string,reload:()=>Promise<void>):Promise<string>{
 try{await reload();return message}catch{return message+' 列表暂未刷新，请恢复引擎后刷新状态。'}
}
