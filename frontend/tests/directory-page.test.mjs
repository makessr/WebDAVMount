import {test} from 'node:test'
import assert from 'node:assert/strict'
import {directoryPage} from '../src/directory-page.ts'

test('all 10001 entries can be reached once, including the final partial page',()=>{
  const entries=Array.from({length:10001},(_,i)=>i),seen=[]
  const first=directoryPage(entries,1)
  assert.equal(first.pages,51)
  for(let n=1;n<=first.pages;n++){
    const page=directoryPage(entries,n)
    assert.ok(page.items.length<=200)
    assert.equal(page.start,seen.length+1)
    seen.push(...page.items)
    assert.equal(page.end,seen.length)
  }
  assert.deepEqual(seen,entries)
  assert.deepEqual(entries,Array.from({length:10001},(_,i)=>i))
})
test('page resets safely when a smaller or empty directory replaces the list',()=>{
  const small=directoryPage(['nested'],51)
  assert.equal(small.page,1);assert.deepEqual(small.items,['nested'])
  const empty=directoryPage([],51)
  assert.equal(empty.pages,1);assert.equal(empty.page,1)
  assert.equal(empty.start,0);assert.equal(empty.end,0)
})
test('invalid pages stay in bounds',()=>{
  const items=Array.from({length:201},(_,i)=>i)
  for(const n of [NaN,Infinity,-1,0])assert.equal(directoryPage(items,n).page,1)
  assert.equal(directoryPage(items,999).page,2)
  assert.equal(directoryPage(items,1.5).page,1)
})
