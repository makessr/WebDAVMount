<script setup lang="ts">
import {ref,watch} from 'vue'
import UiIcon from './UiIcon.vue'
import {favoriteName,favoriteDirectionAllowed,type DirectoryFavorite} from './directory-favorites'
const props=defineProps<{profileId:string,profileName:string,currentPath:string,loaded:boolean,items:DirectoryFavorite[],busy:boolean,error:string,api:(name:string,...args:any[])=>Promise<any>,run:(fn:()=>Promise<void>)=>Promise<void>}>()
const emit=defineEmits<{close:[],changed:[items:DirectoryFavorite[]],locate:[path:string],notice:[text:string]}>()
const name=ref(favoriteName(props.currentPath)),editing=ref(''),editName=ref(''),removing=ref('')
watch(()=>props.currentPath,path=>name.value=favoriteName(path))
async function add(){await props.run(async()=>{const items=await props.api('SaveDirectoryFavorite',props.profileId,{id:'',name:name.value,path:props.currentPath});emit('changed',items);emit('notice','当前目录已收藏')})}
async function rename(item:DirectoryFavorite){await props.run(async()=>{const items=await props.api('SaveDirectoryFavorite',props.profileId,{...item,name:editName.value});emit('changed',items);editing.value='';emit('notice','收藏已重命名')})}
async function move(item:DirectoryFavorite,direction:-1|1){await props.run(async()=>{emit('changed',await props.api('MoveDirectoryFavorite',props.profileId,item.id,direction));emit('notice','常用目录顺序已保存')})}
async function remove(item:DirectoryFavorite){await props.run(async()=>{emit('changed',await props.api('RemoveDirectoryFavorite',props.profileId,item.id));removing.value='';emit('notice','本机收藏已移除，网盘文件保留')})}
</script>
<template>
 <section class="dialog favorite-dialog" role="dialog" aria-modal="true" aria-labelledby="favorite-title">
  <div class="dialog-header"><h2 id="favorite-title">常用目录 · {{profileName}}</h2><button class="icon-button" aria-label="关闭常用目录" :disabled="busy" @click="emit('close')"><UiIcon name="close"/></button></div>
  <div class="dialog-body">
   
   <form v-if="loaded" class="favorite-add" @submit.prevent="add"><label>收藏名称<input v-model="name" maxlength="80" required :disabled="busy" placeholder="例如：工作资料"></label><button class="primary" :disabled="busy||!name.trim()||items.length>=100">收藏当前目录</button><p class="field-help">当前位置：{{currentPath||'根目录'}}</p></form>
   <p v-else class="field-help">先成功读取一个目录，即可收藏当前位置。</p>
   <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
   <p v-if="items.length>=100" class="field-help" role="status">常用目录已满，可先移除一项。</p>
   <p v-if="!items.length" class="search-empty">还没有常用目录收藏。</p>
   <ul v-else class="favorite-list"><li v-for="(item,index) in items" :key="item.id">
    <div v-if="editing===item.id" class="favorite-edit"><label>新名称<input v-model="editName" maxlength="80" required :disabled="busy" @keydown.enter.prevent="rename(item)"></label><button class="primary" :disabled="busy||!editName.trim()" @click="rename(item)">保存名称</button><button :disabled="busy" @click="editing=''">取消</button></div>
    <template v-else><strong>{{item.name}}</strong><p class="favorite-location">{{item.path||'根目录'}}</p>
     <div v-if="removing===item.id" class="favorite-remove"><span>只移除本机收藏，目录和文件保留。</span><button class="danger-text" :disabled="busy" @click="remove(item)">确认移除</button><button :disabled="busy" @click="removing=''">返回</button></div>
     <div v-else class="favorite-actions"><button :disabled="busy" @click="emit('locate',item.path)">打开目录</button><button :disabled="busy" @click="editing=item.id;editName=item.name;removing=''">重命名</button><button :disabled="busy||!favoriteDirectionAllowed(index,items.length,-1)" :aria-label="'上移收藏 '+item.name" @click="move(item,-1)">上移</button><button :disabled="busy||!favoriteDirectionAllowed(index,items.length,1)" :aria-label="'下移收藏 '+item.name" @click="move(item,1)">下移</button><button class="danger-text" :disabled="busy" @click="removing=item.id">移除…</button></div>
    </template>
   </li></ul>
   
  </div>
  <div class="dialog-footer"><button :disabled="busy" @click="emit('close')">完成</button></div>
 </section>
</template>
