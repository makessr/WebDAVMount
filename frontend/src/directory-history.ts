import type {DirectoryOrder} from './directory-view'

export type DirectoryLocation={path:string,query:string,order:DirectoryOrder,page:number}
export type DirectoryHistory={entries:DirectoryLocation[],index:number}
export type HistoryMove={state:DirectoryHistory,location:DirectoryLocation}
export const emptyHistory=():DirectoryHistory=>({entries:[],index:-1})
const copy=(location:DirectoryLocation)=>({...location})

function rememberCurrent(history:DirectoryHistory,current?:DirectoryLocation):DirectoryLocation[]{
  const entries=history.entries.map(copy)
  if(current&&entries[history.index]?.path===current.path)entries[history.index]=copy(current)
  return entries
}

// Commit only after the destination was read successfully. History holds view
// positions, not remote file lists, and is bounded within this application run.
export function recordVisit(history:DirectoryHistory,current:DirectoryLocation|undefined,next:DirectoryLocation):DirectoryHistory {
  const entries=rememberCurrent(history,current)
  if(entries[history.index]?.path===next.path){entries[history.index]=copy(next);return {entries,index:history.index}}
  const branch=entries.slice(0,history.index+1)
  branch.push(copy(next))
  const bounded=branch.slice(-100)
  return {entries:bounded,index:bounded.length-1}
}

// A plan is immutable; discarding a failed/cancelled move preserves both stacks.
export function planHistoryMove(history:DirectoryHistory,current:DirectoryLocation,delta:-1|1):HistoryMove|undefined {
  const index=history.index+delta
  if(index<0||index>=history.entries.length)return undefined
  const entries=rememberCurrent(history,current)
  return {state:{entries,index},location:copy(entries[index])}
}
