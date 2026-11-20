export type MountedSaveItem = {name:string, state:string, message?:string}
export type MountedSaveBatch = {folder:string, items:MountedSaveItem[], recordId?:string}
export type MountedSaveRecord = MountedSaveBatch & {
  id:string
  sourceId:string
  sourceName:string
  completedAt:string
  retryCount:number
  sourceMatches:boolean
  retried:boolean
}
export type MountedSaveHistoryReport = {entries:MountedSaveRecord[], warning?:string}

export const batchSaveStates:Record<string,string> = {
  saved:'已保存', skipped:'已跳过', failed:'保存失败', cancelled:'已取消', unprocessed:'未处理',
}

export function mountedSaveSummary(batch:MountedSaveBatch):string {
  return Object.entries(batchSaveStates)
    .map(([state,label])=>({label,count:batch.items.filter(item=>item.state===state).length}))
    .filter(item=>item.count>0)
    .map(item=>item.label+' '+item.count+' 项').join('，')
}
