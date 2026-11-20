<script setup lang="ts">
import {computed,ref} from 'vue'
import UiIcon from './UiIcon.vue'
import {batchSaveStates,mountedSaveSummary,type MountedSaveRecord} from './mounted-save-history'

const props=defineProps<{
  records:MountedSaveRecord[]
  selectedId:string|null
  blocked:boolean
  retryReady:boolean
  warning:string
}>()
defineEmits<{select:[id:string],retry:[record:MountedSaveRecord],clear:[],openFolder:[id:string],forget:[record:MountedSaveRecord]}>()

const query=ref(''),onlyPending=ref(false)
const visibleRecords=computed(()=>{
  const text=query.value.trim().toLocaleLowerCase()
  return props.records.filter(record=>
    (!onlyPending.value||record.retryCount>0)&&
    (!text||[record.sourceName,record.folder,...record.items.map(item=>item.name)].some(value=>value.toLocaleLowerCase().includes(text))))
})
</script>

<template>
  <section class="save-history-page" aria-label="另存记录">
    <div class="save-history-actions save-history-toolbar">
      <span class="page-hint">本机加密保留最近 15 批，可在重启后查看。</span>
      <button v-if="records.length||warning" :disabled="blocked" @click="$emit('clear')">清除记录…</button>
    </div>
    <p v-if="warning" class="setting-warning" role="status">{{warning}}</p>
    <div v-if="records.length" class="save-history-filters">
      <label class="search-box"><UiIcon name="search" :size="16"/><input v-model="query" type="search" aria-label="搜索另存记录" placeholder="搜索网盘、文件名或保存位置" :disabled="blocked"></label>
      <label class="save-history-filter-check"><input v-model="onlyPending" type="checkbox" :disabled="blocked">仅待重试</label>
      <span class="page-hint" role="status">{{visibleRecords.length}} / {{records.length}} 批</span>
    </div>
    <div v-if="!records.length" class="empty-state">
      <span class="empty-icon"><UiIcon name="folder" :size="36"/></span>
      <h2>暂无批量另存记录</h2>
      <p>挂载网盘后，在文件列表中使用「批量另存」。</p>
    </div>
    <div v-else-if="!visibleRecords.length" class="empty-state">
      <h2>没有匹配的批次</h2>
      <button :disabled="blocked" @click="query='';onlyPending=false">清除筛选</button>
    </div>
    <template v-else>
      <details v-for="record in visibleRecords" :key="record.id" class="save-history-record" :open="selectedId===record.id">
        <summary @click.prevent="$emit('select',record.id)">
          <div><strong>{{record.sourceName}}</strong><span>{{mountedSaveSummary(record)}}</span></div>
          <div class="save-history-meta"><time :datetime="record.completedAt">{{new Date(record.completedAt).toLocaleString('zh-CN',{hour12:false})}}</time><UiIcon :name="selectedId===record.id?'down':'chevron'" :size="14"/></div>
        </summary>
        <div class="save-history-detail">
          <div class="save-history-folder">
            <p class="selectable-path">{{record.folder}}</p>
            <button :disabled="blocked" @click="$emit('openFolder',record.id)"><UiIcon name="folder" :size="16"/>打开保存位置</button>
          </div>
          <table class="save-history-files">
            <thead><tr><th scope="col">文件</th><th scope="col">结果</th></tr></thead>
            <tbody>
              <tr v-for="(item,index) in record.items" :key="index">
                <td>{{item.name}}</td>
                <td>{{batchSaveStates[item.state]}}<small v-if="item.message">{{item.message}}</small></td>
              </tr>
            </tbody>
          </table>
          <div class="save-history-actions">
            <span v-if="record.retryCount&&!retryReady" class="page-hint">请先确认原网盘连接并挂载。</span>
            <span v-else-if="record.retried" class="page-hint">已发起后续重试。</span>
            <button class="danger-text" :disabled="blocked" @click="$emit('forget',record)">移除此批记录…</button>
            <button v-if="record.retryCount" :disabled="blocked||!retryReady" @click="$emit('retry',record)">重试未保存的 {{record.retryCount}} 个文件…</button>
          </div>
        </div>
      </details>
    </template>
  </section>
</template>
