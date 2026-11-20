import {test} from 'node:test'
import assert from 'node:assert/strict'
import {batchSummary,shouldReadBatchDirectory} from '../src/batch-result.ts'

test('batch summary distinguishes failures, existing mounts and cancelled entries',()=>{
 const report={items:['success','failed','skipped','cancelled'].map((state,i)=>({id:String(i),state})),cancelled:true}
 assert.equal(batchSummary(report,true),'批量挂载：成功 1，跳过 1，失败 1，未处理 1；已取消后续处理')
 assert.match(batchSummary({items:[],cancelled:false},false),/^批量卸载：成功 0/)
})
test('only a newly mounted selected connection gets a directory refresh, without cancellation',()=>{
 const report={items:[{id:'new',state:'success'},{id:'existing',state:'skipped'},{id:'bad',state:'failed'}],cancelled:false}
 assert.equal(shouldReadBatchDirectory(report,'new',true),true)
 for(const id of ['existing','bad','missing'])assert.equal(shouldReadBatchDirectory(report,id,true),false)
 assert.equal(shouldReadBatchDirectory(report,'new',false),false)
 assert.equal(shouldReadBatchDirectory({...report,cancelled:true},'new',true),false)
})
