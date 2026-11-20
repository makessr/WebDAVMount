import {test} from 'node:test'
import assert from 'node:assert/strict'
import {canSwapDrives,previewDriveChanges} from '../src/connection-drives.ts'

const plan={
 rows:[{id:'one',name:'项目一',drive:'W:'},{id:'two',name:'项目二',drive:'X:'}],
 available:['V:','W:','X:'],
}

test('drive preview permits swaps and distinguishes unchanged assignments',()=>{
 assert.equal(previewDriveChanges(plan,{}).changed,0)
 const draft=previewDriveChanges(plan,{one:'X:',two:'W:'})
 assert.equal(canSwapDrives(plan),true)
 assert.deepEqual(draft.changes,[{id:'one',from:'W:',to:'X:'},{id:'two',from:'X:',to:'W:'}])
 assert.equal(draft.conflictIds.size,0)
})

test('duplicates and unavailable letters cannot be submitted even when a row is unchanged',()=>{
 assert.deepEqual([...previewDriveChanges(plan,{one:'X:'}).conflictIds],['one'])
 assert.deepEqual([...previewDriveChanges(plan,{one:'V:',two:'V:'}).conflictIds],['one','two'])
 const occupied={...plan,available:['W:']}
 assert.equal(canSwapDrives(occupied),false)
 assert.deepEqual([...previewDriveChanges(occupied,{one:'X:'}).conflictIds],['one'])
 assert.equal(previewDriveChanges(occupied,{}).conflictIds.size,0)
})
