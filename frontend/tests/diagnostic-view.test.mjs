import test from 'node:test'
import assert from 'node:assert/strict'
import {filterDiagnosticEvents,diagnosticErrorsOnly,diagnosticSessionLabel} from '../src/diagnostic-view.ts'
test('diagnostic filters distinguish deliberate cancellation from errors and keep source order intact',()=>{
 const events=[{operation:'mount/mount',result:'success'},{operation:'operations/list',result:'timeout'},{operation:'mount/mount',result:'cancelled'},{operation:'mount/mount',result:'authentication_or_permission'}]
 const before=structuredClone(events)
 assert.deepEqual(filterDiagnosticEvents(events,'',true),[events[3],events[1]])
 assert.deepEqual(filterDiagnosticEvents(events,'mount/mount',true),[events[3]])
 assert.deepEqual(filterDiagnosticEvents(events,'unknown',false),[])
 assert.deepEqual(events,before);assert.equal(diagnosticErrorsOnly('cancelled'),false)
})
test('session labels distinguish current, previous legacy and historical records',()=>{
 const s={id:'a',startedAt:'2026-11-18T00:00:00Z',version:'legacy'}
 assert.equal(diagnosticSessionLabel(s,'a',0),'当前会话')
 assert.match(diagnosticSessionLabel(s,'b',1),/^上次会话.*旧版$/)
 assert.match(diagnosticSessionLabel({...s,version:'0.36.0'},'b',2),/^历史会话.*0\.36\.0$/)
})
