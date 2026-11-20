// WebDAV/rclone timestamps use RFC3339. Do not let Date.parse interpret an
// absent offset as local time, normalize invalid calendar dates, or show Go's zero.
export function modifiedTimestamp(value:unknown):number|undefined {
  if(typeof value!=='string')return undefined
  const match=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.exec(value)
  if(!match)return undefined
  const [,y,m,d,h,min,s,zone]=match
  const year=Number(y),month=Number(m),day=Number(d)
  const leap=year%4===0&&(year%100!==0||year%400===0)
  const days=[31,leap?29:28,31,30,31,30,31,31,30,31,30,31]
  if(year<=1||month<1||month>12||day<1||day>days[month-1]!||Number(h)>23||Number(min)>59||Number(s)>59)return undefined
  if(zone!=='Z'&&(Number(zone.slice(1,3))>23||Number(zone.slice(4))>59))return undefined
  const timestamp=Date.parse(value)
  return Number.isFinite(timestamp)?timestamp:undefined
}

export function formatModified(value:unknown,timeZone?:string):string {
  const timestamp=modifiedTimestamp(value)
  if(timestamp===undefined)return '—'
  const parts=new Intl.DateTimeFormat('zh-CN',{
    year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hourCycle:'h23',...(timeZone?{timeZone}:{})
  }).formatToParts(timestamp)
  const part=(type:string)=>parts.find(p=>p.type===type)?.value??''
  return `${part('year').padStart(4,'0')}-${part('month')}-${part('day')} ${part('hour')}:${part('minute')}`
}
