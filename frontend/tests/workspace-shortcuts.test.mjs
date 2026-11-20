import test from 'node:test'
import assert from 'node:assert/strict'
import {workspaceShortcut} from '../src/workspace-shortcuts.ts'

const mountedPage={screen:'connections',hasProfile:true,blocked:false,dialog:false}
test('F5 refreshes mount status or diagnostics without reloading the WebView',()=>{
  assert.equal(workspaceShortcut({key:'F5'},mountedPage),'refreshStatus')
  assert.equal(workspaceShortcut({key:'F5'},{...mountedPage,screen:'diagnostics'}),'refreshLogs')
  for(const changes of [{hasProfile:false},{screen:'settings'},{dialog:true},{blocked:true}]){
    assert.equal(workspaceShortcut({key:'F5'},{...mountedPage,...changes}),'ignore')
  }
  for(const changes of [{repeat:true},{ctrlKey:true},{shiftKey:true}]){
    assert.equal(workspaceShortcut({key:'F5',...changes},mountedPage),'ignore')
  }
})
test('the mount workspace leaves former file navigation shortcuts unhandled',()=>{
  assert.equal(workspaceShortcut({key:'f',ctrlKey:true},mountedPage),null)
  for(const key of ['ArrowLeft','ArrowRight','ArrowUp']){
    assert.equal(workspaceShortcut({key,altKey:true},mountedPage),null)
  }
})
