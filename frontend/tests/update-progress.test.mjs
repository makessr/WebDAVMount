import {test} from 'node:test'
import assert from 'node:assert/strict'
import {updateProgress} from '../src/update-progress.ts'

test('download shows real bytes, speed, elapsed and estimated remaining time',()=>{
 const p=updateProgress({configured:true,state:'downloading',downloadedBytes:5*1024*1024,totalBytes:20*1024*1024,bytesPerSecond:1024*1024,elapsedSeconds:65,remainingSeconds:15})
 assert.equal(p.percent,25);assert.equal(p.size,'5.0 MB / 20.0 MB');assert.equal(p.speed,'1.0 MB/s');assert.equal(p.elapsed,'1分5秒');assert.equal(p.remaining,'约 15秒')
})
test('waiting and stalled transfers do not invent a rate or ETA',()=>{
 const p=updateProgress({configured:true,state:'downloading',totalBytes:1024,bytesPerSecond:0,elapsedSeconds:10})
 assert.equal(p.speed,'等待数据…');assert.equal(p.remaining,'计算中…');assert.equal(p.percent,0)
})
test('received bytes do not imply verification success',()=>{
 const p=updateProgress({configured:true,state:'verifying',downloadedBytes:100,totalBytes:100})
 assert.equal(p.percent,100);assert.equal(p.speed,'正在校验');assert.equal(p.remaining,'正在校验');assert.equal(p.visible,true)
})
test('completion, cancellation and retry do not show a stale active progress bar',()=>{
 for(const state of ['downloaded','available','error','idle'])assert.equal(updateProgress({configured:true,state,downloadedBytes:99,totalBytes:100}).visible,false)
 assert.equal(updateProgress({configured:true,state:'downloading',downloadedBytes:0,totalBytes:100}).percent,0)
})
test('range mode reports connections, reused bytes and bounded retry activity',()=>{
 const p=updateProgress({configured:true,state:'downloading',downloadMode:'ranges',connections:4,resumedBytes:8*1024*1024,retryCount:2,downloadedBytes:10*1024*1024,totalBytes:20*1024*1024,networkBytes:2*1024*1024})
 assert.equal(p.mode,'分段下载 · 4 个连接');assert.equal(p.resumed,'8.0 MB');assert.equal(p.retries,'已自动重试 2 次');assert.equal(p.percent,50)
 assert.equal(updateProgress({configured:true,state:'downloading',downloadMode:'sequential'}).mode,'普通下载')
})
