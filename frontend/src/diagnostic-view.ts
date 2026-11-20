export type DiagnosticEvent={time:string,operation:string,result:string,durationMs:number}
export type DiagnosticSession={id:string,startedAt:string,version:string,events:DiagnosticEvent[]}
export type DiagnosticSessions={currentID:string,sessions:DiagnosticSession[],warning:string}
export const diagnosticOperationNames:Record<string,string>={'connections.autostart':'批量自动挂载设置','connections.delete':'批量删除连接','connections.copy':'复制连接','operations/list':'读取目录','mount/mount':'挂载网盘','mount/unmount':'卸载网盘','config/create':'保存连接','config/delete':'清理连接','core/quit':'停止挂载服务','cache.scan':'统计缓存','cache.migrate':'迁移缓存','remote.search':'搜索网盘文件','startup.save':'保存启动设置','startup.retry':'重试启动挂载','recovery.auto':'自动恢复挂载','license.import':'导入授权','update.check':'检查更新','update.download':'下载更新包'}
export const diagnosticResultNames:Record<string,string>={success:'成功',cancelled:'已取消',authentication_or_permission:'认证或权限错误',rate_limited:'请求过于频繁',tls_certificate:'证书错误',timeout:'连接超时',engine_unavailable:'挂载服务不可用',network_unavailable:'网络不可达',operation_failed:'操作失败'}
export function diagnosticErrorsOnly(result:string){return result!=='success'&&result!=='cancelled'}
export function filterDiagnosticEvents(events:DiagnosticEvent[],operation:string,errorsOnly:boolean):DiagnosticEvent[]{return events.filter(event=>(!operation||event.operation===operation)&&(!errorsOnly||diagnosticErrorsOnly(event.result))).slice().reverse()}
export function diagnosticSessionLabel(session:DiagnosticSession,currentID:string,index:number):string{
 if(session.id===currentID)return '当前会话'
 const date=new Date(session.startedAt),time=Number.isFinite(date.getTime())?date.toLocaleString('zh-CN',{hour12:false}):'时间未知'
 return `${index===1?'上次会话':'历史会话'} · ${time} · ${session.version==='legacy'?'旧版':session.version}`
}
