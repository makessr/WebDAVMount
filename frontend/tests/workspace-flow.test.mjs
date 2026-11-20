import {test} from 'node:test'
import assert from 'node:assert/strict'
import {breadcrumbItems,connectionDriveOptions,suggestDrive,saveConnectionFlow,fileSize} from '../src/workspace-flow.ts'
test('breadcrumbs retain literal Chinese and special-character directory names',()=>{
 assert.deepEqual(breadcrumbItems('中文 # %/A&B+'),[{name:'中文 # %',path:'中文 # %'},{name:'A&B+',path:'中文 # %/A&B+'}])
 assert.deepEqual(breadcrumbItems(''),[])
})
test('new connection suggests an unoccupied and unconfigured drive letter',()=>{
 assert.equal(suggestDrive([{drive:'z:'},{drive:'Y:'}]),'X:')
 assert.equal(suggestDrive([]),'Z:')
 assert.equal(suggestDrive([{drive:'y:'}],['z:']),'X:')
 const all=connectionDriveOptions([],[]).map(option=>option.drive)
 assert.equal(suggestDrive([],all),'')
 assert.equal(suggestDrive(all.map(drive=>({drive})),[]),'')
})
test('editing a drive distinguishes system occupancy and another saved profile',()=>{
 const profiles=[{id:'editing',drive:'W:'},{id:'other',drive:'x:'}]
 const options=connectionDriveOptions(profiles,['Z:'],'editing')
 assert.deepEqual(options.find(option=>option.drive==='W:'),{drive:'W:',occupied:false,configured:false})
 assert.deepEqual(options.find(option=>option.drive==='X:'),{drive:'X:',occupied:false,configured:true})
 assert.deepEqual(options.find(option=>option.drive==='Z:'),{drive:'Z:',occupied:true,configured:false})
})
test('save and mount selects the saved connection before mounting; save-only does not mount',async()=>{
 const events=[],p={id:'new'}
 const run=mount=>saveConnectionFlow(async()=>{events.push('save');return p},async value=>{assert.equal(value,p);events.push('select')},async value=>{assert.equal(value,p);events.push('mount')},mount)
 await run(true);assert.deepEqual(events,['save','select','mount'])
 events.length=0;await run(false);assert.deepEqual(events,['save','select'])
})
test('save failure cannot mount; mount failure leaves the saved selection intact',async()=>{
 let selected='',mounts=0
 await assert.rejects(saveConnectionFlow(async()=>{throw Error('save')},async()=>{selected='new'},async()=>{mounts++},true),/save/)
 assert.equal(selected,'');assert.equal(mounts,0)
 await assert.rejects(saveConnectionFlow(async()=>({id:'new'}),async p=>{selected=p.id},async()=>{throw Error('mount')},true),/mount/)
 assert.equal(selected,'new')
})
test('file sizes include GB and unknown values',()=>{
 assert.equal(fileSize(1<<30),'1.0 GB');assert.equal(fileSize(0),'0 B');assert.equal(fileSize(-1),'—')
})
