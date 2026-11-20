<script setup lang="ts">
import {computed, onMounted, ref} from 'vue'
import {errorMessage} from './operation-feedback'

type CheckState = 'success' | 'failed' | 'skipped'
type HistoryItem = {name?:string,state:CheckState,mount:string,previous?:CheckState}
type HistoryEntry = {id:string,recordedAt:string,items:HistoryItem[]}
type HistoryReport = {entries:HistoryEntry[],warning?:string}

const props = defineProps<{
  busy:boolean
  api:(name:string,...args:any[])=>Promise<any>
  run:(fn:()=>Promise<void>)=>Promise<void>
}>()
const emit = defineEmits<{notice:[message:string]}>()
const report = ref<HistoryReport>({entries:[]})
const selected = ref('')
const loading = ref(false)
const readError = ref('')
const confirming = ref(false)
const changesOnly = ref(false)

const current = computed(() => report.value.entries.find(entry => entry.id === selected.value))
const labels:Record<CheckState,string> = {success:'可读取',failed:'读取失败',skipped:'未完成'}
const changed = (row:HistoryItem) => row.state !== 'skipped' && !!row.previous && row.previous !== row.state
const displayed = computed(() => (current.value?.items ?? [])
  .map((row, index) => ({...row, index}))
  .filter(row => !changesOnly.value || changed(row)))
const counts = computed(() => {
  const counts = {success:0,failed:0,skipped:0}
  for (const row of current.value?.items ?? []) counts[row.state]++
  return counts
})
const dateLabel = new Intl.DateTimeFormat('zh-CN',{dateStyle:'short',timeStyle:'medium'})

async function reload() {
  loading.value = true
  readError.value = ''
  try {
    report.value = await props.api('ConnectionCheckHistory') as HistoryReport
    if (!current.value) selected.value = report.value.entries[0]?.id ?? ''
  } catch (error) {
    readError.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}
onMounted(reload)

async function clearHistory() {
  await props.run(async () => {
    await props.api('ClearConnectionCheckHistory')
    confirming.value = false
    changesOnly.value = false
    await reload()
    emit('notice','体检历史已清除')
  })
}

async function exportHistory() {
  const rows = displayed.value.map(({state,mount}) => ({state,mount}))
  await props.run(async () => {
    const path = await props.api('ExportConnectionCheckReport',rows)
    emit('notice',path ? '脱敏体检报告已保存' : '已取消保存报告')
  })
}
</script>

<template>
  <div class="check-history" aria-label="体检历史">
    <p v-if="loading" role="status">正在读取体检历史…</p>
    <p v-if="readError || report.warning" class="danger-text" role="alert">{{ readError || report.warning }}</p>
    <div class="history-tools">
      <label v-if="report.entries.length">检查时间
        <select v-model="selected" :disabled="busy || loading">
          <option v-for="entry in report.entries" :key="entry.id" :value="entry.id">
            {{ dateLabel.format(new Date(entry.recordedAt)) }} · {{ entry.items.length }} 个网盘
          </option>
        </select>
      </label>
      <button v-if="readError" :disabled="busy || loading" @click="reload">重新读取</button>
      <button v-if="(report.entries.length || report.warning) && !confirming" :disabled="busy || loading" @click="confirming = true">清除历史…</button>
    </div>
    <div v-if="confirming" class="history-confirm">
      <p>清除本机保存的全部体检记录？</p>
      <button class="danger-text" :disabled="busy" @click="clearHistory">确认清除历史</button>
      <button :disabled="busy" @click="confirming = false">取消</button>
    </div>
    <template v-if="current">
      <div class="history-summary">
        <span role="status">可读取 {{ counts.success }} · 失败 {{ counts.failed }} · 未完成 {{ counts.skipped }}</span>
        <label><input v-model="changesOnly" type="checkbox"> 仅显示读取状态有变化的网盘</label>
        <button :disabled="busy || !displayed.length" @click="exportHistory">保存脱敏报告…</button>
      </div>
      <ul v-if="displayed.length" aria-label="本轮体检记录">
        <li v-for="item in displayed" :key="item.index">
          <div><strong>{{ item.name || '已无法识别的网盘' }}</strong><small>{{ item.mount }}</small></div>
          <div><span :class="{ 'danger-text':item.state === 'failed' }">{{ labels[item.state] }}</span>
            <small v-if="changed(item)">{{ item.state === 'success' ? '已恢复读取' : '从可读取变为失败' }}</small>
          </div>
        </li>
      </ul>
      <p v-else class="field-help">本轮没有与上次已完成结果不同的网盘。</p>
    </template>
    <p v-else-if="!loading && !readError && !report.warning" class="field-help">完成一次连接体检后，记录会显示在这里。</p>
    <p class="field-help">本机保留最近 12 轮。每个网盘与它上次已完成的检查结果比较。</p>
  </div>
</template>

<style scoped>
.check-history {font-size:12px;margin-top:10px}
.history-tools,.history-summary,.history-confirm {display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin:8px 0}
.history-tools label,.history-summary label {display:flex;align-items:center;gap:6px;flex-wrap:wrap}
.history-confirm {padding:8px 10px;background:var(--surface);border:1px solid var(--line);border-radius:5px}
.history-confirm p {margin:0}
ul {list-style:none;margin:0;padding:0;max-height:215px;overflow:auto}
li {padding:8px 0;display:flex;gap:10px;justify-content:space-between;flex-wrap:wrap;border-top:1px solid var(--line)}
li div {display:grid;gap:3px;overflow-wrap:anywhere}
small {color:var(--muted)}
</style>
