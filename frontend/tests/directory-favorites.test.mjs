import {test} from 'node:test'
import assert from 'node:assert/strict'
import {favoriteName,favoriteDirectionAllowed} from '../src/directory-favorites.ts'
test('favorite suggested names use the selected directory, with an explicit root label',()=>{
 assert.equal(favoriteName(''),'根目录');assert.equal(favoriteName('中文/工作 # %'),'工作 # %')
})
test('favorite reorder controls respect both ends and reject missing indices',()=>{
 assert.equal(favoriteDirectionAllowed(0,3,-1),false);assert.equal(favoriteDirectionAllowed(0,3,1),true)
 assert.equal(favoriteDirectionAllowed(2,3,1),false);assert.equal(favoriteDirectionAllowed(2,3,-1),true)
 assert.equal(favoriteDirectionAllowed(-1,3,1),false);assert.equal(favoriteDirectionAllowed(0,0,1),false)
})
