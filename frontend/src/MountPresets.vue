<script setup lang="ts">
import {computed, onMounted, ref} from 'vue'
import {connectionGroups, mountPresetKey, type GroupedProfile} from './connection-groups'
import {errorMessage} from './operation-feedback'

import type {MountPreset} from './connection-management'

const props = defineProps<{
  presets: MountPreset[], profiles: GroupedProfile[], initialSelection: string[], busy: boolean, online: boolean,
  readError: string, api: (name: string, ...args: any[]) => Promise<any>,
  run: (fn: () => Promise<void>) => Promise<void>, reload: () => Promise<void>,
}>()
const emit = defineEmits<{notice: [message: string], apply: [id: string, mount: boolean], switchPreset: []}>()
const known = computed(() => new Set(props.profiles.map(mountPresetKey)))
const groups = computed(() => connectionGroups(props.profiles))
const active = ref(props.initialSelection.length > 0)
const editing = ref('')
const name = ref('')
const members = ref(props.profiles.filter(profile => props.initialSelection.includes(profile.id)).map(mountPresetKey))
const deleting = ref('')
const startupSetting = ref<{id:string, warning?:string} | null>(null)
const startupDraft = ref('')
const startupReadError = ref('')
const startupMissing = computed(() => !!startupSetting.value?.id && !props.presets.some(p => p.id === startupSetting.value?.id))

async function readStartupSetting() {
  startupReadError.value = ''
  try {
    startupSetting.value = await props.api('StartupPreset')
    startupDraft.value = startupSetting.value?.id ?? ''
  } catch (error) {
    startupReadError.value = errorMessage(error)
  }
}
onMounted(readStartupSetting)

async function saveStartupSetting() {
  const id = startupDraft.value
  await props.run(async () => {
    await props.api('SaveStartupPreset', id)
    startupSetting.value = {id}
    emit('notice', '启动挂载设置已保存，下次打开程序时生效')
  })
}

function missing(preset: MountPreset) {
  return preset.connectionIDs.filter(id => !known.value.has(id)).length
}

function edit(preset?: MountPreset) {
  editing.value = preset?.id ?? ''
  name.value = preset?.name ?? ''
  members.value = preset?.connectionIDs.filter(id => known.value.has(id)) ?? []
  deleting.value = ''
  active.value = true
}

async function save() {
  await props.run(async () => {
    await props.api('SaveMountPreset', editing.value, name.value, [...members.value])
    active.value = false
    await props.reload()
    emit('notice', '挂载方案已保存')
  })
}

async function remove(id: string) {
  await props.run(async () => {
    await props.api('DeleteMountPreset', id)
    deleting.value = ''
    await props.reload()
    emit('notice', '挂载方案已删除')
  })
}
</script>

