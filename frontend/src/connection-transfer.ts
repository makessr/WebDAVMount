import type {StorageProvider} from './storage-providers'
export type ImportRow={provider?:StorageProvider,index:number,name:string,url:string,user:string,originalDrive:string,drive:string,status:'ready'|'duplicate'|'unavailable',message:string,group?:string,favoriteCount?:number,hasNote?:boolean}
export type ImportPreview={token:string,createdAt:string,expiresAt:string,includesPasswords:boolean,rows:ImportRow[],presetCount?:number}
export type PresetImportRow={index:number,name:string,memberCount:number,status:'ready'|'duplicate'|'missing',message:string}
export function eligibleIndices(preview:ImportPreview):number[]{return preview.rows.filter(row=>row.status==='ready').map(row=>row.index)}
export function selectedImportRows(preview:ImportPreview,indices:number[]):ImportRow[]{
  const selected=new Set(indices)
  return preview.rows.filter(row=>row.status==='ready'&&selected.has(row.index))
}
export function backupPasswordValid(password:string):boolean{
  const size=Array.from(password).length
  return size>=12&&size<=128&&new TextEncoder().encode(password).length<=512&&password.trim().length>0
}
export function transferTime(value:string):string{
  const date=new Date(value)
  return Number.isFinite(date.getTime())?date.toLocaleString('zh-CN',{hour12:false}):'未知'
}
