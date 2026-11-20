<script setup lang="ts">
import {computed, ref, watch} from 'vue'
import ConnectionCheckHistory from './ConnectionCheckHistory.vue'
import {connectionGroups} from './connection-groups'
import {mergeConnectionChecks, visibleSelection, type ConnectionCheckItem, type ManagedProfile} from './connection-management'

const props = defineProps<{
  profiles: ManagedProfile[]
  busy: boolean
  online: boolean
  api: (name: string, ...args: any[]) => Promise<any>
  run: (fn: () => Promise<void>) => Promise<void>
}>()
const emit = defineEmits<{notice: [message: string]}>()

const results = defineModel<ConnectionCheckItem[]>({required:true})
const selected = defineModel<string[]>('selected', {required:true})
const running = ref(false)
const stopping = ref(false)
const cancelError = ref('')
const showHistory = ref(false)
const onlyIssues = ref(false)
const group = ref<string | null>(null)
const groups = computed(() => connectionGroups(props.profiles))
const visible = computed(() => props.profiles.filter(item => group.value === null || (item.group || '') === group.value))
const picked = computed(() => visibleSelection(selected.value, visible.value))
watch(group, () => {selected.value = picked.value})
const progress = ref({completed: 0, total: 0})
const savedResults = computed(() => mergeConnectionChecks(results.value, [], props.profiles))
const currentResults = computed(() => {
  const ids = new Set(visible.value.map(item => item.id))
  return savedResults.value.filter(item => ids.has(item.id))
})
const hiddenCount = computed(() => savedResults.value.length - currentResults.value.length)
const issues = computed(() => currentResults.value.filter(item => item.state !== 'success'))
const displayed = computed(() => onlyIssues.value ? issues.value : currentResults.value)
const counts = computed(() => ({
  success: currentResults.value.filter(item => item.state === 'success').length,
  failed: currentResults.value.filter(item => item.state === 'failed').length,
  skipped: currentResults.value.filter(item => item.state === 'skipped').length,
}))

async function check(ids: string[], retry = false) {
  if (!ids.length || ids.length > 200 || props.busy || running.value) return
  running.value = true
  stopping.value = false
  cancelError.value = ''
  progress.value = {completed: 0, total: ids.length}
  let polling = false
  let stopped = false
  const timer = setInterval(async () => {
    if (polling) return
    polling = true
    try {
      const state = await props.api('ConnectionCheckProgress')
      if (!stopped && state.active && state.total === ids.length) {
        progress.value = {completed: state.completed, total: state.total}
      }
    } catch {
      // Progress is optional until the current batch returns its results.
    } finally {
      polling = false
    }
  }, 300)
  try {
    await props.run(async () => {
      const checked = await props.api('CheckConnections', ids) as ConnectionCheckItem[]
      results.value = mergeConnectionChecks(results.value, checked, props.profiles)
      if (!retry) onlyIssues.value = false
    })
  } finally {
    stopped = true
    clearInterval(timer)
    running.value = false
    stopping.value = false
  }
}

async function exportReport() {
  const rows = displayed.value.map(({state, mount}) => ({state, mount}))
  if (!rows.length || props.busy) return
  await props.run(async () => {
    const path = await props.api('ExportConnectionCheckReport', rows)
    emit('notice', path ? '脱敏体检报告已保存' : '已取消保存报告')
  })
}

async function stop() {
  stopping.value = true
  cancelError.value = ''
  try {
    await props.api('CancelOperation')
  } catch {
    stopping.value = false
    cancelError.value = '停止请求未完成，请重试'
  }
}
</script>

