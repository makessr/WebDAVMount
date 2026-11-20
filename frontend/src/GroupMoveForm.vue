<script setup lang="ts">
import {onMounted,ref,useId,watch} from 'vue'

const props=defineProps<{groups:readonly string[],count:number,busy:boolean,cancellable?:boolean}>()
const emit=defineEmits<{move:[group:string],cancel:[]}>()
const target=ref('')
const groupInput=ref<HTMLInputElement|null>(null)
const optionsId=useId()
onMounted(()=>{if(props.cancellable)groupInput.value?.focus()})
watch(()=>props.count,count=>{if(!count)target.value=''})
function submit(){
 if(!props.busy&&props.count>0&&props.count<=200)emit('move',target.value.trim())
}
</script>

<template>
 <form class="group-move-form" @submit.prevent="submit">
  <label>移入分组（已选 {{count}} 个）
   <input ref="groupInput" v-model="target" :list="optionsId" maxlength="32" placeholder="输入新分组，或选择已有分组" :disabled="busy">
   <datalist :id="optionsId"><option v-for="name in groups" :key="name" :value="name" /></datalist>
  </label>
  <button class="primary" :disabled="busy||!count||count>200">{{target.trim()?'移动所选':'移入未分组'}}</button>
  <button v-if="cancellable" type="button" :disabled="busy" @click="emit('cancel')">取消</button>
 </form>
</template>

<style scoped>
.group-move-form{display:flex;gap:8px;flex-wrap:wrap;align-items:flex-end}
.group-move-form label{flex:1;min-width:160px;display:grid;gap:8px}
</style>
