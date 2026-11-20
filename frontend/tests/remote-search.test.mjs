import {test} from 'node:test'
import assert from 'node:assert/strict'
import {searchPage,searchLocation,searchComplete} from '../src/remote-search.ts'

test('search result pagination is bounded and clamps invalid positions',()=>{
 const rows=Array.from({length:123},(_,i)=>i)
 assert.deepEqual(searchPage(rows,2).items,rows.slice(50,100))
 assert.equal(searchPage(rows,100).page,3);assert.equal(searchPage(rows,0).page,1)
 assert.equal(searchPage(rows,NaN).page,1);assert.equal(searchPage([],10).pages,1)
})
test('file results locate the parent while folder results open the folder',()=>{
 assert.deepEqual(searchLocation({Path:'中文 资料/报告.pdf',IsDir:false}),{directory:'中文 资料',selectedPath:'中文 资料/报告.pdf'})
 assert.deepEqual(searchLocation({Path:'root.txt',IsDir:false}),{directory:'',selectedPath:'root.txt'})
 assert.deepEqual(searchLocation({Path:'中文 资料/归档',IsDir:true}),{directory:'中文 资料/归档',selectedPath:''})
})
test('malformed result paths cannot request parent traversal or another scope',()=>{
 for(const Path of ['','../outside','/absolute','one/../two','one//two','one\\two','one/','one/\x00two'])assert.equal(searchLocation({Path,IsDir:true}),undefined)
})
test('cancelled, failed, partial and bounded searches never imply absence',()=>{
 assert.equal(searchComplete({phase:'done'}),true)
 for(const phase of ['idle','searching','cancelled','limited','failed','partial'])assert.equal(searchComplete({phase}),false)
})
