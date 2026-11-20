type Cell<T> = {value:T}
type Feedback = {busy:Cell<boolean>,canCancel:Cell<boolean>,error:Cell<string>,notice:Cell<string>}

export function errorMessage(error:unknown):string {
  return (error instanceof Error ? error.message : String(error)).replace(/^RuntimeError:\s*/, '')
}

export function createTaskRunner(state:Feedback) {
  let current:{cancelling:boolean}|undefined
  async function task(fn:()=>Promise<void>,cancellable=false) {
    if(state.busy.value)return
    const operation={cancelling:false}
    current=operation
    state.busy.value=true
    state.canCancel.value=cancellable
    state.error.value=''
    state.notice.value=''
    try {await fn()}
    catch(error) {
      const message=errorMessage(error)
      if(message==='操作已取消')state.notice.value=message
      else {state.error.value=message;state.notice.value=''}
    } finally {
      // Cancel is only an acknowledgement; this task owns the final result.
      if(state.notice.value==='已请求取消，正在结束当前操作…')state.notice.value='操作已结束'
      current=undefined
      state.busy.value=false
      state.canCancel.value=false
    }
  }
  async function cancel(request:()=>Promise<unknown>) {
    const operation=current
    if(!operation||!state.canCancel.value||operation.cancelling)return
    operation.cancelling=true
    state.notice.value='已请求取消，正在结束当前操作…'
    try {await request()}
    catch(error) {
      // Ignore acknowledgements for an operation that has already finished.
      if(current===operation){state.error.value=errorMessage(error);state.notice.value=''}
    } finally {operation.cancelling=false}
  }
  return {task,cancel}
}
