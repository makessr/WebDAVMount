import {test} from 'node:test'
import assert from 'node:assert/strict'
import {columnDirection,nextColumnOrder} from '../src/directory-columns.ts'
test('column headers toggle their direction and choose useful defaults when changing columns',()=>{
 assert.equal(nextColumnOrder('nameAsc','name'),'nameDesc')
 assert.equal(nextColumnOrder('nameDesc','name'),'nameAsc')
 assert.equal(nextColumnOrder('nameAsc','size'),'sizeDesc')
 assert.equal(nextColumnOrder('sizeDesc','size'),'sizeAsc')
 assert.equal(nextColumnOrder('sizeAsc','modified'),'modifiedDesc')
 assert.equal(nextColumnOrder('modifiedDesc','modified'),'modifiedAsc')
 assert.equal(nextColumnOrder('modifiedAsc','name'),'nameAsc')
})
test('only the active header exposes its direction across every sort mode',()=>{
 for(const column of ['name','size','modified'])for(const suffix of ['Asc','Desc']){
  const order=column+suffix
  for(const header of ['name','size','modified'])assert.equal(columnDirection(order,header),header===column?(suffix==='Asc'?'ascending':'descending'):'none')
 }
})
