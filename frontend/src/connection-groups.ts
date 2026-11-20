export type GroupedProfile={id:string,name:string,drive:string,group?:string,note?:string,presetKey?:string}
export function mountPresetKey(profile:GroupedProfile){return profile.presetKey||profile.id}
export function connectionGroups<T extends GroupedProfile>(profiles:readonly T[],query=''){
 const groups=new Map<string,T[]>()
 const q=query.trim().toLocaleLowerCase()
 for(const p of profiles){const name=p.group||'';if(q&&![p.name,name,p.drive,p.note||''].some(value=>value.toLocaleLowerCase().includes(q)))continue;const items=groups.get(name)||[];items.push(p);groups.set(name,items)}
 const result=[...groups].filter(([name])=>name!=='').map(([name,items])=>({name,label:name,items}))
 const ungrouped=groups.get('');if(ungrouped)result.push({name:'',label:'未分组',items:ungrouped})
 return result
}
export function groupMoveAllowed<T extends GroupedProfile>(profiles:readonly T[],id:string,direction:-1|1){
 const p=profiles.find(p=>p.id===id);if(!p)return false
 const peers=profiles.filter(x=>(x.group||'')===(p.group||'')),index=peers.findIndex(x=>x.id===id)
 return index+direction>=0&&index+direction<peers.length
}
