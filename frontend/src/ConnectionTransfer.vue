<script setup lang="ts">
import {providerLabel} from './storage-providers'
import {ref,computed,onUnmounted,watch} from 'vue'
import {eligibleIndices,selectedImportRows,backupPasswordValid,transferTime,type ImportPreview,type PresetImportRow} from './connection-transfer'
import {errorMessage} from './operation-feedback'
const props=defineProps<{
  profiles:{id:string,name:string,drive:string}[],blocked:boolean,
  api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>)=>Promise<void>,refresh:()=>Promise<void>
}>()
const emit=defineEmits<{notice:[message:string]}>()
const mode=ref<'export'|'import'>('export'),selected=ref(props.profiles.map(p=>p.id))
const password=ref(''),repeat=ref(''),includePasswords=ref(false),passwordConsent=ref(false)
const importPassword=ref(''),preview=ref<ImportPreview|null>(null),indices=ref<number[]>([]),confirmed=ref(false),result=ref(''),savedPath=ref('')
const connectionApplied=ref(false),presetRows=ref<PresetImportRow[]>([]),presetSelection=ref<number[]>([]),presetError=ref('')
const importRows=computed(()=>preview.value?selectedImportRows(preview.value,indices.value):[])
const canExport=computed(()=>selected.value.length>0&&selected.value.length<=200&&backupPasswordValid(password.value)&&password.value===repeat.value&&(!includePasswords.value||passwordConsent.value))
watch(()=>props.profiles.map(p=>p.id).join(','),()=>{selected.value=selected.value.filter(id=>props.profiles.some(p=>p.id===id))})
function discard(){
  const token=preview.value?.token
  preview.value=null;indices.value=[];confirmed.value=false;connectionApplied.value=false
  presetRows.value=[];presetSelection.value=[];presetError.value=''
  if(token)void props.api('DiscardConnectionImport',token).catch(()=>{})
}
function switchMode(next:'export'|'import'){if(props.blocked)return;discard();password.value='';repeat.value='';importPassword.value='';result.value='';savedPath.value='';mode.value=next}
async function exportBackup(){await props.run(async()=>{
  result.value='';savedPath.value=''
  try{
    const exported=await props.api('ExportWorkspaceBackup',[...selected.value],password.value,includePasswords.value)
    if(!exported.path){emit('notice','已取消选择保存位置');return}
    savedPath.value=exported.path
    result.value=`已备份 ${exported.count} 个网盘连接和 ${exported.presetCount||0} 个挂载方案，${exported.includesPasswords?'包含网盘密码':'不含网盘密码'}。${exported.presetWarning||''}`
    emit('notice','网盘连接备份已保存')
  }finally{password.value='';repeat.value='';passwordConsent.value=false}
})}
async function openBackup(){await props.run(async()=>{
  discard();result.value=''
  try{
    const data=await props.api('PreviewConnectionImport',importPassword.value) as ImportPreview
    if(!data.token){emit('notice','已取消选择备份文件');return}
    preview.value=data;indices.value=eligibleIndices(data);confirmed.value=false
    await reloadPresetPreview()
    emit('notice','备份已解锁，请核对要导入的网盘')
  }finally{importPassword.value=''}
})}
async function applyImport(){await props.run(async()=>{
  if(!preview.value||connectionApplied.value||!confirmed.value||!importRows.value.length)return
  const count=await props.api('ApplyConnectionImport',preview.value.token,importRows.value.map(row=>row.index))
  const hadPasswords=preview.value.includesPasswords
  if(preview.value.presetCount){connectionApplied.value=true}else{preview.value=null}
  indices.value=[];confirmed.value=false
  result.value=`已导入 ${count} 个网盘。${hadPasswords?'请先测试连接，再按需挂载。':'请在“编辑网盘”中补填应用密码，再测试连接。'}自动挂载已关闭。`
  emit('notice',`已导入 ${count} 个网盘连接`)
  await reloadPresetPreview()
  await props.refresh()
})}
async function reloadPresetPreview(){
  if(!preview.value?.presetCount)return
  presetError.value=''
  presetRows.value=[]
  presetSelection.value=[]
  try{
    presetRows.value=await props.api('PreviewWorkspacePresetImport',preview.value.token)
    presetSelection.value=presetRows.value.filter(row=>row.status==='ready').map(row=>row.index)
  }catch(e){presetError.value=errorMessage(e)}
}
async function restorePresets(){await props.run(async()=>{
  if(!preview.value?.presetCount||!presetSelection.value.length)return
  const count=await props.api('RestoreWorkspacePresets',preview.value.token,[...presetSelection.value])
  const summary=`已恢复 ${count} 个挂载方案。`
  result.value=result.value?result.value+' '+summary:summary
  emit('notice',summary)
  await reloadPresetPreview()
  await props.refresh()
})}
onUnmounted(discard)
</script>

