import {test} from 'node:test'
import assert from 'node:assert/strict'
import {createTaskRunner, errorMessage} from '../src/operation-feedback.ts'

const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
const setup=()=>{const state=Object.fromEntries(Object.entries({busy:false,canCancel:false,error:'',notice:''}).map(([k,value])=>[k,{value}]));return {state,...createTaskRunner(state)}}
test('backend error messages omit the runtime wrapper',()=>{
  const e=new Error('网络不可达');e.name='RuntimeError'
  assert.equal(errorMessage(e),'网络不可达')
  assert.equal(errorMessage('RuntimeError: 操作已取消'),'操作已取消')
})
for(const order of ['operation-first','cancel-first'])test(`cancellation ends with one final notice: ${order}`,async()=>{
  const {state,task,cancel}=setup(),operation=deferred(),reply=deferred()
  const running=task(()=>operation.promise,true)
  const cancelling=cancel(()=>reply.promise)
  assert.match(state.notice.value,/正在结束/)
  if(order==='cancel-first'){reply.resolve();await cancelling}
  operation.reject(new Error('操作已取消'));await running
  reply.resolve();await cancelling
  assert.equal(state.notice.value,'操作已取消')
  assert.equal(state.error.value,'')
  assert.equal(state.busy.value,false)
  assert.equal(state.canCancel.value,false)
})
test('late cancel failure cannot overwrite the next operation',async()=>{
  const {state,task,cancel}=setup(),operation=deferred(),reply=deferred(),next=deferred()
  const running=task(()=>operation.promise,true),cancelling=cancel(()=>reply.promise)
  operation.resolve();await running
  const second=task(()=>next.promise,true)
  reply.reject(new Error('旧请求失败'));await cancelling
  assert.equal(state.error.value,'');assert.equal(state.notice.value,'')
  next.resolve();await second
})
test('cancel loses race to successful completion without changing success',async()=>{
  const {state,task,cancel}=setup(),operation=deferred(),reply=deferred()
  const running=task(async()=>{await operation.promise;state.notice.value='连接成功'},true)
  const cancelling=cancel(()=>reply.promise)
  operation.resolve();await running;reply.resolve();await cancelling
  assert.equal(state.notice.value,'连接成功')
})
