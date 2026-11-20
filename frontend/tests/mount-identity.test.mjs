import {test} from 'node:test'
import assert from 'node:assert/strict'
import {isProfileMounted} from '../src/mount-identity.ts'
test('profiles sharing a drive do not share mounted status',()=>{
 const mounts=[{ProfileID:'owner',MountPoint:'z:'}]
 assert.equal(isProfileMounted(mounts,{id:'owner',drive:'Z:'}),true)
 assert.equal(isProfileMounted(mounts,{id:'other',drive:'Z:'}),false)
 assert.equal(isProfileMounted(mounts,{id:'owner',drive:'Y:'}),false)
})
test('missing identity, empty snapshots and no selected profile stay unmounted',()=>{
 assert.equal(isProfileMounted([{MountPoint:'Z:'}],{id:'owner',drive:'Z:'}),false)
 assert.equal(isProfileMounted([],{id:'owner',drive:'Z:'}),false)
 assert.equal(isProfileMounted([{ProfileID:'owner'}],{id:'owner',drive:'Z:'}),false)
 assert.equal(isProfileMounted([],undefined),false)
})
