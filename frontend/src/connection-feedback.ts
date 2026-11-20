type Feedback={label:string,tone:'neutral'|'success'|'warning',detail:string}

// Health describes the most recent directory read, never a live network probe.
// Mount state and short-lived operation notices are separate facts.
export function remoteFeedback(online:boolean,mounted:boolean,health:string):Feedback {
  if(!online)return {label:'服务已停止',tone:'warning',detail:'请先恢复挂载服务，再刷新目录检查远端。'}
  if(health==='最近目录读取成功')return {label:'最近读取成功',tone:'success',detail:'仅代表最近一次目录读取结果，当前网络状态请刷新确认。'}
  if(health==='最近读取失败：操作已取消')return {label:'读取已取消',tone:'neutral',detail:'本次读取已取消，远端是否可访问请重新刷新确认。'}
  if(health.startsWith('最近读取失败：'))return {label:'最近读取失败',tone:'warning',detail:health.slice('最近读取失败：'.length)}
  return {label:'尚未检查',tone:'neutral',detail:mounted?'挂载仅表示盘符就绪，请刷新目录检查远端。':'点击“读取目录”检查远端，也可先挂载网盘。'}
}

export function recoveryFeedback(message:string):Feedback {
  if(message.startsWith('恢复失败：'))return {label:'恢复失败',tone:'warning',detail:message}
  if(message==='已恢复挂载')return {label:'挂载已恢复',tone:'success',detail:'请刷新目录确认远端可访问。'}
  if(message.startsWith('盘符保持挂载'))return {label:'保持挂载',tone:'neutral',detail:'盘符仍在挂载，请刷新目录确认远端可访问。'}
  return {label:'未参与恢复',tone:'neutral',detail:'仅恢复本次会话中成功挂载过的网盘。'}
}