<template>
  <section class="connection-check" aria-label="连接体检">
    <div class="check-actions check-view">
      <button :disabled="busy || running" :aria-pressed="!showHistory" @click="showHistory = false">检查网盘</button>
      <button :disabled="busy || running" :aria-pressed="showHistory" @click="showHistory = true">体检历史</button>
    </div>
    <ConnectionCheckHistory v-if="showHistory" :busy="busy" :api="api" :run="run" @notice="emit('notice',$event)" />
    <template v-else>
    <div class="check-actions">
      <label>体检分组 <select v-model="group" :disabled="busy">
        <option :value="null">全部分组</option>
        <option v-for="entry in groups" :key="entry.name" :value="entry.name">{{ entry.label }}</option>
      </select></label>
      <button type="button" :disabled="busy || !visible.length || visible.length > 200"
        @click="selected = picked.length === visible.length ? [] : visible.map(item => item.id)">
        {{ picked.length && picked.length === visible.length ? '取消全选' : '全选当前分组' }}
      </button>
      <span>已选 {{ picked.length }} / {{ visible.length }} 个</span>
    </div>
    <ul v-if="visible.length" class="check-pick" aria-label="选择要检查的网盘">
      <li v-for="item in visible" :key="item.id">
        <label><input v-model="selected" type="checkbox" :value="item.id"
          :disabled="busy || (!picked.includes(item.id) && picked.length >= 200)"> {{ item.name }}</label>
      </li>
    </ul>
    <p v-else class="field-help">当前分组没有可检查的网盘。</p>
    <div class="check-actions">
      <button class="primary" :disabled="busy || !online || !picked.length" @click="check(picked)">检查所选连接</button>
      <button v-if="issues.length" :disabled="busy || !online || issues.length > 200" @click="check(issues.map(item => item.id), true)">
        重试可见失败和未完成项（{{ issues.length }}）
      </button>
      <button v-if="running" :disabled="stopping" @click="stop">{{ stopping ? '正在停止…' : '停止检查' }}</button>
    </div>
    <div v-if="running" class="check-progress">
      <progress :value="progress.completed" :max="progress.total" aria-label="体检进度"></progress>
      <span role="status">已处理 {{ progress.completed }} / {{ progress.total }} 个</span>
    </div>
    <p v-if="cancelError" role="alert">{{ cancelError }}</p>
    <p v-if="issues.length > 200" class="field-help">请缩小筛选范围，每次最多重试 200 个连接。</p>
    <p v-if="hiddenCount" class="field-help">另有 {{ hiddenCount }} 条体检结果，调整筛选条件后可查看。</p>
    <template v-if="currentResults.length">
      <div class="check-summary">
        <span role="status">当前筛选中，成功 {{ counts.success }}，失败 {{ counts.failed }}，未完成 {{ counts.skipped }}</span>
        <label><input v-model="onlyIssues" type="checkbox">仅显示失败和未完成项</label>
        <button :disabled="busy || !displayed.length || displayed.length > 1000" @click="exportReport">保存脱敏报告…</button>
      </div>
      
      <ul v-if="displayed.length" aria-label="连接体检结果">
        <li v-for="item in displayed" :key="item.id">
          <strong>{{ item.name }}（{{ item.drive }}）</strong>
          <small>{{ item.mount }}<template v-if="item.checkedAt"> · {{ new Date(item.checkedAt).toLocaleTimeString() }}</template></small>
          <span :class="{ 'danger-text': item.state === 'failed' }">{{ item.message }}</span>
        </li>
      </ul>
      <p v-else class="field-help">没有失败或未完成项。</p>
    </template>
    </template>
  </section>
</template>

<style scoped>
.connection-check {padding:6px 0;font-size:11px}
.check-actions,.check-summary,.check-summary label {display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.check-actions button {font-size:11px;padding:6px 9px}
.check-view {margin-bottom:10px}
.check-view button[aria-pressed="true"] {color:var(--primary);border-color:var(--primary)}
.check-summary {margin-top:12px;justify-content:space-between}
.check-pick {border:1px solid var(--line);border-radius:5px;margin:8px 0 12px;padding:0 10px}
.check-pick label,.check-progress {display:flex;align-items:center;gap:8px}
.check-progress {margin:10px 0}
.check-progress progress {flex:1;min-width:0;accent-color:var(--primary)}
ul {list-style:none;margin:8px 0 0;padding:0;max-height:180px;overflow:auto}
li {display:grid;gap:4px;padding:8px 0;border-bottom:1px solid var(--line);overflow-wrap:anywhere}
small {color:var(--muted)}
</style>
