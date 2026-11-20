<script setup lang="ts">
import {ref,computed,watch,onUnmounted} from 'vue'
import {startupSummary,startupLabel,type StartupTasks} from './startup-tasks'
const props=defineProps<{report:StartupTasks,busy:boolean,api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>,cancellable?:boolean)=>Promise<void>,refresh:()=>Promise<void>}>()
const emit=defineEmits<{notice:[message:string]}>()
const current=ref<StartupTasks>(props.report),expanded=ref(false),working=ref(false),stopping=ref(false),controlError=ref('')
const summary=computed(()=>startupSummary(current.value))
watch(()=>props.report,value=>{if(!working.value)current.value=value})
let poll:ReturnType<typeof setInterval>|undefined,pending=false,closed=false
watch(()=>current.value.active||working.value,active=>{
 if(poll){clearInterval(poll);poll=undefined}
 if(active)poll=setInterval(async()=>{if(pending)return;pending=true;try{const next=await props.api('StartupMountTasks');if(!closed)current.value=next}catch{}finally{pending=false}},500)
},{immediate:true})
async function stop(){stopping.value=true;controlError.value='';try{await props.api(current.value.retrying?'CancelOperation':'CancelStartupMount');emit('notice','已请求停止后续挂载，已挂载的网盘保留')}catch{controlError.value='停止请求未完成，请稍后重试。'}finally{stopping.value=false}}
async function retry(){expanded.value=true;await props.run(async()=>{working.value=true;try{await props.api('RetryFailedStartupMounts');emit('notice','启动挂载重试已结束，请核对逐项结果')}finally{working.value=false;current.value=await props.api('StartupMountTasks');await props.refresh()}},true)}
onUnmounted(()=>{closed=true;if(poll)clearInterval(poll)})
</script>
<template>
 <section v-if="summary.total || current.warning" class="startup-tasks" aria-label="本次启动挂载任务">
  <div class="startup-heading">
   <div><strong>{{current.presetName?'启动挂载 · '+current.presetName:'启动挂载'}}</strong><span v-if="summary.total">已处理 {{summary.done}} / {{summary.total}}<template v-if="summary.retry"> · {{summary.retry}} 项未完成</template></span></div>
   <div v-if="summary.total" class="startup-actions"><button v-if="current.active" :disabled="stopping||current.stopping" @click="stop">{{current.stopping?'正在结束当前任务':'停止后续挂载'}}</button><button v-else :disabled="busy||working||!summary.retry" @click="retry">重试未完成项</button><button :aria-expanded="expanded" @click="expanded=!expanded">{{expanded?'收起':'查看任务'}}</button></div>
  </div>
  <div v-if="current.warning" class="dialog-error" role="alert">本次自动挂载已停止。{{current.warning}}</div>
  <div v-if="controlError" class="dialog-error" role="alert">{{controlError}}</div><div v-if="expanded" class="startup-details"><ul><li v-for="item in current.items" :key="item.id"><div><strong>{{item.name}}</strong><small>{{item.drive}}</small><span :class="{'startup-failed':item.state==='failed'}">{{startupLabel(item.state)}}</span></div><p>{{item.message}}</p></li></ul></div>
 </section>
</template>
<style scoped>
.startup-tasks{margin:0 20px 12px;border:1px solid #dce4f1;background:#f7f9fe;border-radius:6px;flex:none}.startup-heading{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:10px 12px}.startup-heading>div:first-child{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.startup-heading span{font-size:12px;color:#6b7280}.startup-actions{display:flex;gap:6px;flex:none}.startup-actions button{padding:5px 9px;font-size:12px;min-height:28px}.startup-details{border-top:1px solid #e3e8f2;padding:8px 12px}.startup-details>p{margin:0 0 6px}.startup-details ul{list-style:none;margin:0;padding:0;max-height:150px;overflow:auto}.startup-details li{padding:8px 0;border-top:1px solid #e5e9f1}.startup-details li>div{display:flex;gap:10px;align-items:center}.startup-details strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:65%}.startup-details small{color:#6b7280}.startup-details span{margin-left:auto;font-size:12px;color:#35694b}.startup-details .startup-failed{color:#b64b3c}.startup-details li p{font-size:12px;color:#6b7280;margin:4px 0 0;overflow-wrap:anywhere}
</style>
