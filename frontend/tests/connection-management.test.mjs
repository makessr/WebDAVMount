import {test} from 'node:test'
import assert from 'node:assert/strict'
import {managementRows,orderManagementRows,visibleSelection,selectedManagementRows,managementChangeFeedback,mergeConnectionChecks} from '../src/connection-management.ts'
import {connectionGroups} from '../src/connection-groups.ts'
const profiles=[{id:'one',name:'工作盘',drive:'W:',group:'工作',provider:'onedrive',autoMount:true},{id:'two',name:'生活盘',drive:'V:',group:'生活',autoMount:false},{id:'three',name:'另一工作盘',drive:'U:',group:'工作',provider:'onedrive'},{id:'four',name:'无分组',drive:'T:'}]
const filter={query:'',group:null,provider:null,mount:'all',automatic:'all'}
test('management filters combine name/group/drive, mounted identity and automatic preference without reordering',()=>{
 assert.deepEqual(managementRows(profiles,['one'],filter),profiles)
 assert.deepEqual(managementRows(profiles,['one'],{...filter,group:'工作',mount:'unmounted',automatic:'off'}).map(p=>p.id),['three'])
 assert.deepEqual(managementRows(profiles,['one'],{...filter,query:'w:'}).map(p=>p.id),['one'])
 assert.deepEqual(managementRows(profiles,[],{...filter,query:'未分组'}).map(p=>p.id),['four'])
 assert.deepEqual(managementRows(profiles,['one'],{...filter,group:'',automatic:'off'}).map(p=>p.id),['four'])
 assert.equal(managementRows(profiles,[],{...filter,query:'missing'}).length,0)
})
test('selection is deduplicated and hidden/stale identities are removed; actions preserve displayed order',()=>{
 const rows=managementRows(profiles,[],{...filter,group:'工作'})
 assert.deepEqual(visibleSelection(['two','one','missing','one','three'],rows),['one','three'])
 assert.deepEqual(selectedManagementRows(rows,['three','one']).map(p=>p.id),['one','three'])
 assert.deepEqual(visibleSelection(['one'],[]),[])
 assert.deepEqual(profiles.map(p=>p.id),['one','two','three','four'])
})
test('provider filters compose with mount state and preserve legacy WebDAV connections',()=>{
 const rows=managementRows(profiles,['one'],{...filter,provider:'onedrive',mount:'mounted'})
 assert.deepEqual(visibleSelection(['one','two','three'],rows),['one'])
 assert.deepEqual(managementRows(profiles,[],{...filter,provider:'webdav'}).map(p=>p.id),['two','four'])
})
test('a later connection check replaces only its matched results and keeps profile order',()=>{
 const original=[{id:'three',state:'failed'},{id:'one',state:'success'},{id:'deleted',state:'failed'}]
 const updated=[{id:'three',state:'success'},{id:'two',state:'skipped'}]
 assert.deepEqual(mergeConnectionChecks(original,updated,profiles),[original[1],updated[1],updated[0]])
 assert.equal(original[0].state,'failed')
})
test('notes compose with manager filters and sidebar search; sorting preserves original rows',()=>{
 const rows=[
  {id:'ten',name:'项目10',note:'冷备份 Finance',drive:'Y:',group:'工作'},
  {id:'two',name:'项目2',note:'归档 Finance',drive:'X:',group:'工作'},
  {id:'other',name:'生活盘',note:'冷备份',drive:'W:',group:'个人'},
 ]
 assert.deepEqual(managementRows(rows,['ten'],{...filter,query:' finance ',mount:'mounted'}).map(p=>p.id),['ten'])
 assert.deepEqual(connectionGroups(rows,'冷备份').flatMap(g=>g.items.map(p=>p.id)),['ten','other'])
 assert.deepEqual(orderManagementRows(rows.slice(0,2),'name').map(p=>p.id),['two','ten'])
 assert.deepEqual(orderManagementRows(rows,'drive').map(p=>p.id),['other','two','ten'])
 assert.deepEqual(orderManagementRows(rows,'saved').map(p=>p.id),['ten','two','other'])
})
test('a refresh failure cannot turn a committed management change into a failed operation or trigger another write',async()=>{
 let calls=0
 assert.equal(await managementChangeFeedback('已删除 2 个本机连接。',async()=>{calls++}),'已删除 2 个本机连接。')
 const message=await managementChangeFeedback('连接副本已创建',async()=>{calls++;throw Error('synthetic-private-error')})
 assert.match(message,/连接副本已创建.*列表暂未刷新/);assert.ok(!message.includes('synthetic-private-error'));assert.equal(calls,2)
})
