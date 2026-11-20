<script setup lang="ts">
import {computed, nextTick, onUnmounted, ref, watch} from 'vue'
import UiIcon from './UiIcon.vue'
import {searchLocation, searchPage, type SearchItem} from './remote-search'
import {sortDirectory, type DirectoryOrder} from './directory-view'
import {nextColumnOrder, columnDirection} from './directory-columns'

type SearchProfile={id:string,name:string,drive:string}
type Match=SearchItem & {profileId:string,profileName:string}
type SearchDrive={id:string,name:string,phase:string,message:string,matched:number}
type SearchReport={query:string,phase:string,items:Match[],drives:SearchDrive[]}
type SearchProgress={active:boolean,completed:number,total:number,name?:string}
const props=defineProps<{
  profiles:SearchProfile[],initialId:string,busy:boolean,error:string,
  api:(name:string,...args:any[])=>Promise<any>,
  run:(fn:()=>Promise<void>,cancellable?:boolean)=>Promise<void>,
}>()
const emit=defineEmits<{close:[],locate:[location:{profileId:string,directory:string,selectedPath:string}]}>()
const chosen=ref(props.initialId?[props.initialId]:[])
const query=ref(''),kind=ref('all'),recursive=ref(true),running=ref(false),stopping=ref(false)
const report=ref<SearchReport|null>(null),progress=ref<SearchProgress|null>(null)
const cancelError=ref('')
const resultSummary=ref<HTMLElement|null>(null)
const source=ref(''),page=ref(1),order=ref<DirectoryOrder>('nameAsc')
const picked=computed(()=>props.profiles.filter(p=>chosen.value.includes(p.id)).map(p=>p.id))
const drivesWithMatches=computed(()=>report.value?.drives.filter(p=>p.matched>0)??[])
const filtered=computed(()=>report.value?.items.filter(item=>!source.value||item.profileId===source.value)??[])
const paged=computed(()=>searchPage(sortDirectory(filtered.value,order.value),page.value))
const summary=computed(()=>{
  switch(report.value?.phase){
    case 'done': return '搜索完成'
    case 'partial': return '搜索结束，部分结果不完整'
    case 'limited': return '已到总时长上限，保留已找到的结果'
    case 'cancelled': return '搜索已停止，保留已找到的结果'
    default: return ''
  }
})
const driveLabel=(id:string)=>props.profiles.find(p=>p.id===id)?.drive??''
watch([source,order],()=>{page.value=1})
let timer:ReturnType<typeof setInterval>|undefined
let polling=false,closed=false
let runSerial=0

async function start(){
  if(props.busy||running.value||!picked.value.length||picked.value.length>8||!query.value.trim())return
  const request={profileIds:picked.value,query:query.value,kind:kind.value,recursive:recursive.value}
  await props.run(async()=>{
    const serial=++runSerial
    running.value=true
    stopping.value=false
    cancelError.value=''
    report.value=null
    source.value=''
    page.value=1
    progress.value={active:true,completed:0,total:request.profileIds.length}
    timer=setInterval(async()=>{
      if(polling||closed||!running.value)return
      polling=true
      try{
        const current=await props.api('MultiRemoteSearchProgress') as SearchProgress
        if(!closed&&running.value&&serial===runSerial&&current.active&&current.total===request.profileIds.length)progress.value=current
      }catch{}finally{polling=false}
    },400)
    try{
      report.value=await props.api('SearchMultipleRemotes',request) as SearchReport
      await nextTick()
      resultSummary.value?.focus({preventScroll:true})
      resultSummary.value?.scrollIntoView({block:'start'})
    }
    finally{
      if(timer)clearInterval(timer)
      running.value=false
      stopping.value=false
    }
  },true)
}
async function stop(){
  if(stopping.value||!running.value)return
  stopping.value=true
  cancelError.value=''
  try{await props.api('CancelOperation')}
  catch{if(running.value){stopping.value=false;cancelError.value='停止请求未完成，请重试。'}}
}
function locate(item:Match){
  if(props.busy)return
  const location=searchLocation(item)
  if(location)emit('locate',{profileId:item.profileId,...location})
}
onUnmounted(()=>{closed=true;if(timer)clearInterval(timer)})
</script>

