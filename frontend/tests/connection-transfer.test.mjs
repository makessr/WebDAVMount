import {test} from 'node:test'
import assert from 'node:assert/strict'
import {eligibleIndices,selectedImportRows,backupPasswordValid,transferTime} from '../src/connection-transfer.ts'
test('only ready rows can be selected and duplicate selections do not add rows',()=>{
 const preview={rows:[{index:0,status:'ready'},{index:1,status:'duplicate'},{index:2,status:'unavailable'},{index:3,status:'ready'}]}
 assert.deepEqual(eligibleIndices(preview),[0,3])
 assert.deepEqual(selectedImportRows(preview,[3,1,2,3,99]).map(r=>r.index),[3])
})
test('password policy counts Unicode characters and limits UTF8 bytes',()=>{
 assert.equal(backupPasswordValid('短密码'),false)
 assert.equal(backupPasswordValid('              '),false)
 assert.equal(backupPasswordValid('a'.repeat(12)),true)
 assert.equal(backupPasswordValid('中'.repeat(12)),true)
 assert.equal(backupPasswordValid('😀'.repeat(128)),true)
 assert.equal(backupPasswordValid('a'.repeat(129)),false)
 assert.equal(transferTime('broken'),'未知')
})
