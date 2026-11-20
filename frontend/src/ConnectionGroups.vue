<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import UiIcon from './UiIcon.vue'
import ConnectionManagement from './ConnectionManagement.vue'
import ConnectionCheck from './ConnectionCheck.vue'
import MountPresets from './MountPresets.vue'
import GroupMoveForm from './GroupMoveForm.vue'
import type {ConnectionCheckItem,ManagedProfile,MountPreset} from './connection-management'
import {connectionGroups,groupMoveAllowed} from './connection-groups'
const props=defineProps<{profiles:ManagedProfile[],selected:string,connectionsScreen:boolean,mountedIds:string[],busy:boolean,online:boolean,open:boolean,error:string,presets:MountPreset[],presetReadError:string,api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>,cancellable?:boolean)=>Promise<void>,reload:()=>Promise<void>,reloadPresets:()=>Promise<void>}>()
const emit=defineEmits<{choose:[id:string],manage:[open:boolean],batch:[group:string,mount:boolean],notice:[text:string],preset:[id:string,mount:boolean],switchPreset:[]}>()
const managerTab=ref<'manage'|'connections'|'groups'|'check'|'presets'>('manage')
const presetSelection=ref<string[]>([])
const checkResults=ref<ConnectionCheckItem[]>([])
const checkSelected=ref<string[]>([])
watch(()=>props.open,open=>{if(!open){checkResults.value=[];checkSelected.value=[]}})
const query=ref(''),collapsed=ref(new Set<string>()),chosen=ref<string[]>([]),scope=ref<string|null>(null),renameFrom=ref(''),renameTo=ref(''),dissolving=ref('')
const allGroups=computed(()=>connectionGroups(props.profiles)),groups=computed(()=>connectionGroups(props.profiles,query.value))
const managerRows=computed(()=>scope.value===null?props.profiles:props.profiles.filter(p=>(p.group||'')===scope.value))
watch(scope,()=>{chosen.value=[]})
watch([()=>props.selected,()=>props.profiles.find(p=>p.id===props.selected)?.group],([id])=>{const p=props.profiles.find(p=>p.id===id);if(p)collapsed.value.delete(p.group||'')})
watch(()=>props.profiles,()=>{chosen.value=chosen.value.filter(id=>props.profiles.some(p=>p.id===id));if(scope.value!==null&&!allGroups.value.some(g=>g.name===scope.value))scope.value=null})
function fold(name:string){if(collapsed.value.has(name))collapsed.value.delete(name);else collapsed.value.add(name)}
function openManager(group:string|null=null){managerTab.value=group===null?'manage':'connections';scope.value=group;chosen.value=[];renameFrom.value='';renameTo.value='';dissolving.value='';emit('manage',true)}
function chooseManaged(id:string){emit('manage',false);emit('choose',id)}
function openCheck(ids:string[]){checkSelected.value=ids;managerTab.value='check'}
function openPresets(ids:string[]=[]){presetSelection.value=ids;managerTab.value='presets'}
function openPresetManager(){openManager();openPresets()}
defineExpose({openPresetManager})
function applyPreset(id:string,mount:boolean){emit('preset',id,mount)}
async function change(method:string,args:any[],message:string,after?:()=>void){await props.run(async()=>{await props.api(method,...args);await props.reload();after?.();emit('notice',message)})}
async function moveSelected(group:string){await change('MoveConnectionsToGroup',[[...chosen.value],group],group?'所选网盘已移入分组':'所选网盘已移入未分组',()=>{chosen.value=[]})}
async function rename(){await change('RenameConnectionGroup',[renameFrom.value,renameTo.value],'分组名称已保存',()=>{renameFrom.value='';renameTo.value=''})}
async function dissolve(name:string){await change('RenameConnectionGroup',[name,''],'分组已解散，连接和挂载保留',()=>{dissolving.value='';renameFrom.value=''})}
function groupAction(event:Event,name:string){const select=event.target as HTMLSelectElement;const action=select.value;select.value='';if(action==='manage')openManager(name);else if(action==='mount'||action==='unmount')emit('batch',name,action==='mount')}
</script>
<template>
 <div class="group-nav-tools"><input v-if="profiles.length>4||!!query" v-model="query" aria-label="查找网盘、分组或备注" placeholder="查找网盘、分组或备注" :disabled="busy"><button v-if="profiles.length || presets.length || presetReadError" class="text-button" :disabled="busy" @click="openManager()">管理网盘</button></div>
 <nav class="drive-list grouped-drives" aria-label="已保存的网盘">
  <section v-for="group in groups" :key="group.name" class="drive-group">
   <div class="group-heading"><button class="group-fold" :disabled="busy" :aria-expanded="!collapsed.has(group.name)||!!query" @click="fold(group.name)"><span aria-hidden="true">{{collapsed.has(group.name)&&!query?'›':'⌄'}}</span><strong :title="group.label">{{group.label}}</strong></button><select class="group-action" :disabled="busy" :aria-label="group.label+'分组操作'" @change="groupAction($event,group.name)"><option value="">操作</option><option value="mount" :disabled="!online">挂载本组</option><option value="unmount" :disabled="!online">卸载本组</option><option value="manage">整理本组…</option></select></div>
   <div v-show="!collapsed.has(group.name)||!!query">
     <button v-for="p in group.items" :key="p.id" class="group-drive" :title="p.name" :class="{active:connectionsScreen&&selected===p.id}"
       :aria-label="p.name+' '+p.drive+' '+(mountedIds.includes(p.id)?'已挂载':'未挂载')" :aria-current="connectionsScreen&&selected===p.id?'page':undefined" :disabled="busy" @click="emit('choose',p.id)">
       <span class="nav-drive-icon"><UiIcon name="drive" :size="16"/></span>
       <span class="nav-drive-text">{{p.name}}</span>
       <span class="drive-state" aria-hidden="true"><span class="state-dot" :class="{online:mountedIds.includes(p.id)}"></span>{{p.drive}}</span>
     </button>
   </div>
  </section>
  <p v-if="profiles.length&&!groups.length" class="nav-empty">没有找到匹配的网盘</p>
 </nav>
 <Teleport to="body"><div v-if="open" class="overlay" @click.self="!busy&&emit('manage',false)"><section class="dialog group-dialog" role="dialog" aria-modal="true" aria-labelledby="group-manager-title">
  <div class="dialog-header"><h2 id="group-manager-title">管理网盘</h2><button class="icon-button" :disabled="busy" aria-label="关闭网盘管理" @click="emit('manage',false)"><UiIcon name="close"/></button></div>
  <div class="dialog-body"><div v-if="error" class="dialog-error" role="alert">{{error}}</div>
   <div class="group-tabs" role="tablist" aria-label="管理方式"><button role="tab" :aria-selected="managerTab==='manage'" :disabled="busy" @click="managerTab='manage'">连接管理</button><button role="tab" :aria-selected="managerTab==='connections'" :disabled="busy" @click="managerTab='connections'">网盘整理</button><button role="tab" :aria-selected="managerTab==='groups'" :disabled="busy" @click="managerTab='groups'">分组管理</button><button role="tab" :aria-selected="managerTab==='check'" :disabled="busy" @click="managerTab='check'">连接体检</button><button role="tab" :aria-selected="managerTab==='presets'" :disabled="busy" @click="openPresets()">挂载方案</button></div><ConnectionManagement v-if="managerTab==='manage'" :profiles="profiles" :mounted-ids="mountedIds" :busy="busy" :online="online" :api="api" :run="run" :reload="reload" @notice="emit('notice',$event)" @choose="chooseManaged" @check="openCheck" @preset="openPresets"/><MountPresets v-if="managerTab==='presets'" :presets="presets" :profiles="profiles" :initial-selection="presetSelection" :online="online" :busy="busy" :read-error="presetReadError" :api="api" :run="run" :reload="reloadPresets" @apply="applyPreset" @switch-preset="emit('switchPreset')" @notice="emit('notice',$event)"/><ConnectionCheck v-if="managerTab==='check'" v-model="checkResults" v-model:selected="checkSelected" :profiles="profiles" :busy="busy" :online="online" :api="api" :run="run" @notice="emit('notice',$event)"/><div v-if="managerTab==='connections'"><label class="group-scope">显示分组<select v-model="scope" :disabled="busy"><option :value="null">全部分组</option><option v-for="g in allGroups" :key="g.name" :value="g.name">{{g.label}}（{{g.items.length}}）</option></select></label>
   <ul class="group-profile-list"><li v-for="p in managerRows" :key="p.id"><label><input v-model="chosen" type="checkbox" :value="p.id" :disabled="busy"><span><strong>{{p.name}}</strong><small>{{p.drive}} · {{p.group||'未分组'}}</small></span></label><button :disabled="busy||!groupMoveAllowed(profiles,p.id,-1)" :aria-label="'上移'+p.name" @click="change('MoveConnectionInGroup',[p.id,-1],'组内顺序已保存')">上移</button><button :disabled="busy||!groupMoveAllowed(profiles,p.id,1)" :aria-label="'下移'+p.name" @click="change('MoveConnectionInGroup',[p.id,1],'组内顺序已保存')">下移</button></li></ul>
   <GroupMoveForm :groups="allGroups.map(g=>g.name).filter(Boolean)" :count="chosen.length" :busy="busy" @move="moveSelected" />
   </div><div v-else-if="managerTab==='groups'"><h3>分组管理</h3><ul class="group-name-list"><li v-for="g in allGroups.filter(g=>g.name)" :key="g.name"><template v-if="dissolving===g.name"><p>解散“{{g.label}}”？{{g.items.length}}个连接移回未分组，磁盘继续挂载。</p><button class="danger-text" :disabled="busy" @click="dissolve(g.name)">确认解散</button><button :disabled="busy" @click="dissolving=''">取消</button></template><form v-else-if="renameFrom===g.name" @submit.prevent="rename"><input v-model="renameTo" maxlength="32" required :disabled="busy" aria-label="新的分组名称"><button class="primary" :disabled="busy||!renameTo.trim()">保存名称</button><button type="button" :disabled="busy" @click="renameFrom=''">取消</button></form><template v-else><strong>{{g.label}}</strong><small>{{g.items.length}}个网盘</small><button :disabled="busy" @click="renameFrom=g.name;renameTo=g.name;dissolving=''">重命名</button><button :disabled="busy" @click="dissolving=g.name;renameFrom=''">解散</button></template></li></ul><p v-if="!allGroups.some(g=>g.name)" class="field-help">选择上方网盘并输入分组名称，即可创建第一个分组。</p></div>
  </div><div class="dialog-footer"><button :disabled="busy" @click="emit('manage',false)">完成</button></div>
 </section></div></Teleport>