<template>
  <section class="setting-section transfer-intro"><h2>网盘连接备份与迁移</h2><p class="field-help">备份涵盖分组、常用目录、备注和成员选齐的挂载方案。</p><div class="transfer-mode" role="group" aria-label="备份操作"><button :class="{primary:mode==='export'}" :disabled="blocked" @click="switchMode('export')">导出备份</button><button :class="{primary:mode==='import'}" :disabled="blocked" @click="switchMode('import')">从备份导入</button></div></section>
  <form v-if="mode==='export'" class="setting-section transfer-form" @submit.prevent="exportBackup">
    <div class="setting-heading"><h2>选择要备份的网盘</h2><button type="button" :disabled="blocked||!profiles.length" @click="selected=selected.length===profiles.length?[]:profiles.map(p=>p.id)">{{selected.length===profiles.length?'取消全选':'全选'}}</button></div>
    <p v-if="!profiles.length" class="page-hint">还没有网盘连接，请先添加网盘；已有备份可通过“从备份导入”恢复。</p>
    <div v-else class="transfer-connections" role="group" aria-label="备份网盘选择"><label v-for="p in profiles" :key="p.id" class="check-row"><input v-model="selected" type="checkbox" :value="p.id" :disabled="blocked"><span>{{p.name}}<small>{{p.drive}}</small></span></label></div>
    <label class="check-row"><input v-model="includePasswords" type="checkbox" :disabled="blocked"><span>在加密备份中包含网盘密码<small>默认不包含；不包含时，导入后需重新填写网盘应用密码。</small></span></label>
    <label v-if="includePasswords" class="check-row transfer-caution"><input v-model="passwordConsent" type="checkbox" :disabled="blocked"><span>我会妥善保管备份文件和备份密码<small>知道备份密码的人可以读取备份中的网盘密码。</small></span></label>
    <div class="transfer-passwords"><label>设置备份密码<input v-model="password" type="password" autocomplete="new-password" maxlength="256" :disabled="blocked" placeholder="12至128个字符"></label><label>再次输入备份密码<input v-model="repeat" type="password" autocomplete="new-password" maxlength="256" :disabled="blocked" placeholder="再次输入相同密码"></label></div>
    <p class="field-help">备份密码无法找回，请与备份文件分开保存。</p>
    <div class="setting-actions"><button class="primary" :disabled="blocked||!canExport">选择位置并导出…</button><span class="page-hint">已选 {{selected.length}} 个，单份最多 200 个</span></div>
  </form>
  <template v-else>
    <form v-if="!preview" class="setting-section transfer-form" @submit.prevent="openBackup"><h2>解锁备份文件</h2><label>备份密码<input v-model="importPassword" type="password" autocomplete="off" maxlength="256" :disabled="blocked" placeholder="输入导出时设置的备份密码"></label><p class="page-hint">选择 .wdmconnections 备份预览。</p><button class="primary" :disabled="blocked||!backupPasswordValid(importPassword)">选择文件并预览…</button></form>
    <section v-else class="setting-section"><div class="setting-heading"><h2>核对导入内容</h2><button :disabled="blocked" @click="discard">重新选择</button></div><p class="page-hint">备份时间：{{transferTime(preview.createdAt)}}<br>预览有效至：{{transferTime(preview.expiresAt)}}（本次有效，关闭页面后需重新解锁）</p><p class="setting-warning">{{preview.includesPasswords?'此备份包含网盘密码。':'此备份不含网盘密码，导入后请补填应用密码。'}}已有连接保留，导入后不会自动挂载。</p>
      <template v-if="!connectionApplied">
      <div class="transfer-preview" role="group" aria-label="导入网盘选择"><label v-for="row in preview.rows" :key="row.index" class="transfer-preview-row"><input v-model="indices" type="checkbox" :value="row.index" :disabled="blocked||row.status!=='ready'"><span><strong>{{row.name}}</strong><small>{{providerLabel(row.provider)}}<template v-if="row.url"> · {{row.url}}</template></small><small>用户名：{{row.user||'（空）'}} · {{row.drive?(row.drive===row.originalDrive?row.drive:`${row.originalDrive} → ${row.drive}`):'跳过'}}</small><small>分组：{{row.group||'未分组'}} · 常用目录 {{row.favoriteCount||0}} 个<template v-if="row.hasNote"> · 含备注</template></small><small :class="{'transfer-skipped':row.status!=='ready'}">{{row.message}}</small></span></label></div>
      <label class="check-row"><input v-model="confirmed" type="checkbox" :disabled="blocked||!importRows.length"><span>我已核对，将所选 {{importRows.length}} 个网盘添加到当前客户端</span></label><p class="field-help">重复连接会跳过。请按预览核对名称和盘符。</p><div class="setting-actions"><button class="primary" :disabled="blocked||!confirmed||!importRows.length" @click="applyImport">确认导入 {{importRows.length}} 个网盘</button><button :disabled="blocked" @click="discard">取消导入</button></div>
      </template>
    </section>
    <section v-if="preview?.presetCount" class="setting-section">
      <div class="setting-heading"><h2>恢复挂载方案</h2><button :disabled="blocked" @click="run(reloadPresetPreview)">刷新方案预览</button></div>
      <p class="field-help">先导入所需网盘。成员齐全的方案可以单独恢复，已有同名方案保留。</p>
      <p v-if="presetError" class="setting-warning" role="alert">{{ presetError }}</p>
      <div v-if="presetRows.length" class="transfer-preview" role="group" aria-label="挂载方案恢复选择">
        <label v-for="row in presetRows" :key="row.index" class="transfer-preview-row">
          <input v-model="presetSelection" type="checkbox" :value="row.index" :disabled="blocked||row.status!=='ready'">
          <span><strong>{{row.name}}</strong><small>{{row.memberCount}} 个网盘 · {{row.message}}</small></span>
        </label>
      </div>
      <button class="primary" :disabled="blocked||!presetSelection.length" @click="restorePresets">恢复 {{presetSelection.length}} 个方案</button>
    </section>
  </template>
  <section v-if="result" class="setting-section transfer-result" role="status"><h2>操作完成</h2><p>{{result}}</p><p v-if="savedPath" class="selectable-path">{{savedPath}}</p></section>
</template>
