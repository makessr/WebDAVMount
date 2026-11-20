export type StartupTask={id:string,name:string,drive:string,state:string,message:string}
export type StartupTasks={active:boolean,retrying?:boolean,stopping?:boolean,presetName?:string,warning?:string,items:StartupTask[]}
export function startupSummary(report:StartupTasks){
 const retry=report.items.filter(x=>x.state==='failed'||x.state==='cancelled').length
 const done=report.items.filter(x=>!['queued','running'].includes(x.state)).length
 return {retry,done,total:report.items.length}
}
export function startupLabel(state:string):string{
 return ({queued:'等待',running:'进行中',mounted:'成功',failed:'失败',cancelled:'已停止',skipped:'已跳过'} as Record<string,string>)[state]||'待确认'
}
