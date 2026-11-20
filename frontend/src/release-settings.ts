export type UpdateState={configured:boolean,state:string,message?:string,currentVersion?:string,availableVersion?:string,downloadedPath?:string,cancelled?:boolean,downloadedBytes?:number,totalBytes?:number,bytesPerSecond?:number,elapsedSeconds?:number,remainingSeconds?:number,resumeAvailable?:boolean,resumedBytes?:number,connections?:number,retryCount?:number,downloadMode?:string,networkBytes?:number}
export type ReleaseCall=(method:string)=>Promise<unknown>
export const initialUpdate:UpdateState={configured:false,state:'disabled',message:'在线更新尚未开放，请使用安装包更新。'}
export function updateFeedback(state:UpdateState){
  return {message:state.configured?(state.message||'点击检查更新，不会在后台自动联网。'):'在线更新尚未开放，请使用安装包更新。',canCheck:state.configured,canDownload:state.configured&&['available','error'].includes(state.state)&&!!state.availableVersion}
}
export async function loadReleaseSettings(call:ReleaseCall){
  return await call('UpdateInfo') as UpdateState
}
export async function withReleaseStateRecovery<T>(action:()=>Promise<T>,refresh:()=>Promise<unknown>):Promise<T>{
  try{return await action()}
  catch(error){
    try{await refresh()}catch{}
    throw error
  }
}
export async function runReleaseAction(call:ReleaseCall,action:'check'|'download',update:UpdateState):Promise<{update?:UpdateState,notice:string}>{
  const view=updateFeedback(update)
  if(action==='check'?!view.canCheck:!view.canDownload)return {notice:view.message}
  const next=await call(action==='check'?'CheckForUpdates':'DownloadUpdate') as UpdateState
  if(next.cancelled)return {update:next,notice:'已取消更新操作'}
  return {update:next,notice:action==='download'&&next.downloadedPath?(next.state==='installing'?next.message||'安装程序已启动，正在退出客户端并停止传输…':`更新包已保存至：${next.downloadedPath}。${next.message||'可重试打开安装程序。'}`):next.message||'更新状态已刷新'}
}
