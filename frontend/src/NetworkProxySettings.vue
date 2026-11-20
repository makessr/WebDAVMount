<script setup lang="ts">
import {onMounted,ref} from 'vue'

type ProxySettings={mode:string,protocol:string,host:string,port:number|string,username:string,password:string,bypass:string}
type ProxyInfo={settings:ProxySettings,hasPassword:boolean,pending:boolean,warning?:string}
const props=defineProps<{api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>)=>Promise<void>,blocked:boolean}>()
const emit=defineEmits<{notice:[message:string]}>()
const form=ref<ProxySettings>({mode:'environment',protocol:'http',host:'',port:'',username:'',password:'',bypass:''})
const info=ref<ProxyInfo>()
const warning=ref('')

function apply(value:ProxyInfo){
  info.value=value
  form.value={...value.settings,port:value.settings.port||'',protocol:value.settings.protocol||'http',password:''}
  warning.value=value.warning||''
}
onMounted(async()=>{
  try{apply(await props.api('ProxyInfo'))}
  catch{warning.value='无法读取网盘代理设置，请重新打开此页。'}
})
async function save(){
  await props.run(async()=>{
    const result:ProxyInfo=await props.api('SaveProxySettings',{...form.value,port:Number(form.value.port)})
    apply(result)
    emit('notice',result.pending?'代理设置已保存。退出并重新打开客户端后生效。':'代理设置已保存')
  })
}
async function probe(){
  await props.run(async()=>{emit('notice',await props.api('ProbeMEGANetwork'))})
}
</script>

<template>
  <section class="setting-section">
    <h2>网盘代理</h2>
    <form class="proxy-form" @submit.prevent="save">
      <fieldset :disabled="blocked||!info">
        <legend>连接方式</legend>
        <label><input v-model="form.mode" type="radio" value="environment"> 沿用环境变量（默认）</label>
        <label><input v-model="form.mode" type="radio" value="direct"> 直连</label>
        <label><input v-model="form.mode" type="radio" value="system"> 使用 Windows 系统代理</label>
        <label><input v-model="form.mode" type="radio" value="manual"> 使用自定义代理</label>
      </fieldset>
      <p v-if="form.mode==='system'" class="field-help">使用 Windows 的手动代理地址。使用自动配置脚本时，请改用自定义代理。</p>
      <fieldset v-if="form.mode==='manual'" :disabled="blocked||!info">
        <legend>代理服务器</legend>
        <div class="proxy-address">
          <label>协议<select v-model="form.protocol"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option></select></label>
          <label>主机名或 IP<input v-model.trim="form.host" placeholder="127.0.0.1" required maxlength="253"></label>
          <label>端口<input v-model.number="form.port" type="number" min="1" max="65535" step="1" placeholder="7897" required></label>
        </div>
        <div class="proxy-credentials">
          <label>用户名（选填）<input v-model="form.username" maxlength="255" autocomplete="off"></label>
          <label>密码（选填）<input v-model="form.password" type="password" maxlength="255" autocomplete="new-password" :placeholder="info?.hasPassword?'留空保留原密码':''"></label>
        </div>
        <label>不经代理的地址（选填）<input v-model.trim="form.bypass" maxlength="4096" placeholder="nas.local, 192.168.1.0/24"></label>
        <p class="field-help">多个地址用逗号分隔。本机回环地址直接连接。</p>
      </fieldset>
      <p v-if="warning" class="setting-warning" role="status">{{warning}}</p>
      <p v-if="info?.pending" class="setting-warning" role="status">退出并卸载后重新打开客户端，以应用代理设置。</p>
      <div class="setting-actions">
        <button class="primary" :disabled="blocked||!info">保存代理设置</button>
        <button type="button" :disabled="blocked||!info||info.pending||!!warning" @click="probe">测试 MEGA API</button>
      </div>
    </form>
  </section>
</template>

<style scoped>
.proxy-form{max-width:36rem}
fieldset{padding:0;margin:12px 0 16px;border:0;min-width:0;display:grid;gap:9px}
legend{margin-bottom:7px;font-weight:600}
.proxy-address,.proxy-credentials{display:grid;grid-template-columns:6rem minmax(8rem,1fr) 6rem;gap:10px}
.proxy-credentials{grid-template-columns:repeat(2,minmax(0,1fr))}
.proxy-address label,.proxy-credentials label,fieldset>label:last-of-type:has(input:not([type=radio])){display:grid;gap:5px}
.proxy-address input,.proxy-address select,.proxy-credentials input,fieldset>label:last-of-type input:not([type=radio]){min-width:0;width:100%}
@media(max-width:580px){.proxy-address,.proxy-credentials{grid-template-columns:1fr}}
</style>
