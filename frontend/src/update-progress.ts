import type {UpdateState} from './release-settings'

function bytes(n:number){
  if(!Number.isFinite(n)||n<=0)return '0 B'
  if(n<1024)return `${Math.round(n)} B`
  if(n<1024*1024)return `${(n/1024).toFixed(1)} KB`
  return `${(n/1024/1024).toFixed(1)} MB`
}
function duration(n:number){
  const seconds=Math.max(0,Math.floor(Number.isFinite(n)?n:0))
  const hours=Math.floor(seconds/3600),minutes=Math.floor(seconds/60)%60
  return hours?`${hours}小时${minutes}分`:(minutes?`${minutes}分${seconds%60}秒`:`${seconds}秒`)
}
export function updateProgress(state:UpdateState){
  const downloaded=Math.max(0,state.downloadedBytes||0),total=Math.max(0,state.totalBytes||0)
  const verifying=state.state==='verifying'
  return {
    visible:state.state==='downloading'||verifying,
    percent:total?Math.min(100,Math.max(0,downloaded/total*100)):0,
    size:`${bytes(downloaded)} / ${total?bytes(total):'等待文件大小'}`,
    speed:verifying?'正在校验':((state.bytesPerSecond||0)>0?`${bytes(state.bytesPerSecond!)}/s`:'等待数据…'),
    elapsed:duration(state.elapsedSeconds||0),
    mode:state.downloadMode==='ranges'?`分段下载 · ${state.connections||1} 个连接`:state.downloadMode==='sequential'?'普通下载':state.downloadMode==='preparing'?'正在检查已保存分段…':'',
    resumed:(state.resumedBytes||0)>0?bytes(state.resumedBytes!):'',
    retries:(state.retryCount||0)>0?`已自动重试 ${state.retryCount} 次`:'',
    remaining:verifying?'正在校验':(typeof state.remainingSeconds==='number'&&Number.isFinite(state.remainingSeconds)?`约 ${duration(Math.ceil(state.remainingSeconds))}`:'计算中…')
  }
}
