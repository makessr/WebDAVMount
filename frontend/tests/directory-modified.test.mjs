import {test} from 'node:test'
import assert from 'node:assert/strict'
import {modifiedTimestamp,formatModified} from '../src/directory-modified.ts'
import {sortDirectory} from '../src/directory-view.ts'
import {emptyHistory,recordVisit,planHistoryMove} from '../src/directory-history.ts'

test('RFC3339 offsets and fractions identify the same instant; local Chinese date format is explicit',()=>{
 const utc='2026-11-16T00:30:12.123456789Z',offset='2026-11-16T08:30:12.123+08:00'
 assert.equal(modifiedTimestamp(utc),modifiedTimestamp(offset))
 assert.equal(formatModified(utc,'Asia/Shanghai'),'2026-11-16 08:30')
 assert.equal(formatModified(utc,'UTC'),'2026-11-16 00:30')
})
test('missing, zero, malformed, ambiguous and invalid calendar timestamps stay unknown',()=>{
 for(const value of [undefined,null,0,'','0001-01-01T00:00:00Z','2026-11-16','2026-11-16T01:02:03','2026-02-29T00:00:00Z','2024-02-30T00:00:00Z','2026-13-01T00:00:00Z','2026-11-16T24:00:00Z','2026-11-16T00:60:00Z','2026-11-16T00:00:00+24:00','arbitrary']){
  assert.equal(modifiedTimestamp(value),undefined,String(value));assert.equal(formatModified(value),'—')
 }
 assert.notEqual(modifiedTimestamp('2024-02-29T00:00:00Z'),undefined)
 assert.equal(modifiedTimestamp('1970-01-01T00:00:00Z'),0)
})
test('modified-time order keeps folders first and unknowns last in both directions; ties are natural',()=>{
 const item=(Name,ModTime,IsDir=false)=>({Name,Path:Name,Size:1,IsDir,ModTime})
 const source=[item('unknown',null),item('new','2026-11-16T10:00:00Z'),item('old','2025-01-01T00:00:00Z'),item('tie10','2026-11-16T18:00:00+08:00'),item('tie2','2026-11-16T10:00:00Z'),item('dir-old','2025-01-01T00:00:00Z',true),item('dir-new','2026-11-16T10:00:00Z',true),item('dir-unknown',null,true)]
 const before=structuredClone(source)
 assert.deepEqual(sortDirectory(source,'modifiedDesc').map(x=>x.Name),['dir-new','dir-old','dir-unknown','new','tie2','tie10','old','unknown'])
 assert.deepEqual(sortDirectory(source,'modifiedAsc').map(x=>x.Name),['dir-old','dir-new','dir-unknown','old','new','tie2','tie10','unknown'])
 assert.deepEqual(source,before)
})
test('navigation retains the modified-time order with query and page',()=>{
 const root={path:'',query:'文件',page:2,order:'modifiedDesc'},child={path:'child',query:'',page:1,order:'modifiedAsc'}
 const history=recordVisit(recordVisit(emptyHistory(),undefined,root),root,child)
 assert.deepEqual(planHistoryMove(history,child,-1).location,root)
 assert.deepEqual(planHistoryMove(planHistoryMove(history,child,-1).state,root,1).location,child)
})
