<script setup lang="ts">
import {computed,nextTick,ref,watch} from 'vue'
import {connectionGroups} from './connection-groups'
import ConnectionDrives from './ConnectionDrives.vue'
import GroupMoveForm from './GroupMoveForm.vue'
import {type DrivePlan, type previewDriveChanges} from './connection-drives'
import {errorMessage} from './operation-feedback'
import {providerLabel} from './storage-providers'
import {managementRows,orderManagementRows,visibleSelection,selectedManagementRows,managementChangeFeedback,type ManagedProfile,type ManagerFilter,type ManagerOrder} from './connection-management'
const props=defineProps<{profiles:ManagedProfile[],mountedIds:string[],busy:boolean,online:boolean,api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>,cancellable?:boolean)=>Promise<void>,reload:()=>Promise<void>}>()
const emit=defineEmits<{notice:[text:string],choose:[id:string],check:[ids:string[]],preset:[ids:string[]]}>()
const filter=ref<ManagerFilter>({query:'',group:null,provider:null,mount:'all',automatic:'all'}),chosen=ref<string[]>([])
const order=ref<ManagerOrder>('saved')
const rows=computed(()=>orderManagementRows(managementRows(props.profiles,props.mountedIds,filter.value),order.value)),groups=computed(()=>connectionGroups(props.profiles))
const providerOptions=computed(()=>[...new Set(props.profiles.map(p=>p.provider||'webdav' as const))].sort((a,b)=>providerLabel(a).localeCompare(providerLabel(b),'zh-CN')))
const groupNames=computed(()=>groups.value.map(g=>g.name).filter(Boolean))
const picked=computed(()=>selectedManagementRows(rows.value,chosen.value)),allVisible=computed(()=>rows.value.length>0&&rows.value.every(p=>chosen.value.includes(p.id)))
const deleting=ref<string[]|null>(null),deleteConfirmed=ref(false),copying=ref(''),copyName=ref(''),copyDrive=ref(''),copyPassword=ref(false),copyOptions=ref<{drives:string[],hasPassword:boolean,group:string,favoriteCount:number}|null>(null)
const batchRunning=ref(false),batchStopping=ref(false)
const moving=ref(false)
const drivePlan=ref<DrivePlan|null>(null),driveError=ref('')
const noteID=ref(''),noteDraft=ref(''),noteInput=ref<HTMLInputElement|null>(null)
const noteTarget=computed(()=>rows.value.find(p=>p.id===noteID.value))
watch(noteTarget,target=>{if(!target)noteID.value=''})
const outcome=ref(''),batchItems=ref<{id:string,name:string,drive:string,state:string,message:string}[]>([])
// Polling may return new arrays with identical data. Keep open forms intact
// until their actual scope/data changes, rather than closing on every refresh.
watch(()=>JSON.stringify([filter.value,props.profiles.map(p=>[p.id,p.name,p.drive,p.group,p.autoMount,p.provider,p.note]),[...props.mountedIds].sort()]),()=>{chosen.value=visibleSelection(chosen.value,rows.value);deleting.value=null;deleteConfirmed.value=false;copying.value='';copyOptions.value=null;moving.value=false},{flush:'sync'})
watch(providerOptions,options=>{if(filter.value.provider&&!options.includes(filter.value.provider))filter.value.provider=null})
watch(groups,options=>{if(filter.value.group!==null&&!options.some(g=>g.name===filter.value.group))filter.value.group=null})
function selectVisible(){chosen.value=allVisible.value?[]:rows.value.slice(0,200).map(p=>p.id);deleting.value=null;deleteConfirmed.value=false}
watch(chosen,()=>{deleting.value=null;deleteConfirmed.value=false;moving.value=false;drivePlan.value=null})
async function finishChange(message:string){outcome.value=await managementChangeFeedback(message,props.reload);emit('notice',outcome.value)}
async function openDrives(){
 if(props.busy||!chosen.value.length)return
 const ids=[...chosen.value]
 driveError.value=''
 await props.run(async()=>{
  try{
   const plan=await props.api('PreviewConnectionDrives',ids)
   deleting.value=null
   copying.value=''
   copyOptions.value=null
   moving.value=false
   noteID.value=''
   drivePlan.value=plan
  }catch(error){
   if(!drivePlan.value)throw error
   driveError.value=errorMessage(error)
  }
 })
}
async function saveDrives(changes:ReturnType<typeof previewDriveChanges>['changes']){
 if(!drivePlan.value||props.busy)return
 driveError.value=''
 await props.run(async()=>{
  try{
   const result=await props.api('SaveConnectionDrives',changes)
   drivePlan.value=null
   chosen.value=[]
   await finishChange(`已调整 ${result.changed} 个网盘的盘符`)
  }catch(error){driveError.value=errorMessage(error)}
 })
}
async function openNote(p:ManagedProfile){
 drivePlan.value=null
 deleting.value=null
 copying.value=''
 copyOptions.value=null
 moving.value=false
 noteID.value=p.id
 noteDraft.value=p.note||''
 await nextTick()
 noteInput.value?.focus()
}
async function saveNote(){
 if(!noteTarget.value||props.busy)return
 const id=noteTarget.value.id,note=noteDraft.value.trim()
 await props.run(async()=>{
  await props.api('SaveConnectionNote',id,note)
  noteID.value=''
  await finishChange(note?'备注已保存':'备注已清空')
 })
}
function openMove(){
 drivePlan.value=null
 noteID.value=''
 deleting.value=null
 copying.value=''
 copyOptions.value=null
 moving.value=true
}
async function moveSelected(group:string){
 const ids=picked.value.map(p=>p.id)
 if(!moving.value||!ids.length||props.busy)return
 await props.run(async()=>{
  await props.api('MoveConnectionsToGroup',ids,group)
  moving.value=false
  chosen.value=[]
  await finishChange(`已将 ${ids.length} 个网盘移入${group?`「${group}」`:'未分组'}`)
 })
}
async function autoMount(enabled:boolean){const ids=[...chosen.value];await props.run(async()=>{const result=await props.api('SetConnectionsAutoMount',ids,enabled);await finishChange(`已${enabled?'开启':'关闭'} ${result.changed} 个网盘的自动挂载，下次打开程序时生效`)})}
async function batch(mount:boolean){const ids=[...chosen.value];outcome.value='';batchItems.value=[];batchRunning.value=true;batchStopping.value=false;try{await props.run(async()=>{const report=await props.api(mount?'MountConnections':'UnmountConnections',ids);batchItems.value=report.items||[];await finishChange(report.cancelled?'已停止后续任务，请查看逐项结果':'所选网盘处理完成，请查看逐项结果')},true)}finally{batchRunning.value=false;batchStopping.value=false}}
async function stopBatch(){batchStopping.value=true;try{await props.api('CancelOperation')}catch{batchStopping.value=false;outcome.value='未能停止后续任务，请稍后重试'}}
function requestDelete(){drivePlan.value=null;noteID.value='';moving.value=false;deleting.value=[...chosen.value];deleteConfirmed.value=false;copying.value='';copyOptions.value=null}
async function remove(){const ids=deleting.value;if(!ids||!deleteConfirmed.value)return;await props.run(async()=>{const result=await props.api('DeleteConnections',ids);deleting.value=null;chosen.value=[];await finishChange(`已删除 ${result.changed} 个本机连接。${result.warning||''}`)})}
async function openCopy(id:string){await props.run(async()=>{const options=await props.api('ConnectionCopyOptions',id);drivePlan.value=null;noteID.value='';copying.value=id;copyOptions.value=options;copyName.value=options.name;copyDrive.value=options.drives[0]||'';copyPassword.value=false;deleting.value=null;moving.value=false})}
async function copy(){if(!copyOptions.value)return;const id=copying.value,name=copyName.value,drive=copyDrive.value,password=copyPassword.value,needsPassword=copyOptions.value.hasPassword&&!copyPassword.value;await props.run(async()=>{await props.api('CopyConnection',id,name,drive,password);copying.value='';copyOptions.value=null;await finishChange('连接副本已创建，自动挂载已关闭'+(needsPassword?'；请在编辑连接中填写网盘密码':''))})}
</script>
<template>
 <div class="manager-filters">
  <label class="manager-search">查找网盘<input v-model="filter.query" :disabled="busy" type="search" placeholder="名称、备注、分组或盘符" aria-label="管理列表查找网盘"></label>
  <label>分组<select v-model="filter.group" :disabled="busy"><option :value="null">全部分组</option><option v-for="g in groups" :key="g.name" :value="g.name">{{g.label}}</option></select></label>
  <label>服务商<select v-model="filter.provider" :disabled="busy"><option :value="null">全部服务商</option><option v-for="provider in providerOptions" :key="provider" :value="provider">{{providerLabel(provider)}}</option></select></label>
  <label>挂载状态<select v-model="filter.mount" :disabled="busy"><option value="all">全部</option><option value="mounted">已挂载</option><option value="unmounted">未挂载</option></select></label>
  <label>自动挂载<select v-model="filter.automatic" :disabled="busy"><option value="all">全部</option><option value="on">已开启</option><option value="off">已关闭</option></select></label>
  <label>排序<select v-model="order" :disabled="busy"><option value="saved">原有顺序</option><option value="name">按名称</option><option value="drive">按盘符</option></select></label>
 </div>
 <div class="manager-selection"><label><input type="checkbox" :checked="allVisible" :disabled="busy||!rows.length||rows.length>200" @change="selectVisible">全选当前结果</label><span>已选 {{chosen.length}} / {{rows.length}} 个</span><button :disabled="busy||!chosen.length" @click="chosen=[]">清空选择</button></div>
 <ul class="manager-connections" aria-label="连接管理列表">
  <li v-for="p in rows" :key="p.id">
   <input v-model="chosen" type="checkbox" :value="p.id" :aria-label="'选择 '+p.name" :disabled="busy||(!chosen.includes(p.id)&&chosen.length>=200)">
   <div class="manager-identity">
    <button class="text-button manager-name" :disabled="busy" :title="'查看 '+p.name" @click="emit('choose',p.id)">{{p.name}}</button>
    <small>{{p.drive}} · {{providerLabel(p.provider)}} · {{p.group||'未分组'}}</small>
    <p v-if="p.note" class="manager-note" :title="p.note">{{p.note}}</p>
   </div>
   <span class="manager-status" :class="{mounted:mountedIds.includes(p.id)}">{{mountedIds.includes(p.id)?'已挂载':'未挂载'}}</span>
   <span class="manager-auto">自动 {{p.autoMount?'开启':'关闭'}}</span>
   <button :disabled="busy" :aria-label="'修改 '+p.name+' 的备注'" @click="openNote(p)">备注…</button>
   <button :disabled="busy||!online" :aria-label="'复制连接'+p.name" @click="openCopy(p.id)">复制…</button>
  </li>
 </ul>
 <p v-if="!rows.length" class="field-help">没有符合条件的网盘，试试修改筛选条件。</p>
 <form v-if="noteTarget" class="manager-note-form" @submit.prevent="saveNote">
  <label>{{noteTarget.name}}的备注<input ref="noteInput" v-model="noteDraft" placeholder="例如：工作项目归档" :disabled="busy" aria-describedby="manager-note-help"></label>
  <p id="manager-note-help" class="field-help">最多 120 个字符，留空可清除。</p>
  <div class="manager-actions">
   <button class="primary" :disabled="busy||Array.from(noteDraft.trim()).length>120">保存备注</button>
   <button type="button" :disabled="busy" @click="noteID=''">取消</button>
  </div>
 </form>
 <div class="manager-actions"><button :disabled="busy||!chosen.length" @click="openMove">移动到分组…</button><button :disabled="busy||!online||!chosen.length" @click="openDrives">调整盘符…</button><button :disabled="busy||!online||!chosen.length" @click="batch(true)">挂载所选</button><button :disabled="busy||!online||!chosen.length" @click="batch(false)">卸载所选</button><button :disabled="busy||!chosen.length" @click="autoMount(true)">开启自动挂载</button><button :disabled="busy||!chosen.length" @click="autoMount(false)">关闭自动挂载</button><button class="danger-text" :disabled="busy||!online||!chosen.length" @click="requestDelete">删除所选…</button></div>
 <button v-if="batchRunning" :disabled="batchStopping" @click="stopBatch">{{batchStopping?'正在结束当前任务':'停止后续任务'}}</button><p class="field-help">更改筛选会取消被隐藏的选择。</p>
 <GroupMoveForm v-if="moving" class="manager-copy" :groups="groupNames" :count="picked.length" :busy="busy" cancellable @move="moveSelected" @cancel="moving=false" />
 <ConnectionDrives v-if="drivePlan" :plan="drivePlan" :busy="busy" :error="driveError" @refresh="openDrives" @save="saveDrives" @cancel="drivePlan=null" />
 <section v-if="deleting" class="manager-confirm" aria-label="删除连接确认"><h3>删除 {{deleting.length}} 个本机连接？</h3><p>所选连接的本机配置、保存的密码和常用目录会删除，网盘内文件保留。已挂载项必须先卸载；检查不通过时整批保留。</p><ul><li v-for="p in picked" :key="p.id">{{p.name}}（{{p.drive}}）</li></ul><label class="check-row"><input v-model="deleteConfirmed" type="checkbox" :disabled="busy">我已核对以上连接</label><div class="manager-actions"><button class="danger-text" :disabled="busy||!deleteConfirmed" @click="remove">确认删除本机连接</button><button :disabled="busy" @click="deleting=null">取消</button></div></section>
 <form v-if="copying&&copyOptions" class="manager-copy" @submit.prevent="copy"><h3>复制连接</h3><div class="manager-copy-fields"><label>新连接名称<input v-model="copyName" :disabled="busy" maxlength="80" required></label><label>盘符<select v-model="copyDrive" :disabled="busy"><option v-for="drive in copyOptions.drives" :key="drive" :value="drive">{{drive}}</option></select></label></div><p class="field-help">复制地址、账号、分组、备注和 {{copyOptions.favoriteCount}} 个常用目录。新副本不会自动挂载。</p><label v-if="copyOptions.hasPassword" class="check-row"><input v-model="copyPassword" :disabled="busy" type="checkbox">同时复制已保存的网盘密码</label><p v-if="!copyOptions.drives.length" class="danger-text">没有可用盘符，请先调整其它连接。</p><div class="manager-actions"><button class="primary" :disabled="busy||!copyName.trim()||!copyDrive">创建副本</button><button type="button" :disabled="busy" @click="copying='';copyOptions=null">取消</button></div></form>
 <div class="manager-actions"><button :disabled="busy || !chosen.length" @click="emit('check',[...chosen])">体检所选连接…</button><button :disabled="busy || !chosen.length" @click="emit('preset',[...chosen])">将所选保存为方案…</button></div>
 <p v-if="outcome" class="manager-outcome" role="status">{{outcome}}</p><ul v-if="batchItems.length" class="manager-results" aria-label="所选网盘处理结果"><li v-for="item in batchItems" :key="item.id"><strong>{{item.name}}（{{item.drive}}）</strong><span :class="{'danger-text':item.state==='failed'}">{{item.message}}</span></li></ul>
