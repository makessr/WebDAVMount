<script setup lang="ts">
import {computed, ref, watch} from 'vue'
import {canSwapDrives, previewDriveChanges, type DrivePlan} from './connection-drives'

const props = defineProps<{plan: DrivePlan, busy: boolean, error: string}>()
const emit = defineEmits<{save: [changes: ReturnType<typeof previewDriveChanges>['changes']], refresh: [], cancel: []}>()
const targets = ref<Record<string, string>>({})
const draft = computed(() => previewDriveChanges(props.plan, targets.value))
watch(() => props.plan, () => {targets.value = {}})

function swap() {
  const [first, second] = props.plan.rows
  targets.value = {[first.id]: second.drive, [second.id]: first.drive}
}

function save() {
  if (!props.busy && draft.value.changed && !draft.value.conflictIds.size) {
    emit('save', draft.value.changes)
  }
}
</script>

<template>
  <form class="drive-editor" aria-labelledby="drive-editor-title" @submit.prevent="save">
    <div class="drive-editor-heading">
      <h3 id="drive-editor-title">调整盘符</h3>
      <button v-if="canSwapDrives(plan)" type="button" :disabled="busy" @click="swap">交换原盘符</button>
    </div>
    <p class="field-help">可使用本次所选网盘腾出的盘符。保存后，下次挂载使用新盘符。</p>
    <div class="drive-editor-list">
      <table>
        <thead><tr><th scope="col">网盘</th><th scope="col">原盘符</th><th scope="col">目标盘符</th></tr></thead>
        <tbody>
          <tr v-for="row in plan.rows" :key="row.id">
            <td>{{ row.name }}</td>
            <td>{{ row.drive }}</td>
            <td>
              <select :value="targets[row.id] ?? row.drive" :aria-label="row.name + '的目标盘符'"
                :aria-invalid="draft.conflictIds.has(row.id) || undefined" :disabled="busy"
                @change="targets[row.id] = ($event.target as HTMLSelectElement).value">
                <option :value="row.drive">{{ row.drive }}（保留）</option>
                <option v-for="drive in plan.available.filter(item => item !== row.drive)" :key="drive" :value="drive">{{ drive }}</option>
              </select>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="draft.conflictIds.size" class="danger-text" role="alert">有重复或不可用的目标盘符，请修改标出的选项。</p>
    <p v-else-if="!plan.available.length" class="field-help">当前没有可分配的盘符。释放盘符后，可重新读取。</p>
    <p v-if="error" class="danger-text" role="alert">{{ error }}</p>
    <div class="drive-editor-actions">
      <button class="primary" :disabled="busy || !draft.changed || !!draft.conflictIds.size">保存 {{ draft.changed }} 项调整</button>
      <button type="button" :disabled="busy" @click="emit('cancel')">取消</button>
      <button type="button" class="text-button" :disabled="busy" @click="emit('refresh')">重新读取盘符</button>
    </div>
  </form>
</template>

<style scoped>
.drive-editor {margin-top:12px;padding:12px;border:1px solid #d9e5ff;border-radius:5px;background:#f8faff}
.drive-editor-heading,.drive-editor-actions {display:flex;align-items:center;flex-wrap:wrap;gap:8px}
.drive-editor-heading h3 {margin:0 auto 0 0;font-size:13px}
.drive-editor-list {max-height:190px;overflow:auto;border:1px solid var(--line);border-radius:4px;background:white}
table {width:100%;border-collapse:collapse;text-align:left}
th,td {padding:6px 9px;border-bottom:1px solid var(--line);overflow-wrap:anywhere}
th {position:sticky;top:0;background:var(--surface);font-weight:500}
th:not(:first-child) {width:106px}
select[aria-invalid="true"] {border-color:var(--danger)}
.drive-editor-actions {margin-top:10px}
.drive-editor p {margin:8px 0}
</style>
