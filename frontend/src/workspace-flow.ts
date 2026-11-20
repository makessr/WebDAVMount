import type {DirectoryOrder} from './directory-view'
export type WorkspaceView={files:any[],path:string,loaded:boolean,query:string,page:number,order?:DirectoryOrder}
export function emptyWorkspace():WorkspaceView{return {files:[],path:'',loaded:false,query:'',page:1}}
export function breadcrumbItems(path:string){
 const parts=path.split('/').filter(Boolean)
 return parts.map((name,index)=>({name,path:parts.slice(0,index+1).join('/')}))
}
export function connectionDriveOptions(profiles:readonly {id?:string,drive:string}[],occupied:readonly string[],profileId='') {
 const used=new Set(profiles.filter(p=>!profileId||p.id!==profileId).map(p=>p.drive.toUpperCase()))
 const system=new Set(occupied.map(drive=>drive.toUpperCase()))
 return Array.from({length:23},(_,index)=>{
  const drive=String.fromCharCode(68+index)+':'
  return {drive,occupied:system.has(drive),configured:used.has(drive)}
 })
}
export function suggestDrive(profiles:readonly {drive:string}[],occupied:readonly string[]=[]):string {
 const options=connectionDriveOptions(profiles,occupied).reverse()
 return options.find(option=>!option.occupied&&!option.configured)?.drive??''
}
export async function saveConnectionFlow<T>(save:()=>Promise<T>,select:(profile:T)=>Promise<void>,connect:(profile:T)=>Promise<void>,mount:boolean){
 const profile=await save()
 await select(profile)
 if(mount)await connect(profile)
 return profile
}
export function fileSize(value:number):string{
 if(!Number.isFinite(value)||value<0)return '—'
 if(value<1024)return `${value} B`
 const units=['KB','MB','GB','TB'];let n=value/1024,index=0
 while(n>=1024&&index<units.length-1){n/=1024;index++}
 return `${n.toFixed(1)} ${units[index]}`
}
