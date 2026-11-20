import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mountAndRead} from '../src/mount-flow.ts'

test('successful mount reads the directory afterwards',async()=>{
  const calls=[]
  const result=await mountAndRead(async()=>{calls.push('mount')},async()=>{calls.push('read')})
  assert.deepEqual(calls,['mount','read']);assert.equal(result.directoryLoaded,true)
})
test('failed mount never reads a directory',async()=>{
  let read=false
  await assert.rejects(mountAndRead(async()=>{throw Error('mount failed')},async()=>{read=true}))
  assert.equal(read,false)
})
test('directory failure preserves successful mount outcome',async()=>{
  const error=Error('read failed')
  const result=await mountAndRead(async()=>{},async()=>{throw error})
  assert.equal(result.directoryLoaded,false);assert.equal(result.error,error)
})

for(const error of [Error('操作已取消'),'RuntimeError: 操作已取消'])test(`cancelled automatic directory read keeps successful mount: ${String(error)}`,async()=>{
  const result=await mountAndRead(async()=>{},async()=>{throw error})
  assert.equal(result.directoryLoaded,false)
  assert.equal(result.cancelled,true)
  assert.equal(result.error,error)
})

test('real directory failure is not treated as a cancellation',async()=>{
  const result=await mountAndRead(async()=>{},async()=>{throw Error('网络不可达')})
  assert.equal(result.cancelled,false)
})
