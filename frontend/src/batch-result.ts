export type BatchItem={id:string,name:string,drive:string,state:'success'|'skipped'|'failed'|'cancelled',message:string}
export type BatchReport={items:BatchItem[],cancelled:boolean}
export function batchSummary(report:BatchReport,mount:boolean,label=mount?'批量挂载':'批量卸载'):string {
  const count=(state:BatchItem['state'])=>report.items.filter(item=>item.state===state).length
  return `${label}：成功 ${count('success')}，跳过 ${count('skipped')}，失败 ${count('failed')}，未处理 ${count('cancelled')}${report.cancelled?'；已取消后续处理':''}`
}
export function shouldReadBatchDirectory(report:BatchReport,id:string,mount:boolean):boolean {
  return mount&&!report.cancelled&&report.items.some(item=>item.id===id&&item.state==='success')
}
