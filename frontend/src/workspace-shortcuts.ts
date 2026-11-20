type Key = {key:string,ctrlKey?:boolean,altKey?:boolean,shiftKey?:boolean,metaKey?:boolean,repeat?:boolean}
type Context = {screen:string,hasProfile:boolean,blocked:boolean,dialog:boolean}
export type WorkspaceShortcut = 'refreshStatus'|'refreshLogs'|'ignore'

export function workspaceShortcut(event:Key,context:Context):WorkspaceShortcut|null {
  if(event.key!=='F5')return null
  // Consume F5 even when unavailable so WebView cannot reload an in-flight task.
  if(context.blocked||context.dialog||event.repeat||event.ctrlKey||event.altKey||event.shiftKey||event.metaKey)return 'ignore'
  if(context.screen==='diagnostics')return 'refreshLogs'
  return context.screen==='connections'&&context.hasProfile?'refreshStatus':'ignore'
}
