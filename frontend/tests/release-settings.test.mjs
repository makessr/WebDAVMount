import {test} from 'node:test'
import assert from 'node:assert/strict'
import {initialUpdate,updateFeedback,loadReleaseSettings,runReleaseAction,withReleaseStateRecovery} from '../src/release-settings.ts'
test('a failed transfer retains a download action only when a release remains available',()=>{
 assert.equal(updateFeedback({configured:true,state:'error',availableVersion:'0.28.0',resumeAvailable:true}).canDownload,true)
 assert.equal(updateFeedback({configured:true,state:'error'}).canDownload,false)
 assert.equal(updateFeedback({configured:true,state:'verifying',availableVersion:'0.28.0'}).canDownload,false)
})

test('unconfigured updates never invoke native actions',async()=>{
  const call=async()=>{assert.fail('unconfigured action invoked')}
  assert.equal(updateFeedback(initialUpdate).canCheck,false)
  assert.equal(updateFeedback({...initialUpdate,state:'available',availableVersion:'99.0.0'}).canDownload,false)
  for(const action of ['check','download'])await runReleaseAction(call,action,initialUpdate)
})
test('status loading only queries local status methods',async()=>{
  const calls=[]
  const loaded=await loadReleaseSettings(async method=>{calls.push(method);return initialUpdate})
  assert.deepEqual(calls,['UpdateInfo']);assert.deepEqual(loaded,initialUpdate)
})
test('checking and downloading call distinct APIs and download never installs',async()=>{
  const calls=[]
  const available={configured:true,state:'available',availableVersion:'0.25.0'}
  const call=async method=>{calls.push(method);return method==='CheckForUpdates'?available:{...available,state:'downloaded',downloadedPath:'C:\\fixture\\setup.exe'}}
  const checked=await runReleaseAction(call,'check',{configured:true,state:'idle'})
  assert.equal(checked.update.availableVersion,'0.25.0')
  const result=await runReleaseAction(call,'download',checked.update)
  assert.deepEqual(calls,['CheckForUpdates','DownloadUpdate']);assert.match(result.notice,/C:\\fixture\\setup.exe/);assert.match(result.notice,/已保存/)
})
test('cancelled download does not announce completion',async()=>{
  const available={configured:true,state:'available',availableVersion:'0.25.0'}
  const result=await runReleaseAction(async()=>({...available,cancelled:true}),'download',available)
  assert.equal(result.notice,'已取消更新操作')
})

test('failed or cancelled action refreshes state before preserving original error',async()=>{
  for(const original of [Error('download failed'),Error('操作已取消')]){
    let current={configured:true,state:'available',availableVersion:'0.25.0'}
    await assert.rejects(withReleaseStateRecovery(async()=>{throw original},async()=>{current={configured:true,state:'idle'}}),error=>error===original)
    assert.equal(updateFeedback(current).canDownload,false)
  }
})
test('state refresh failure cannot hide the original action error',async()=>{
  const original=Error('invalid update')
  await assert.rejects(withReleaseStateRecovery(async()=>{throw original},async()=>{throw Error('refresh unavailable')}),error=>error===original)
})
test('successful action keeps returned state and notice without extra refresh',async()=>{
  const result={update:{configured:true,state:'downloaded',downloadedPath:'fixture'},notice:'saved'}
  assert.equal(await withReleaseStateRecovery(async()=>result,async()=>assert.fail('unexpected refresh')),result)
})

test('successful automatic installer handoff uses backend shutdown feedback',async()=>{const next={configured:true,state:'installing',downloadedPath:'C:/fixture/setup.exe',message:'安装程序已启动，正在退出客户端并停止传输…'};const result=await runReleaseAction(async()=>next,'download',{configured:true,state:'available',availableVersion:'999.0.0'});assert.equal(result.notice,next.message);assert.equal(result.update.state,'installing')})
