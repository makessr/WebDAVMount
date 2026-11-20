import {test} from 'node:test'
import assert from 'node:assert/strict'
import {remoteFeedback,recoveryFeedback} from '../src/connection-feedback.ts'

test('mounted drive without a directory read never claims remote availability',()=>{
 const state=remoteFeedback(true,true,'')
 assert.equal(state.label,'尚未检查');assert.equal(state.tone,'neutral')
 assert.match(state.detail,/挂载仅表示盘符就绪/)
 assert.equal(remoteFeedback(true,false,'').label,'尚未检查')
})
test('directory outcomes remain visible independently of transient success notices; cancellation is not a network failure',()=>{
 assert.equal(remoteFeedback(true,true,'最近目录读取成功').label,'最近读取成功')
 const failed=remoteFeedback(true,true,'最近读取失败：无法连接网盘，请检查网络')
 assert.equal(failed.label,'最近读取失败');assert.equal(failed.tone,'warning');assert.match(failed.detail,/无法连接/)
 assert.equal(remoteFeedback(true,true,'最近读取失败：操作已取消').label,'读取已取消')
 assert.equal(remoteFeedback(true,true,'最近读取失败：操作已取消').tone,'neutral')
 assert.equal(remoteFeedback(true,true,'unexpected').label,'尚未检查')
})
test('stopped engine overrides old read success and directs recovery before refresh',()=>{
 const state=remoteFeedback(false,true,'最近目录读取成功')
 assert.equal(state.label,'服务已停止');assert.equal(state.tone,'warning');assert.match(state.detail,/恢复挂载/)
})
test('recovery results stay per connection and distinguish failure, restored mount and retained mount',()=>{
 assert.equal(recoveryFeedback('恢复失败：盘符被占用').tone,'warning')
 assert.equal(recoveryFeedback('已恢复挂载').label,'挂载已恢复')
 const retained=recoveryFeedback('盘符保持挂载；请测试连接确认远端可访问')
 assert.equal(retained.label,'保持挂载');assert.equal(retained.tone,'neutral')
 assert.equal(recoveryFeedback('').label,'未参与恢复')
})