</template>
<style scoped>
.manager-filters{display:flex;gap:8px;flex-wrap:wrap}.manager-filters label{display:grid;gap:6px;font-size:11px;min-width:90px;flex:1}.manager-filters .manager-search{flex:2;min-width:150px}.manager-filters input,.manager-filters select{width:100%;min-width:0}.manager-selection{display:flex;gap:12px;align-items:center;margin:12px 0;font-size:11px;color:#74839a;flex-wrap:wrap}.manager-selection label{display:flex;gap:7px;align-items:center}.manager-selection button{margin-left:auto}.manager-connections{list-style:none;margin:0;padding:0;border:1px solid #e5e9f0;border-radius:5px;max-height:215px;overflow:auto}.manager-connections li{display:flex;align-items:center;gap:10px;padding:10px;border-bottom:1px solid #edf0f5}.manager-connections li:last-child{border:0}.manager-connections label{display:flex;align-items:center;gap:9px;min-width:0;flex:1}.manager-connections label span{min-width:0}.manager-connections strong{display:block;font-size:12px;font-weight:500;overflow-wrap:anywhere}.manager-connections small{display:block;font-size:10px;color:#8491a5;margin-top:5px}.manager-status,.manager-auto{font-size:10px;color:#8491a5;white-space:nowrap}.manager-status.mounted{color:#287c4a}.manager-actions{display:flex;gap:7px;flex-wrap:wrap;margin-top:12px}.manager-actions button,.manager-selection button,.manager-connections button{font-size:11px;padding:6px 9px}.manager-confirm,.manager-copy{border:1px solid #d9e5ff;background:#f8faff;padding:14px;border-radius:5px;margin-top:16px}.manager-confirm{border-color:#f0d8cb;background:#fff9f5}.manager-confirm h3,.manager-copy h3{font-size:13px;margin:0 0 8px}.manager-confirm p{font-size:11px;line-height:1.8;color:#7a6d65}.manager-confirm ul{max-height:100px;overflow:auto;font-size:11px;line-height:1.9;overflow-wrap:anywhere}.manager-copy-fields{display:flex;gap:12px}.manager-copy-fields label{display:grid;gap:8px;flex:1;min-width:0}.manager-copy-fields label:first-child{flex:3}.manager-outcome{font-size:11px;color:#486484;line-height:1.8}.manager-results{padding:0;list-style:none;max-height:160px;overflow:auto}.manager-results li{font-size:11px;display:grid;gap:5px;padding:8px 0;border-bottom:1px solid #edf0f5;overflow-wrap:anywhere}.manager-results strong{font-weight:500}.manager-results span{color:#8491a5}@media(max-width:900px){.manager-connections li{gap:6px}.manager-auto{max-width:42px;white-space:normal}}
.manager-identity{flex:1;min-width:0}
.manager-connections .manager-name{display:block;max-width:100%;padding:0;overflow:hidden;text-overflow:ellipsis;text-align:left;white-space:nowrap;font-size:12px}
.manager-connections li>button{flex:none}
.manager-note{margin:4px 0 0;color:#486484;font-size:11px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.manager-note-form{margin:10px 0 0;padding:12px;border:1px solid #d9e5ff;border-radius:5px;background:#f8faff}
.manager-note-form label{display:grid;gap:6px;overflow-wrap:anywhere}
.manager-note-form input{width:100%}
</style>
