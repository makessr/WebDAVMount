import {test} from 'node:test'
import assert from 'node:assert/strict'
import {emptyHistory,recordVisit,planHistoryMove} from '../src/directory-history.ts'

const at=(path,query='',page=1,order='nameAsc')=>({path,query,page,order})
test('back and forward restore literal Chinese paths, search, sort and page without retaining file lists',()=>{
 let h=recordVisit(emptyHistory(),undefined,at(''))
 h=recordVisit(h,at('','文件',2,'sizeDesc'),at('项目 # %/A&B+'))
 const back=planHistoryMove(h,at('项目 # %/A&B+'),-1)
 assert.deepEqual(back.location,at('','文件',2,'sizeDesc'))
 const forward=planHistoryMove(back.state,back.location,1)
 assert.equal(forward.location.path,'项目 # %/A&B+')
 assert.equal(Object.hasOwn(forward.location,'files'),false)
})
test('refresh does not append a history entry or discard the forward branch',()=>{
 let h=recordVisit(emptyHistory(),undefined,at(''))
 h=recordVisit(h,at(''),at('child'))
 const back=planHistoryMove(h,at('child'),-1)
 const refreshed=recordVisit(back.state,back.location,at('','x',1))
 assert.equal(refreshed.entries.length,2);assert.equal(refreshed.index,0)
 assert.equal(planHistoryMove(refreshed,at('','x'),1).location.path,'child')
})
test('failed/cancelled navigation plans do not mutate current state; new navigation discards only its own forward branch',()=>{
 let h=recordVisit(emptyHistory(),undefined,at(''))
 h=recordVisit(h,at(''),at('A'))
 const before=structuredClone(h)
 const back=planHistoryMove(h,at('A','query',3),-1)
 assert.deepEqual(h,before)
 assert.equal(back.state.entries[1].page,3)
 const branched=recordVisit(back.state,back.location,at('B'))
 assert.deepEqual(branched.entries.map(x=>x.path),['','B'])
 assert.deepEqual(h,before)
})
test('histories are bounded and separate connections cannot change each other; edges have no destination',()=>{
 let a=emptyHistory(),b=recordVisit(emptyHistory(),undefined,at('B'))
 for(let i=0;i<130;i++)a=recordVisit(a,i?at(String(i-1)):undefined,at(String(i)))
 assert.equal(a.entries.length,100);assert.equal(a.index,99);assert.equal(a.entries[0].path,'30')
 assert.equal(planHistoryMove(a,at('129'),1),undefined)
 assert.equal(planHistoryMove(b,at('B'),-1),undefined)
 assert.deepEqual(b.entries,[at('B')])
})
