import {test} from 'node:test'
import assert from 'node:assert/strict'
import {sortDirectory,filterDirectory} from '../src/directory-view.ts'
import {directoryPage} from '../src/directory-page.ts'

const file=(Name,Size=1,IsDir=false)=>({Name,Path:Name,Size,IsDir})
test('name sorting is natural, directories stay first, and source is unchanged',()=>{
  const source=[file('文件10'),file('目录2',-1,true),file('文件2'),file('目录10',-1,true)]
  const original=source.slice()
  assert.deepEqual(sortDirectory(source,'nameAsc').map(f=>f.Name),['目录2','目录10','文件2','文件10'])
  assert.deepEqual(sortDirectory(source,'nameDesc').map(f=>f.Name),['目录10','目录2','文件10','文件2'])
  assert.deepEqual(source,original)
})
test('size sorting uses numeric bytes; unknown sizes last, directories alphabetically first',()=>{
  const source=[file('unknown',-1),file('large',100),file('empty',0),file('small',9),file('folder',-1,true),file('invalid',NaN)]
  assert.deepEqual(sortDirectory(source,'sizeAsc').map(f=>f.Name),['folder','empty','small','large','invalid','unknown'])
  assert.deepEqual(sortDirectory(source,'sizeDesc').map(f=>f.Name),['folder','large','small','empty','invalid','unknown'])
})
test('filter matches names only, case-insensitively and literally, with normalized Unicode',()=>{
  const entries=[file('中文ABC[1].txt'),file('Café.txt'),{...file('other'),Path:'中文ABC[1].txt/other'}]
  assert.deepEqual(filterDirectory(entries,'  abc[1]  '),[entries[0]])
  assert.deepEqual(filterDirectory(entries,'中文'),[entries[0]])
  assert.deepEqual(filterDirectory(entries,'Cafe\u0301'),[entries[1]])
  assert.deepEqual(filterDirectory(entries,'absent'),[])
  assert.equal(filterDirectory(entries,'   '),entries)
})
test('filter applies before pagination across 10001 entries and clearing restores all entries',()=>{
  const source=Array.from({length:10001},(_,i)=>file(`file${i}.txt`,i))
  const sorted=sortDirectory(source,'nameAsc'),matches=filterDirectory(sorted,'file10')
  assert.equal(matches.length,112)
  const filteredPage=directoryPage(matches,51)
  assert.equal(filteredPage.page,1);assert.equal(filteredPage.total,112)
  assert.equal(filterDirectory(sorted,'').length,10001)
  assert.deepEqual(directoryPage(filterDirectory(sorted,'missing'),51).items,[])
})