<template>
  <section aria-label="挂载方案" class="mount-presets">
    <div class="preset-actions">
      <button type="button" :disabled="busy || !profiles.length || presets.length >= 16 || !!readError" @click="edit()">新建方案</button>
      <button v-if="presets.length >= 2" type="button" :disabled="busy || !online || !!readError" @click="emit('switchPreset')">切换挂载方案…</button>
      <button v-if="readError" type="button" :disabled="busy" @click="run(reload)">重新读取方案</button>
    </div>
    <p v-if="readError" class="danger-text" role="alert">{{ readError }}</p>
    <p v-else-if="!presets.length && !active" class="field-help">把常用网盘保存为一套方案，可从侧栏「批量」再次使用。</p>
    <div v-if="startupReadError" class="preset-actions">
      <p class="danger-text" role="alert">{{ startupReadError }}</p>
      <button type="button" :disabled="busy" @click="readStartupSetting">重读启动设置</button>
    </div>
    <form v-if="startupSetting && (presets.length || startupSetting.id || startupSetting.warning)" class="preset-startup" @submit.prevent="saveStartupSetting">
      <label class="preset-startup-mode">
        <strong>打开程序时挂载</strong>
        <select v-model="startupDraft" :disabled="busy">
          <option value="">按各网盘的自动挂载设置</option>
          <option v-if="startupMissing" :value="startupSetting.id" disabled>{{ readError ? '当前方案暂不可读取' : '当前方案已不存在' }}</option>
          <option v-for="preset in presets" :key="preset.id" :value="preset.id">{{ preset.name }}</option>
        </select>
      </label>
      <button type="submit" :disabled="busy || (!startupSetting.warning && startupDraft === startupSetting.id)">保存启动设置</button>
      <p v-if="startupSetting.warning" class="danger-text" role="alert">{{ startupSetting.warning }}</p>
      <p v-else class="field-help">选择方案后，下次打开程序时只挂载该方案的网盘。</p>
    </form>
    <ul v-if="presets.length" class="preset-list">
      <li v-for="preset in presets" :key="preset.id">
        <div class="preset-title">
          <strong>{{ preset.name }}</strong>
          <small>{{ preset.connectionIDs.length }} 个网盘</small>
          <small v-if="startupSetting?.id === preset.id">启动时使用</small>
          <small v-if="missing(preset)" class="danger-text">{{ missing(preset) }} 个连接已删除，请编辑方案</small>
        </div>
        <div v-if="deleting === preset.id" class="preset-actions">
          <span>删除这套方案？</span>
          <button type="button" class="danger-text" :disabled="busy" @click="remove(preset.id)">确认删除方案</button>
          <button type="button" :disabled="busy" @click="deleting = ''">取消</button>
        </div>
        <div v-else class="preset-actions">
          <button type="button" :disabled="busy || !online || !!missing(preset)" :aria-label="'挂载 ' + preset.name" @click="emit('apply', preset.id, true)">挂载</button>
          <button type="button" :disabled="busy || !online || !!missing(preset)" :aria-label="'卸载 ' + preset.name" @click="emit('apply', preset.id, false)">卸载</button>
          <button type="button" :disabled="busy" :aria-label="'编辑 ' + preset.name" @click="edit(preset)">编辑</button>
          <button type="button" :disabled="busy || startupSetting?.id === preset.id" :title="startupSetting?.id === preset.id ? '请先切换启动挂载方式' : undefined" :aria-label="'删除 ' + preset.name" @click="active = false; deleting = preset.id">删除…</button>
        </div>
      </li>
    </ul>
    <form v-if="active && !readError" class="preset-form" @submit.prevent="save">
      <label>方案名称<input v-model="name" required :disabled="busy" placeholder="例如：工作常用"></label>
      <div class="preset-members" aria-label="方案网盘">
        <fieldset v-for="group in groups" :key="group.name">
          <legend>{{ group.label }}</legend>
          <label v-for="profile in group.items" :key="profile.id">
            <input v-model="members" type="checkbox" :value="mountPresetKey(profile)" :disabled="busy || (members.length >= 200 && !members.includes(mountPresetKey(profile)))">
            <span>{{ profile.name }}（{{ profile.drive }}）</span>
          </label>
        </fieldset>
      </div>
      <div class="preset-actions">
        <button class="primary" :disabled="busy || !name.trim() || Array.from(name.trim()).length > 32 || !members.length">保存方案</button>
        <button type="button" :disabled="busy" @click="active = false">取消编辑</button>
        <span>已选 {{ members.length }} 个</span>
      </div>
    </form>
  </section>
</template>

<style scoped>
.preset-actions {display:flex;align-items:center;gap:7px;flex-wrap:wrap}
.preset-startup {display:flex;align-items:end;flex-wrap:wrap;gap:7px 10px;margin:12px 0;padding:10px 12px;border:1px solid var(--line);border-radius:5px;background:var(--surface)}
.preset-startup-mode {display:grid;gap:5px;flex:1 1 240px;min-width:0}
.preset-startup-mode select {width:100%;min-height:30px}
.preset-startup p {flex-basis:100%;margin:0}
.preset-list {padding:0;margin:8px 0;list-style:none;max-height:250px;overflow:auto}
.preset-list li {display:flex;justify-content:space-between;align-items:center;gap:8px;flex-wrap:wrap;padding:8px 0;border-bottom:1px solid var(--line)}
.preset-title {display:grid;gap:4px;min-width:0;overflow-wrap:anywhere}
.preset-title small {color:var(--muted)}
.preset-form {margin-top:12px;padding:12px;border:1px solid var(--line);border-radius:5px;background:var(--surface)}
.preset-form>label {display:grid;gap:5px}
.preset-members {max-height:200px;overflow:auto;margin:10px 0}
.preset-members fieldset {border:0;padding:0;margin:0 0 7px}
.preset-members legend {font-size:11px;color:var(--muted);margin:3px 0}
.preset-members label {display:flex;align-items:center;gap:7px;padding:4px 0;overflow-wrap:anywhere}
</style>