</template>
<style scoped>
.group-nav-tools{display:flex;gap:5px;align-items:center;padding:0 14px 6px;flex-wrap:wrap}.group-nav-tools input{width:100%;min-width:0;font-size:11px}.group-nav-tools>.text-button{margin-left:auto;font-size:11px;min-height:24px}.grouped-drives{flex:1;min-height:0;overflow-y:auto;overflow-x:hidden}.group-action{width:49px;min-height:25px;border:0;background:transparent;padding:2px;font-size:10px;color:#8491a5}.drive-group{padding:0 8px}.group-heading{display:flex;align-items:center;margin:3px 0}.group-fold{display:flex;align-items:center;flex:1;min-width:0;gap:6px;border:0;background:transparent;padding:6px 4px;color:#8491a5;font-size:10px}.group-fold strong{font-weight:500;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;flex:1;text-align:left}.group-fold small{font-size:10px}.group-heading summary{list-style:none;cursor:pointer;padding:2px 6px;color:#8491a5;font-size:15px}.group-heading summary:focus-visible{outline:2px solid #2563eb}.group-menu{left:0;right:auto;z-index:40;min-width:125px}.group-drive{width:100%;justify-content:flex-start;gap:8px;min-height:36px;padding:6px 7px;border:1px solid transparent;background:transparent;color:var(--ink);margin:1px 0}
.group-drive:hover{background:var(--surface-hover);border-color:transparent}
.group-drive.active{background:var(--selection);border-color:transparent;color:var(--primary);font-weight:600}
.group-drive .nav-drive-icon{width:19px;height:20px}
.nav-drive-text{flex:1;min-width:0;font-size:12px;overflow:hidden;text-overflow:ellipsis;text-align:left}
.drive-state{display:flex;align-items:center;gap:5px;color:var(--muted);font-size:10px;font-variant-numeric:tabular-nums}
.drive-state .state-dot{margin:0}
.group-dialog{width:650px}.group-scope{display:flex;flex-direction:row;align-items:center;gap:12px;margin-top:14px}.group-scope select{flex:1}.group-profile-list{list-style:none;padding:0;margin:12px 0;border:1px solid #e5e9f0;border-radius:5px;max-height:220px;overflow:auto}.group-profile-list li{display:flex;gap:5px;align-items:center;padding:9px;border-bottom:1px solid #edf0f5}.group-profile-list li:last-child{border:0}.group-profile-list label{flex:1;min-width:0;display:flex;align-items:center;gap:9px}.group-profile-list label span{min-width:0}.group-profile-list strong{display:block;font-size:12px;font-weight:500;overflow-wrap:anywhere}.group-profile-list small{display:block;color:#8a97aa;font-size:10px;margin-top:4px}.group-profile-list button,.group-name-list button{font-size:11px;padding:5px 8px;flex-shrink:0}.group-dialog h3{font-size:13px;font-weight:500;margin:22px 0 8px}.group-name-list{list-style:none;padding:0}.group-name-list li{display:flex;align-items:center;gap:8px;padding:10px 0;border-top:1px solid #edf0f5;flex-wrap:wrap}.group-name-list strong{font-size:12px;font-weight:500;flex:1;overflow-wrap:anywhere}.group-name-list small{font-size:10px;color:#8a97aa}.group-name-list p{width:100%;line-height:1.7;margin:0}.group-name-list form{display:flex;width:100%;gap:6px}.group-name-list form input{flex:1;min-width:0}
.group-tabs{display:flex;gap:16px;border-bottom:1px solid #e5e9f0;margin:14px 0}.group-tabs button{border:0;border-radius:0;background:transparent;padding:8px 3px;color:#8491a5;font-size:12px;border-bottom:2px solid transparent}.group-tabs button[aria-selected=true]{color:#2563eb;border-bottom-color:#2563eb}.group-tabs+div>h3{margin-top:12px}
</style>
