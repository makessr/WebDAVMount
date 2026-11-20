<script setup lang="ts">
import {computed, onMounted, ref} from 'vue'
import UiIcon from './UiIcon.vue'

type RecoveryRow={id:string,name:string,drive:string,state:'ready'|'blocked'|'skipped',message:string,previous?:boolean}
type RecoveryPreview={online:boolean,items:RecoveryRow[],revision:string,session?:{previous:number,warning?:string}}
type RecoveryResult={id:string,name:string,drive:string,label:string,tone:string,detail:string}
const props=defineProps<{
  busy:boolean,error:string,results:RecoveryResult[],
  api:(name:string,...args:any[])=>Promise<any>,
  run:(fn:()=>Promise<void>,cancellable?:boolean)=>Promise<void>,
  refresh:()=>Promise<void>,cancel:()=>Promise<void>,
}>()
const emit=defineEmits<{close:[],notice:[message:string]}>()
const plan=ref<RecoveryPreview|null>(null),selected=ref<string[]>([])
const applying=ref(false)
const refreshWarning=ref('')
const confirmingDismiss=ref(false)
const ready=computed(()=>plan.value?.items.filter(item=>item.state==='ready')??[])
const picked=computed(()=>ready.value.map(item=>item.id).filter(id=>selected.value.includes(id)))
const states={ready:'可恢复',blocked:'盘符冲突',skipped:'保持挂载'}

async function readPlan(initial=false) {
  plan.value=null
  const preview=await props.api('PreviewRecovery') as RecoveryPreview
  plan.value=preview
  selected.value=preview.items.filter(item=>item.state==='ready'&&((initial&&!item.previous)||selected.value.includes(item.id))).map(item=>item.id)
}
async function check(initial=false) {
  refreshWarning.value=''
  await props.run(()=>readPlan(initial))
}
onMounted(()=>{void check(true)})

async function dismissPrevious() {
  await props.run(async()=>{
    await props.api('DismissPreviousMounts')
    confirmingDismiss.value=false
    await props.refresh()
    await readPlan()
    emit('notice','已清除上次挂载记录')
  })
}

async function apply(restartOnly:boolean) {
  if(!plan.value||props.busy||(!restartOnly&&!picked.value.length))return
  const revision=plan.value.revision
  const ids=restartOnly?[]:picked.value
  await props.run(async()=>{
    applying.value=true
    refreshWarning.value=''
    try {
      await props.api('ApplyRecovery',ids,restartOnly,revision)
      emit('notice',restartOnly?'挂载服务已启动':'恢复操作已结束，请核对逐项结果')
    } finally {
      applying.value=false
      try{await props.refresh()}catch{refreshWarning.value='窗口状态刷新失败，请重新检查。'}
      try{await readPlan()}catch{refreshWarning.value='恢复状态刷新失败，请重新检查。'}
    }
  },true)
}
</script>

<template>
  <section class="dialog batch-dialog recovery-center" role="dialog" aria-modal="true" aria-labelledby="recovery-title">
    <div class="dialog-header">
      <h2 id="recovery-title">恢复挂载</h2>
      <button class="icon-button" :disabled="busy" aria-label="关闭恢复中心" @click="emit('close')"><UiIcon name="close"/></button>
    </div>
    <div class="dialog-body">
      <p v-if="error" class="dialog-error" role="alert">{{error}}</p>
      <p v-if="refreshWarning" class="dialog-error" role="alert">{{refreshWarning}}</p>
      <p v-if="busy" role="status">{{applying?'正在恢复挂载…':'正在检查挂载状态…'}}</p>
      <template v-if="plan">
        <p class="page-hint">{{plan.online?'挂载服务运行中':'挂载服务已停止'}}</p>
        <p v-if="plan.session?.warning" class="dialog-error" role="alert">{{plan.session.warning}}</p>
        <div v-if="plan.session?.previous||plan.session?.warning" class="recovery-controls">
          <button v-if="!confirmingDismiss" :disabled="busy" @click="confirmingDismiss=true">清除上次记录…</button>
          <template v-else>
            <span>清除上次留下的挂载记录？</span>
            <button :disabled="busy" class="danger-text" @click="dismissPrevious">确认清除</button>
            <button :disabled="busy" @click="confirmingDismiss=false">取消</button>
          </template>
        </div>
        <button v-if="ready.length>1" :disabled="busy" @click="selected=picked.length===ready.length?[]:ready.map(item=>item.id)">
          {{picked.length===ready.length?'取消全选':'全选可恢复网盘'}}
        </button>
        <ul v-if="plan.items.length" class="batch-result-list recovery-plan" aria-label="网盘恢复状态">
          <li v-for="item in plan.items" :key="item.id">
            <div>
              <label :title="item.name"><input v-model="selected" type="checkbox" :value="item.id" :disabled="busy||item.state!=='ready'"><strong>{{item.name}}</strong></label>
              <span>{{item.drive}}</span>
              <span v-if="item.previous" class="recovery-origin">上次会话</span>
              <b :class="{'danger-text':item.state==='blocked'}">{{states[item.state]}}</b>
            </div>
            <p>{{item.message}}</p>
          </li>
        </ul>
        <p v-else class="field-help">本次会话没有可恢复的网盘。</p>
      </template>
      <section v-if="results.length" class="recovery-result">
        <h3>上次恢复结果</h3>
        <ul class="batch-result-list">
          <li v-for="item in results" :key="item.id"><div><strong>{{item.name}}</strong><span>{{item.drive}}</span><b :class="{'danger-text':item.tone==='warning'}">{{item.label}}</b></div><p>{{item.detail}}</p></li>
        </ul>
      </section>
    </div>
    <div class="dialog-footer">
      <button v-if="applying" @click="cancel">停止后续恢复</button>
      <template v-else>
        <button :disabled="busy" @click="check()">重新检查</button>
        <button v-if="plan&&!plan.online" :disabled="busy" @click="apply(true)">仅启动服务</button>
        <button :disabled="busy" @click="emit('close')">关闭</button>
        <button v-if="ready.length" class="primary" :disabled="busy||!picked.length" @click="apply(false)">恢复所选网盘（{{picked.length}}）</button>
      </template>
    </div>
  </section>
</template>

<style scoped>
.recovery-plan label {display:flex;min-width:0;align-items:center;gap:7px}
.recovery-plan input {flex:none}
.recovery-plan {margin-top:8px}
.recovery-controls {display:flex;align-items:center;gap:7px;flex-wrap:wrap;margin:5px 0}
.recovery-plan .recovery-origin {white-space:nowrap;color:var(--primary)}
.recovery-result {margin-top:14px}
.recovery-result h3 {font-size:12px;margin-bottom:4px}
.dialog-footer {flex-wrap:wrap}
</style>
