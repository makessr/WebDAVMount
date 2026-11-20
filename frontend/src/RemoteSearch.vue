<script setup lang="ts">
import {ref,computed,onUnmounted,watch} from 'vue'
import UiIcon from './UiIcon.vue'
import {emptySearch,searchPage,searchLocation,searchComplete,type SearchItem} from './remote-search'
import {sortDirectory,type DirectoryOrder} from './directory-view'
import {columnDirection,nextColumnOrder,type SortColumn} from './directory-columns'
import {formatModified} from './directory-modified'
import {fileSize} from './workspace-flow'
const props=defineProps<{profileId:string,profileName:string,currentPath:string,busy:boolean,error:string,api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>,cancellable?:boolean)=>Promise<void>}>()
const emit=defineEmits<{close:[],locate:[location:{directory:string,selectedPath:string}]}>()
const query=ref(''),scope=ref('current'),recursive=ref(true),kind=ref('all'),state=ref(emptySearch()),items=ref<SearchItem[]>([]),page=ref(1),order=ref<DirectoryOrder>('nameAsc'),selected=ref(''),running=ref(false),stopping=ref(false)
const sorted=computed(()=>sortDirectory(items.value,order.value)),paged=computed(()=>searchPage(sorted.value,page.value)),selectedItem=computed(()=>items.value.find(p=>p.Path===selected.value))
watch(order,()=>page.value=1)
function sort(column:SortColumn){if(!props.busy)order.value=nextColumnOrder(order.value,column)}
function arrow(column:SortColumn){const direction=columnDirection(order.value,column);return direction==='ascending'?'↑':direction==='descending'?'↓':''}
let timer:ReturnType<typeof setInterval>|undefined,closed=false,polling=false
async function start(){await props.run(async()=>{
 running.value=true;stopping.value=false;items.value=[];selected.value='';page.value=1;state.value={...emptySearch(),active:true,phase:'searching',message:'正在准备搜索…'}
 timer=setInterval(async()=>{if(closed||polling)return;polling=true;try{const p=await props.api('RemoteSearchProgress');if(!closed&&p.profileId===props.profileId)state.value=p}catch{}finally{polling=false}},400)
 try{state.value=await props.api('SearchRemote',{profileId:props.profileId,base:scope.value==='root'?'':props.currentPath,query:query.value,kind:kind.value,recursive:recursive.value});items.value=state.value.items??[]}
 finally{if(timer)clearInterval(timer);running.value=false;stopping.value=false;try{const p=await props.api('RemoteSearchResults');if(!closed&&p.profileId===props.profileId){state.value=p;items.value=p.items??[]}}catch{}}
},true)}
async function stop(){if(stopping.value||!running.value)return;stopping.value=true;try{await props.api('CancelOperation')}catch{stopping.value=false}}
function locate(item:SearchItem){if(props.busy)return;const location=searchLocation(item);if(location)emit('locate',location)}
onUnmounted(()=>{closed=true;if(timer)clearInterval(timer);if(!running.value)void props.api('ClearRemoteSearch').catch(()=>{})})
</script>
<template>
 <section class="dialog remote-search-dialog" role="dialog" aria-modal="true" aria-labelledby="remote-search-title">
  <div class="dialog-header"><h2 id="remote-search-title">搜索网盘文件</h2><button class="icon-button" aria-label="关闭网盘搜索" :disabled="busy" @click="emit('close')"><UiIcon name="close"/></button></div>
  <div class="dialog-body search-body">
   <p class="page-hint">{{profileName}}</p>
   <form class="remote-search-form" @submit.prevent="start">
    <label class="search-query">文件名关键词<input v-model="query" autofocus maxlength="128" required placeholder="例如：照片、报告、.mp4" :disabled="busy"></label>
    <label>搜索位置<select v-model="scope" :disabled="busy"><option value="current">当前目录{{currentPath?'':'（根目录）'}}</option><option value="root">网盘根目录</option></select></label>
    <label>类型<select v-model="kind" :disabled="busy"><option value="all">文件和文件夹</option><option value="files">仅文件</option><option value="folders">仅文件夹</option></select></label>
    <label class="check-row search-recursive"><input v-model="recursive" type="checkbox" :disabled="busy">包含子文件夹</label>
    <button class="primary" :disabled="busy||!query.trim()"><UiIcon name="search" :size="15"/>开始搜索</button>
   </form>
   <p v-if="scope==='current'&&currentPath" class="field-help search-base">当前目录：{{currentPath}}</p>
   <p v-if="error" class="dialog-error" role="alert">{{error}}</p>
   <div class="search-progress" role="status"><strong>{{state.message}}</strong><span v-if="state.phase!=='idle'">已查 {{state.directories||0}} 个目录 · {{state.scanned||0}} 个条目 · 找到 {{state.matched||0}} 项</span><button v-if="running" :disabled="stopping" @click="stop">{{stopping?'正在停止…':'停止搜索'}}</button></div>
   <p v-if="state.failedDirectories||state.skipped" class="field-help">无法读取 {{state.failedDirectories||0}} 个目录，跳过 {{state.skipped||0}} 个异常或重复条目。</p>
   <div v-if="items.length" class="search-table-scroll"><table class="search-results"><thead><tr><th :aria-sort="columnDirection(order,'name')"><button :disabled="busy" @click="sort('name')">名称 {{arrow('name')}}</button></th><th>所在目录</th><th :aria-sort="columnDirection(order,'size')"><button :disabled="busy" @click="sort('size')">大小 {{arrow('size')}}</button></th><th :aria-sort="columnDirection(order,'modified')"><button :disabled="busy" @click="sort('modified')">修改时间 {{arrow('modified')}}</button></th></tr></thead><tbody><tr v-for="item in paged.items" :key="item.Path" :class="{selected:selected===item.Path}" tabindex="0" @focus="selected=item.Path" @keydown.enter.prevent="locate(item)" @keydown.space.prevent="selected=item.Path" @click="selected=item.Path" @dblclick="locate(item)"><td :title="item.Name"><UiIcon :name="item.IsDir?'folder':'file'" :size="16"/>{{item.Name}}</td><td class="search-location" :title="item.Path">{{item.Path.includes('/')?item.Path.slice(0,item.Path.lastIndexOf('/')):'根目录'}}</td><td>{{item.IsDir?'—':fileSize(item.Size)}}</td><td>{{formatModified(item.ModTime)}}</td></tr></tbody></table></div>
   <div v-else-if="!running&&state.phase!=='idle'" class="search-empty">{{searchComplete(state)?'当前搜索范围内没有匹配项。':'当前没有结果；搜索未完成，不代表网盘中不存在匹配项。'}}</div>
   <div v-if="items.length" class="search-result-footer"><span>关键词：{{state.query}} · 共 {{items.length}} 项</span><button :disabled="busy||paged.page<=1" @click="page=paged.page-1">上一页</button><span>{{paged.page}} / {{paged.pages}}</span><button :disabled="busy||paged.page>=paged.pages" @click="page=paged.page+1">下一页</button></div>
   <label v-if="selectedItem" class="search-selected">已选路径<input readonly :value="selectedItem.Path" @focus="($event.target as HTMLInputElement).select()"></label>
   
  </div>
  <div class="dialog-footer"><button :disabled="busy" @click="emit('close')">关闭</button><button class="primary" :disabled="busy||!selectedItem" @click="selectedItem&&locate(selectedItem)">{{selectedItem?.IsDir?'打开文件夹':'定位到所在目录'}}</button></div>
 </section>
</template>
