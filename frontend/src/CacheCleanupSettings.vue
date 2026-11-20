<script setup lang="ts">
import {ref,onMounted} from 'vue'
const props=defineProps<{api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>)=>Promise<void>,blocked:boolean,mounted:number}>()
const enabled=ref(false),days=ref(30),consent=ref(false),loaded=ref(false),warning=ref(''),items=ref<{id:string,state:string,message:string}[]>([])
function apply(info:any){enabled.value=!!info.policy.enabled;days.value=info.policy.retainDays;items.value=info.items||[];warning.value=info.warning||'';loaded.value=true}
onMounted(async()=>{try{apply(await props.api('CacheCleanupInfo'))}catch{warning.value='自动清理设置读取失败，请重新打开此页'}})
async function save(){await props.run(async()=>{await props.api('SaveCacheCleanupPolicy',{enabled:enabled.value,retainDays:Number(days.value)});apply(await props.api('CacheCleanupInfo'));consent.value=false})}
async function check(){await props.run(async()=>{apply(await props.api('RunRetainedCacheCleanup'))})}
</script>
<template>
  <section class="setting-section"><h2>迁移后旧缓存自动清理</h2>
    
    <label class="check-row"><input v-model="enabled" type="checkbox" :disabled="blocked||!loaded"><span>自动清理到期的迁移缓存</span></label>
    <label>至少保留天数<input v-model="days" type="number" min="7" max="365" step="1" :disabled="blocked||!loaded"></label>
    <p v-if="enabled" class="field-help">全部网盘卸载后，只清理已校验且到期的迁移缓存。</p>
    <label v-if="enabled" class="check-row"><input v-model="consent" type="checkbox" :disabled="blocked"><span>我了解清理不可撤销，需要时会重新下载缓存</span></label>
    <div class="setting-actions"><button class="primary" :disabled="blocked||!loaded||(enabled&&!consent)||!Number.isInteger(Number(days))||Number(days)<7||Number(days)>365" @click="save">保存清理设置</button><button :disabled="blocked||!loaded||mounted>0" @click="check">按已保存设置检查并清理</button></div>
    <p v-if="warning" class="setting-warning">{{warning}}</p>
    <ul v-if="items.length"><li v-for="item in items" :key="item.id">{{item.state==='cleaned'?'已清理':'已跳过'}}：{{item.message}}</li></ul>
  </section>
</template>
