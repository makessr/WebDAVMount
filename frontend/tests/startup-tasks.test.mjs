import test from 'node:test'
import assert from 'node:assert/strict'
import {startupSummary,startupLabel} from '../src/startup-tasks.ts'
test('startup tasks distinguish waiting from handled tasks and retry only failures or cancellations',()=>{
 const items=['queued','running','mounted','failed','cancelled','skipped'].map((state,i)=>({id:String(i),state}))
 assert.deepEqual(startupSummary({active:true,items}),{retry:2,done:4,total:6})
 assert.deepEqual(startupSummary({active:false,items:[]}),{retry:0,done:0,total:0})
})
test('task labels describe the launch result rather than claiming current drive ownership',()=>{
 assert.equal(startupLabel('mounted'),'成功');assert.equal(startupLabel('cancelled'),'已停止');assert.equal(startupLabel('unexpected'),'待确认')
})