<template>
  <section class="dialog remote-search-dialog" role="dialog" aria-modal="true" aria-labelledby="multi-search-title">
    <div class="dialog-header"><h2 id="multi-search-title">跨网盘搜索</h2><button class="icon-button" aria-label="关闭跨网盘搜索" :disabled="busy" @click="emit('close')"><UiIcon name="close"/></button></div>
    <div class="dialog-body search-body">
      <form class="multi-search-form" @submit.prevent="start">
        <label class="multi-search-query">文件名关键词<input v-model="query" required maxlength="128" :disabled="busy" placeholder="例如：报告、照片"></label>
        <label>类型<select v-model="kind" :disabled="busy"><option value="all">文件和文件夹</option><option value="files">仅文件</option><option value="folders">仅文件夹</option></select></label>
        <fieldset>
          <legend>网盘范围（已选 {{picked.length}} / 8）</legend>
          <div class="multi-drive-options">
            <label v-for="profile in profiles" :key="profile.id">
              <input v-model="chosen" :value="profile.id" type="checkbox" :disabled="busy||(!chosen.includes(profile.id)&&picked.length>=8)">
              <span>{{profile.name}}（{{profile.drive}}）</span>
            </label>
          </div>
        </fieldset>
        <div class="multi-search-actions">
          <label><input v-model="recursive" type="checkbox" :disabled="busy"> 包含子文件夹</label>
          <button class="primary" :disabled="busy||!picked.length||!query.trim()">搜索所选网盘</button>
        </div>
      </form>
      <p v-if="error" class="dialog-error" role="alert">{{error}}</p>
      <p v-if="cancelError" class="dialog-error" role="alert">{{cancelError}}</p>
      <div v-if="running&&progress" class="search-progress" role="status">
        <strong>{{progress.name?'正在搜索 '+progress.name:'正在准备搜索'}}</strong>
        <span>已处理 {{progress.completed}} / {{progress.total}} 个网盘</span>
        <button :disabled="stopping" @click="stop">{{stopping?'正在停止…':'停止搜索'}}</button>
      </div>
      <template v-if="report">
        <div ref="resultSummary" tabindex="-1" class="search-progress" role="status"><strong>{{summary}}</strong><span>找到 {{report.items.length}} 项</span></div>
        <ul class="multi-search-status" aria-label="各网盘搜索结果">
          <li v-for="drive in report.drives" :key="drive.id"><strong>{{drive.name}}（{{driveLabel(drive.id)}}）</strong><span :class="{'danger-text':drive.phase!=='done'}">{{drive.phase==='done'?'已找到 '+drive.matched+' 项':drive.message}}</span></li>
        </ul>
        <div v-if="report.items.length">
          <label v-if="drivesWithMatches.length>1" class="multi-search-source">按来源筛选
            <select v-model="source" :disabled="busy"><option value="">全部来源</option><option v-for="drive in drivesWithMatches" :key="drive.id" :value="drive.id">{{drive.name}}（{{driveLabel(drive.id)}}）</option></select>
          </label>
          <div class="search-table-scroll"><table class="search-results">
            <thead><tr><th :aria-sort="columnDirection(order,'name')"><button :disabled="busy" @click="order=nextColumnOrder(order,'name')">名称</button></th><th>来源网盘</th><th>所在目录</th><th>操作</th></tr></thead>
            <tbody><tr v-for="item in paged.items" :key="JSON.stringify([item.profileId,item.Path])">
              <td :title="item.Name"><UiIcon :name="item.IsDir?'folder':'file'" :size="16"/>{{item.Name}}</td>
              <td>{{item.profileName}}（{{driveLabel(item.profileId)}}）</td>
              <td class="search-location" :title="item.Path">{{item.Path.includes('/')?item.Path.slice(0,item.Path.lastIndexOf('/')):'根目录'}}</td>
              <td><button :disabled="busy" :aria-label="(item.IsDir?'打开 ':'定位 ')+item.profileName+' '+item.Name" @click="locate(item)">{{item.IsDir?'打开':'定位'}}</button></td>
            </tr></tbody>
          </table></div>
          <div class="search-result-footer"><span>显示 {{paged.total}} 项</span><button :disabled="busy||paged.page<=1" @click="page=paged.page-1">上一页</button><span>{{paged.page}} / {{paged.pages}}</span><button :disabled="busy||paged.page>=paged.pages" @click="page=paged.page+1">下一页</button></div>
        </div>
        <p v-else class="search-empty">{{report.phase==='done'?'所选网盘没有匹配项。':'当前没有结果，部分网盘尚未完成。'}}</p>
      </template>
      
    </div>
    <div class="dialog-footer"><button :disabled="busy" @click="emit('close')">关闭</button></div>
  </section>
</template>

<style scoped>
.multi-search-form {display:grid;grid-template-columns:minmax(0,1fr) minmax(120px,.42fr);gap:10px 14px}
.multi-search-form>label {display:grid;gap:4px}
fieldset {grid-column:1/-1;border:1px solid var(--line);border-radius:5px;margin:0;padding:6px 10px;min-width:0}
legend {color:var(--muted);font-size:11px}
.multi-drive-options {display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:5px 12px;max-height:92px;overflow-y:auto}
.multi-drive-options label {display:flex;align-items:center;gap:6px;overflow-wrap:anywhere}
.multi-drive-options input {flex:none}
.multi-search-actions {grid-column:1/-1;display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap}
.multi-search-status {list-style:none;margin:8px 0;padding:0;max-height:110px;overflow:auto}
.multi-search-status li {display:flex;gap:10px;flex-wrap:wrap;padding:4px 0;border-bottom:1px solid var(--line);font-size:11px}
.multi-search-source {display:flex;align-items:center;gap:8px;margin:8px 0}
.multi-search-source select {max-width:75%}
.search-results th:nth-child(1) {width:33%}
.search-results th:nth-child(2) {width:25%}
.search-results th:nth-child(3) {width:29%}
.search-results th:nth-child(4) {width:13%}
.search-results td button {padding:3px 8px;min-height:26px}
</style>
