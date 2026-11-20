<script setup lang="ts">
import {ref,computed,watch,nextTick,onMounted,onUnmounted} from 'vue'
import * as WailsRuntime from '@wailsio/runtime'
import UiIcon from './UiIcon.vue'
import {storageProviders,providerLabel,usesBrowserGrant,type StorageProvider} from './storage-providers'
import {filterDiagnosticEvents,diagnosticSessionLabel,diagnosticErrorsOnly,diagnosticOperationNames as operationNames,diagnosticResultNames as resultNames,type DiagnosticSessions} from './diagnostic-view'
import ConnectionTransfer from './ConnectionTransfer.vue'
import StartupTasks from './StartupTasks.vue'
import RecoveryCenter from './RecoveryCenter.vue'
import type {StartupTasks as StartupTaskReport} from './startup-tasks'
import CacheCleanupSettings from './CacheCleanupSettings.vue'
import NetworkProxySettings from './NetworkProxySettings.vue'
import ConnectionGroups from './ConnectionGroups.vue'
import type {MountPreset} from './connection-management'
import {mountPresetKey} from './connection-groups'
import logoUrl from './assets/logo.png'
import {updateProgress} from './update-progress'
import {initialUpdate,updateFeedback,loadReleaseSettings,runReleaseAction,withReleaseStateRecovery,type UpdateState} from './release-settings'
import {workspaceShortcut} from './workspace-shortcuts'
import {recoveryFeedback} from './connection-feedback'
import {directoryPage} from './directory-page'
import {isProfileMounted} from './mount-identity'
import {batchSummary,type BatchItem,type BatchReport} from './batch-result'
import {createTaskRunner,errorMessage} from './operation-feedback'
import {connectionDriveOptions,suggestDrive,saveConnectionFlow,fileSize} from './workspace-flow'

type ConnectionProxy={mode:string,protocol?:string,host?:string,port?:number|string,username?:string,password?:string}
type ObjectOptions={mode:string,region?:string,sessionToken?:string,tenant?:string,clientId?:string}
type Profile={provider?:StorageProvider,hostKey?:string,id:string,name:string,url:string,user:string,password:string,drive:string,presetKey?:string,clearPassword?:boolean,autoMount?:boolean,group?:string,note?:string,proxy?:ConnectionProxy,storage?:ObjectOptions}
const api=(name:string,...args:any[])=>WailsRuntime.Call.ByName('webdavmount/desktop/windows.DesktopService.'+name,...args)
const groupOpen=ref(false)
const connectionGroupsControl=ref<InstanceType<typeof ConnectionGroups>|null>(null)
const mountPresets=ref<MountPreset[]>([]),presetReadError=ref('')
async function loadMountPresets(){
  try{
    mountPresets.value=await api('MountPresets') ?? []
    presetReadError.value=''
  }catch(e){
    mountPresets.value=[]
    presetReadError.value=errorMessage(e)
  }
}
type EditCopyStatus={profileId:string,remote:string,local:string}
const editCopy=ref<EditCopyStatus|null>(null),editCopyWarning=ref(''),editCopyDialog=ref(false),confirmForgetEdit=ref(false)
const editCopyOperation=ref('')
type WritableCacheItem={path:string,reason?:string}
type WritableCachePreview={items:WritableCacheItem[],ready:number,skipped:number}
const writableRemountDialog=ref(false)
const writablePreviewOpen=ref(false),writablePreview=ref<WritableCachePreview|null>(null),writablePreviewPage=ref(1)
const writablePreviewRows=computed(()=>directoryPage(writablePreview.value?.items??[],writablePreviewPage.value))
type DirectoryReview={state:string,source:string,target:string,recordID:string}
const directoryReviewOpen=ref(false),directoryReview=ref<DirectoryReview|null>(null)
const pendingDirectoryReviewIDs=ref<string[]>([]),reviewAfterMount=ref(false)
type S3Review={state:string,path:string,recordID:string,paths:string[]|null,total:number}
const s3ReviewOpen=ref(false),s3ReviewResult=ref<S3Review|null>(null),s3ReviewQueue=ref<S3Review|null>(null)
const s3DeleteMode=ref(false)
type WritableStatus={known:boolean,s3ReviewCount?:number,s3DeleteReview?:boolean,directoryReview?:boolean,queued:number,uploading:number,errors:number,conflicts?:number,renameBlocked?:number,delete?:{confirmed:number,blocked:number,conflict:number,unconfirmed:number},outOfSpace:boolean}
const s3WritableProfiles=ref<string[]>([])
const writableProfileID=ref(''),writableJournalError=ref(false),writableStatus=ref<WritableStatus>(),writableDialog=ref(false),writableArchiveDialog=ref(false)
const profiles=ref<Profile[]>([]),selected=ref(''),mounts=ref<any[]>([])
const presetKeys=computed(()=>new Set(profiles.value.map(mountPresetKey)))
const occupiedDrives=ref<string[]>([])
const screen=ref<'connections'|'diagnostics'|'settings'>('connections'),settingsTab=ref<'general'|'proxy'|'cache'|'transfer'|'about'>('general')
const error=ref(''),notice=ref(''),busy=ref(false),canCancel=ref(false),autoRunning=ref(false),engineOnline=ref(true)
const mountStateUnknown=ref(false)
const mountCommandsAvailable=computed(()=>engineOnline.value&&!mountStateUnknown.value)
const clientVersion=ref('')
const cacheMigrationFallback=ref(false)
const startupTasks=ref<StartupTaskReport>({active:false,items:[]})
const sessionRecovery=ref<{previous:number,warning?:string}>({previous:0})
const autoRecoveryState=ref<any>({enabled:false,active:false,state:'disabled'}),autoRecoveryEnabled=ref(false)
const maintenance=ref<any>({settings:{remoteChecks:false,engineWatchdog:false},connections:{}}),maintenanceSettings=ref({remoteChecks:false,engineWatchdog:false})
const activeProbe=computed(()=>maintenance.value.connections?.[selected.value])
const autoRecovering=computed(()=>!!autoRecoveryState.value.active)
const recovery=ref<Record<string,string>>({}),startup=ref<Record<string,{state:string,message:string}>>({})
const connectionMenu=ref<HTMLDetailsElement|null>(null),batchMenu=ref<HTMLDetailsElement|null>(null)
const active=computed(()=>profiles.value.find(p=>p.id===selected.value)),mounted=computed(()=>isProfileMounted(mounts.value,active.value))
const writableMounted=computed(()=>mounted.value&&mounts.value.some(m=>m.ProfileID===selected.value&&m.Writable===true))
const writableRecovery=computed(()=>!mountStateUnknown.value&&!!active.value&&!mounted.value&&writableProfileID.value===active.value.id)
const s3WritableCandidate=computed(()=>active.value?.provider==='minio'&&(writableRecovery.value||s3WritableProfiles.value.includes(active.value.id)))
const writableAvailable=computed(()=>!!active.value&&(!active.value.provider||active.value.provider==='webdav'||s3WritableCandidate.value)&&!writableJournalError.value&&(!writableProfileID.value||writableRecovery.value))
const activeDirectoryReview=computed(()=>pendingDirectoryReviewIDs.value.includes(selected.value)||(writableMounted.value&&!!writableStatus.value?.directoryReview))
const firstDirectoryReview=computed(()=>profiles.value.find(p=>pendingDirectoryReviewIDs.value.includes(p.id)))
const writableNeedsRecovery=computed(()=>!mountStateUnknown.value&&!!writableProfileID.value&&!mounts.value.some(m=>m.ProfileID===writableProfileID.value&&m.Writable===true))
const deleteStatus=computed(()=>writableStatus.value?.delete)
const deleteNeedsReview=computed(()=>!!(deleteStatus.value?.conflict||deleteStatus.value?.unconfirmed))
const deleteMessage=computed(()=>{
  const result=deleteStatus.value
  if(!result)return ''
  return [
    result.confirmed&&result.confirmed+' 项已从网盘删除',
    result.blocked&&result.blocked+' 项删除被阻止',
    result.conflict&&result.conflict+' 项删除遇到版本冲突',
    result.unconfirmed&&result.unconfirmed+' 项删除结果待确认',
  ].filter(Boolean).join('，')
})
const writableMessage=computed(()=>{
  const status=writableStatus.value
  if(!status?.known)return '暂时无法核对写回状态。'
  if(status.outOfSpace)return '缓存空间不足，请释放本地磁盘空间。'
  if(status.conflicts)return '有 '+status.conflicts+' 个文件与远端版本冲突，可导出本机写回副本。'
  if(status.errors)return '有写回错误，请检查网络和远端写入权限。'
  return status.queued||status.uploading?'待上传 '+status.queued+' 个，正在上传 '+status.uploading+' 个。':'当前上传队列为空。卸载前请关闭正在编辑的文件。'
})
const savingMountedFile=ref(false)
const mountedSaveProgress=ref<{active:boolean,bytes:number,total:number,index?:number,count?:number,name?:string,stage?:string}|null>(null)
const mountedSaveMessage=computed(()=>{
  const progress=mountedSaveProgress.value
  if(!progress)return '准备导出写回副本…'
  if(progress.stage==='verifyingWriteback')return '正在复核已保存的写回 ZIP…'
  if(progress.stage==='scanningWriteback')return '正在检查写回缓存…'
  const action='正在导出'
  return (progress.count?action+' '+progress.index+' / '+progress.count:action+'文件')+' · '+fileSize(progress.bytes)+' / '+fileSize(progress.total)
})
const mountedCount=computed(()=>profiles.value.filter(p=>isProfileMounted(mounts.value,p)).length)
const recoveryItems=computed(()=>profiles.value.filter(p=>Object.hasOwn(recovery.value,p.id)).map(p=>({...p,...recoveryFeedback(recovery.value[p.id])})))
const blocked=computed(()=>busy.value||autoRunning.value||autoRecovering.value)
const title=computed(()=>screen.value==='settings'?'设置':screen.value==='diagnostics'?'诊断日志':active.value?.name||'我的网盘')
const {task,cancel:cancelTask}=createTaskRunner({busy,canCancel,error,notice})
function selectProfile(id:string){
  selected.value=id;screen.value='connections';error.value='';notice.value='';connectionMenu.value?.removeAttribute('open')
}
function choose(id:string){if(!blocked.value&&(selected.value!==id||screen.value!=='connections'))selectProfile(id)}
async function refresh(){
  const s:any=await api('Snapshot');profiles.value=s.profiles??[];mounts.value=s.mounts??[];autoRunning.value=!!s.autoRunning;engineOnline.value=s.engineOnline!==false
  mountStateUnknown.value=s.mountStateUnknown===true
  writableProfileID.value=s.writableProfile??''
  writableJournalError.value=!!s.writableJournalError
  writableStatus.value=s.writableStatus
  pendingDirectoryReviewIDs.value=s.pendingDirectoryReviews??[]
  s3WritableProfiles.value=s.s3WritableProfiles??[]
  occupiedDrives.value=s.occupiedDrives??[]
  clientVersion.value=s.version??clientVersion.value
  cacheMigrationFallback.value=!!s.cacheMigrationFallback
  startupTasks.value=s.startupTasks??{active:false,items:[]}
  sessionRecovery.value=s.sessionRecovery??{previous:0}
  const previous=autoRecoveryState.value
  maintenance.value=s.maintenance??maintenance.value
  autoRecoveryState.value=s.autoRecovery??{enabled:false,active:false,state:'disabled'}
  if(autoRecoveryState.value.attempts>previous.attempts&&autoRecoveryState.value.state==='watching')notice.value=autoRecoveryState.value.message
  recovery.value=s.recovery??{};startup.value=s.startup??{}
  if(!profiles.value.some(p=>p.id===selected.value))selected.value=profiles.value[0]?.id??''
}
async function loadEditCopy(){
  try{
    editCopy.value=(await api('EditCopy'))??null
    editCopyWarning.value=''
  }catch(e){editCopy.value=null;editCopyWarning.value=errorMessage(e)}
}
function openEditCopyDialog(){
  error.value=''
  confirmForgetEdit.value=false
  editCopyDialog.value=true
}
async function submitEditCopy(){await task(async()=>{
  editCopyOperation.value='正在核对远端版本并提交修改…'
  try{
    await api('CommitEditCopy')
    await loadEditCopy()
    editCopyDialog.value=false
    notice.value='远端已保存，本机编辑副本仍可使用'
  }finally{editCopyOperation.value=''}
},true)}
async function forgetEditCopy(){await task(async()=>{
  await api('ForgetEditCopy')
  await loadEditCopy()
  editCopyDialog.value=false
  notice.value='已结束编辑记录，本机副本已保留'
})}
async function mountSelected(id:string,writable=false){
  await api(writable?'MountWritable':'Mount',id)
  notice.value='网盘已挂载'
  try{await refresh()}catch(e){error.value='网盘已挂载，状态未能刷新：'+errorMessage(e)}
}
function askWritable(review=false){
  if(blocked.value||mounted.value||!writableAvailable.value||(!engineOnline.value&&!writableRecovery.value))return
  connectionMenu.value?.removeAttribute('open')
  error.value=''
  reviewAfterMount.value=review
  writableDialog.value=true
}
async function confirmWritable(){await task(async()=>{
  const profile=active.value
  if(!profile)return
  await mountSelected(profile.id,true)
  writableDialog.value=false
  if(reviewAfterMount.value)await loadDirectoryReview(profile.id)
})}

const directoryReviewMessages:Record<string,string>={
  'source-absent':'源目录已不存在。',
  'target-matches':'目标目录内容与操作前记录一致，源目录已不存在。',
  'original-matches':'原目录内容与操作前记录一致。',
  unconfirmed:'暂时无法确认远端结果。',
  unreadable:'本机核对记录无法读取。',
  unavailable:'当前挂载不支持目录核对。',
  none:'当前没有待确认的目录操作。',
}

const s3ReviewMessages:Record<string,string>={
  absent:'远端文件已不存在。',
  present:'远端文件仍存在。',
  changed:'远端状态已变化，请重新核对。',
  committed:'远端文件和本地副本一致，可以完成核对。',
  'remote-changed':'远端已有其他版本，本地改动已保留。',
  mismatch:'远端内容或元数据与记录不一致，本地改动已保留。',
  busy:'文件仍在使用，关闭后可重新核对。',
  'cache-unavailable':'本地缓存目前无法完整读取。',
  unconfirmed:'暂时无法核对远端结果，本地改动已保留。',
  none:'该文件已没有待核对记录。',
}
async function openS3Review(){
  if(!active.value||!writableMounted.value||blocked.value)return
  s3ReviewOpen.value=true
  s3ReviewResult.value=null
  await task(async()=>{s3ReviewQueue.value=await api('ReviewWritableS3',active.value!.id,'')})
}
async function inspectS3Delete(recordID=''){
  const profile=active.value
  if(!profile||!writableMounted.value||blocked.value)return
  s3DeleteMode.value=true
  s3ReviewOpen.value=true
  s3ReviewResult.value=null
  let complete=false
  await task(async()=>{
    const result:S3Review=await api('ReviewWritableS3Delete',profile.id,recordID)
    if(result.state==='acknowledged'){complete=true;return}
    s3ReviewResult.value=result
  })
  if(complete){
    if(writableStatus.value)writableStatus.value.s3DeleteReview=false
    closeDialog()
    notice.value='S3 删除核对已完成'
  }
}
async function inspectS3Write(name:string){
  const profile=active.value
  if(!profile||blocked.value)return
  s3ReviewResult.value=null
  await task(async()=>{s3ReviewResult.value=await api('ReviewWritableS3',profile.id,name)})
}
async function acknowledgeS3Write(){
  const profile=active.value,review=s3ReviewResult.value
  if(!profile||!review?.recordID||blocked.value)return
  await task(async()=>{
    try{await api('AcknowledgeWritableS3',profile.id,review.path,review.recordID)}
    catch(e){s3ReviewResult.value={...review,recordID:''};throw e}
    s3ReviewResult.value=null
    s3ReviewQueue.value=await api('ReviewWritableS3',profile.id,'')
    await refresh()
    notice.value='S3 写回核对已完成'
  })
}

async function checkDirectoryReview(){
  const profile=active.value
  if(!profile||!writableMounted.value||blocked.value)return
  await task(()=>loadDirectoryReview(profile.id))
}
async function loadDirectoryReview(id:string){
  directoryReviewOpen.value=true
  directoryReview.value=null
  directoryReview.value=await api('ReviewWritableDirectory',id)
}
async function acknowledgeDirectoryReview(){
  const profile=active.value,record=directoryReview.value
  if(!profile||!writableMounted.value||!record?.recordID)return
  await task(async()=>{
    try{await api('AcknowledgeWritableDirectory',profile.id,record.recordID)}
    catch(e){directoryReview.value={...record,recordID:''};throw e}
    directoryReviewOpen.value=false
    directoryReview.value=null
    notice.value='目录核对已完成'
    await refresh()
  })
}

async function confirmWritableRemount(){await task(async()=>{
  const profile=active.value
  if(!profile||!writableMounted.value)return
  const resolvingDelete=deleteNeedsReview.value
  try{await api('RemountWritableForRename',profile.id)}
  catch(e){
    try{await refresh()}catch{}
    if(!writableMounted.value)writableRemountDialog.value=false
    throw e
  }
  writableRemountDialog.value=false
  notice.value=resolvingDelete?'已重新挂载，文件操作限制已清除':'已重新挂载，改名限制已清除'
  try{await refresh()}
  catch(e){error.value='网盘已重新挂载，状态未能刷新：'+errorMessage(e)}
})}
async function toggleMount(){
  if(mountStateUnknown.value)return
  if(writableRecovery.value){askWritable();return}
  await task(async()=>{
    if(!active.value)return
    if(mounted.value){
      try{await api('Unmount',active.value.id)}
      catch(e){try{await refresh()}catch{}throw e}
      await refresh()
      notice.value='网盘已卸载'
    }else await mountSelected(active.value.id)
  })
}
async function openDrive(){await task(async()=>{if(active.value){await api('OpenMountedDrive',active.value.id);notice.value='已在资源管理器中打开磁盘'}})}
async function showWritablePreview(){
  if(!writableProfileID.value||blocked.value)return
  writablePreviewOpen.value=true
  writablePreview.value=null
  writablePreviewPage.value=1
  await task(async()=>{writablePreview.value=await api('WritableCachePreview') as WritableCachePreview})
}
async function exportWritableCache(){
  if(!writableProfileID.value||blocked.value)return
  await runMountedSave(async()=>{
    const result=await api('ExportWritableCache') as {path:string,files:number,skipped:number}|null
    if(!result)return
    const summary=result.files?'已导出 '+result.files+' 个文件':'仅导出恢复说明'
    notice.value=summary+(result.skipped?'，跳过 '+result.skipped+' 项':'')+' · '+result.path
  })
}
async function archiveWritableCache(){
  if(!writableProfileID.value||blocked.value)return
  writableArchiveDialog.value=false
  await runMountedSave(async()=>{
    try{
      const result=await api('ArchiveWritableSession') as {path:string,files:number,skipped:number}|null
      if(!result)return
      const summary=result.files?'已保存 '+result.files+' 个文件':'已保存恢复说明'
      notice.value='写回会话已结束，'+summary+(result.skipped?'，跳过 '+result.skipped+' 项':'')+' · '+result.path
    }finally{try{await refresh()}catch{}}
  })
}
async function runMountedSave(save:()=>Promise<void>){
  await task(async()=>{
    savingMountedFile.value=true
    mountedSaveProgress.value=null
    let pending=false,finished=false
    const timer=setInterval(async()=>{
      if(pending||finished)return
      pending=true
      try{
        const progress=await api('MountedFileSaveProgress')
        if(!finished&&progress?.active)mountedSaveProgress.value=progress
      }catch{}finally{pending=false}
    },300)
    try{
      await save()
    }finally{
      finished=true
      clearInterval(timer)
      savingMountedFile.value=false
      mountedSaveProgress.value=null
    }
  },true)
}
const blank=():Profile=>({provider:'webdav',id:'',name:'',url:'',user:'',password:'',drive:suggestDrive(profiles.value,occupiedDrives.value)})
const modal=ref(false),form=ref<Profile>(blank()),formElement=ref<HTMLFormElement|null>(null)
const driveOptions=computed(()=>connectionDriveOptions(profiles.value,occupiedDrives.value,form.value.id))
const chosenDrive=computed(()=>driveOptions.value.find(option=>option.drive===form.value.drive))
const formProvider=computed(()=>form.value.provider||'webdav')
const providerFields=computed(()=>storageProviders[formProvider.value])
const objectStorageForm=computed(()=>['r2','minio','tencent','aws','azure','alibaba','backblaze'].includes(formProvider.value))
type CloudStep = {ticket:string, ready:boolean, choices?:{value:string,title:string}[]}
const cloudStep=ref<CloudStep|null>(null), cloudChoice=ref(''), cloudSite=ref('')
const cloudFilter=ref('')
const filteredCloudChoices=computed(()=>{
  const query=cloudFilter.value.trim().toLowerCase()
  return (cloudStep.value?.choices||[]).filter(item=>!query||(item.title||item.value).toLowerCase().includes(query)||item.value===cloudChoice.value)
})
const cloudClientID=ref(''), cloudClientSecret=ref(''), authorizingCloud=ref(false)
const megaCode=ref('')
const megaNetworkMessage=ref('')
const connectionProxy=ref<ConnectionProxy>({mode:'inherit'})
const storageOptions=ref<ObjectOptions>({mode:''})
const proxyAllowed=computed(()=>!['ftp','sftp'].includes(formProvider.value))
const oauthForm=computed(()=>usesBrowserGrant(formProvider.value))
const googleForm=computed(()=>formProvider.value==='googledrive'||formProvider.value==='workspace')
function clearCloudForm(){
  if(cloudStep.value) void api('DiscardCloudLogin',cloudStep.value.ticket).catch(()=>{})
  cloudStep.value=null
  cloudChoice.value=''
  cloudSite.value=''
  cloudFilter.value=''
  cloudClientID.value=''
  cloudClientSecret.value=''
  megaCode.value=''
  megaNetworkMessage.value=''
}
async function authorizeCloud(){
  const last=cloudStep.value
  authorizingCloud.value=true
  try{
    await task(async()=>{
      try{
        cloudStep.value=await api('AuthorizeCloudWithProxy',formProvider.value,last?.ticket||'',cloudChoice.value,
          cloudSite.value.trim(),cloudClientID.value.trim(),cloudClientSecret.value.trim(),form.value.id,formProxy()||null)
        cloudChoice.value=''
        cloudFilter.value=''
        cloudClientSecret.value=''
      }catch(error){
        cloudStep.value=null
        throw error
      }
    },true)
  }finally{authorizingCloud.value=false}
}
function changeProvider(event:Event){
  clearCloudForm()
  form.value.provider=(event.target as HTMLSelectElement).value as StorageProvider
  form.value.url=''
  form.value.user=''
  form.value.password=''
  form.value.hostKey=''
  connectionProxy.value={mode:'inherit'}
  storageOptions.value={mode:''}
}
function add(){clearCloudForm();form.value=blank();connectionProxy.value={mode:'inherit'};storageOptions.value={mode:''};error.value='';modal.value=true}
function edit(){if(!active.value||mounted.value)return;clearCloudForm();connectionMenu.value?.removeAttribute('open');form.value={...active.value,password:'',clearPassword:false};connectionProxy.value={...(active.value.proxy||{mode:'inherit'}),password:''};storageOptions.value={mode:'',...active.value.storage,sessionToken:''};error.value='';modal.value=true}
function formProxy(){
  if(!proxyAllowed.value||connectionProxy.value.mode==='inherit')return undefined
  const p=connectionProxy.value
  return {...p,port:Number(p.port||0),protocol:p.protocol||'http'}
}
async function probeConnectionMEGA(){
  megaNetworkMessage.value=''
  await task(async()=>{megaNetworkMessage.value=await api('ProbeConnectionMEGA',form.value.id,formProxy()||null)},true)
}
async function save(connect=false){
  if(formElement.value&&!formElement.value.reportValidity())return
  if(connect&&chosenDrive.value?.occupied){error.value='此盘符已被 Windows 占用，请选择其他盘符，或仅保存连接。';return}
  const o=storageOptions.value
  const storage=objectStorageForm.value&&(o.mode||o.region)?{...o,tenant:o.mode==='entra'?o.tenant:'',clientId:o.mode==='entra'?o.clientId:'',sessionToken:o.mode==='session'?o.sessionToken:''}:undefined
  const profile={...form.value,proxy:formProxy(),storage}
  const code=megaCode.value.trim()
  try{await task(async()=>{
    await saveConnectionFlow(()=>cloudStep.value?.ready ? api('SaveCloudConnection',profile,cloudStep.value.ticket) as Promise<Profile> : code ? api('SaveMEGAConnection',profile,code) as Promise<Profile> : api('Save',profile) as Promise<Profile>,async p=>{clearCloudForm();modal.value=false;await refresh();selectProfile(p.id);notice.value='网盘已保存'},async p=>{
      try{await mountSelected(p.id)}catch(e){throw new Error('网盘已保存，挂载失败：'+errorMessage(e))}
    },connect)
  },!!code)}finally{if(code)megaCode.value=''}
}
const confirmDelete=ref(false),cacheConfirm=ref(false)
function askDelete(){connectionMenu.value?.removeAttribute('open');error.value='';confirmDelete.value=true}
async function remove(){await task(async()=>{if(!active.value)return;const id=active.value.id;await api('Delete',id);confirmDelete.value=false;selected.value='';await refresh();notice.value='网盘配置已删除'})}
const batchReport=ref<BatchReport|null>(null),batchActive=ref(false),batchModal=ref(false)
const batchStates:Record<string,string>={success:'成功',skipped:'跳过',failed:'失败',cancelled:'未处理'}
type PresetRequest={id:string,mount:boolean,switchMode?:boolean}
type PresetPreviewItem=Omit<BatchItem,'state'> & {state:'ready'|'blocked'|'skipped'}
type PresetPreview=PresetRequest & {name:string,items:PresetPreviewItem[],ready:number,blocked:number,revision:string}
const presetRequest=ref<PresetRequest|null>(null),presetPreview=ref<PresetPreview|null>(null)
const switchSource=ref('')
const trayDrivesOpen=ref(false)
const trayMountedProfiles=computed(()=>profiles.value.filter(p=>isProfileMounted(mounts.value,p)))
const canPreviewSwitch=computed(()=>!!switchSource.value&&!!presetRequest.value?.id&&switchSource.value!==presetRequest.value.id)
const presetCloseButton=ref<HTMLButtonElement|null>(null)
let previewFromManager=false
async function openPresetPreview(id:string,mount:boolean,switchMode=false){
  if(blocked.value)return
  previewFromManager=groupOpen.value
  groupOpen.value=false
  batchMenu.value?.removeAttribute('open')
  presetRequest.value={id,mount,switchMode}
  switchSource.value=''
  const pending=switchMode?undefined:refreshPresetPreview()
  await nextTick()
  presetCloseButton.value?.focus()
  await pending
}
function resetPresetPreview(){
  presetPreview.value=null
  error.value=''
}
async function refreshPresetPreview(){
  const request=presetRequest.value
  if(!request||blocked.value||(request.switchMode&&!canPreviewSwitch.value))return
  await task(async()=>{
    presetPreview.value=null
    if(request.switchMode){
      const plan=await api('PreviewMountPresetSwitch',switchSource.value,request.id) as {source:string,target:string,items:PresetPreviewItem[],ready:number,blocked:number,revision:string}
      presetPreview.value={...request,...plan,name:plan.source+' → '+plan.target}
    }else{
      presetPreview.value=await api('PreviewMountPreset',request.id,request.mount) as PresetPreview
    }
  })
}
async function confirmPresetPreview(){
  const preset=presetPreview.value
  if(!preset||blocked.value||preset.blocked||!preset.ready)return
  await batchConnections(preset.mount,undefined,preset)
}
async function batchConnections(mount:boolean,group?:string,preset?:PresetPreview){
  if(mountStateUnknown.value)return
  batchMenu.value?.removeAttribute('open')
  await task(async()=>{
    batchActive.value=true;batchReport.value=null
    try{
      if(preset)presetPreview.value=null
      const report=await (preset?.switchMode?api('ApplyMountPresetSwitch',switchSource.value,preset.id,preset.revision):preset?api('ApplyMountPreset',preset.id,mount,preset.revision):group===undefined?api(mount?'MountAll':'UnmountAll'):api(mount?'MountGroup':'UnmountGroup',group)) as BatchReport
      if(preset)presetRequest.value=null
      batchReport.value=report;await refresh();notice.value=batchSummary(report,mount,preset?.switchMode?'方案切换':undefined)
    }finally{batchActive.value=false}
  },true)
}
const recoveryModal=ref(false)
function recover(){if(!blocked.value){error.value='';recoveryModal.value=true}}
async function cancel(){await cancelTask(()=>api('CancelOperation'))}
async function retryInstallUpdate(){await task(async()=>{try{updateState.value=await api('InstallDownloadedUpdate');notice.value=updateState.value.message||''}finally{try{updateState.value=await api('UpdateInfo')}catch{}}})}
async function exitApp(){try{await api('Exit')}catch(e){error.value=errorMessage(e)}}
const diagnosticPath=ref(''),diagnosticSession=ref(''),diagnosticOperation=ref(''),diagnosticOnlyErrors=ref(false)
const diagnosticReport=ref<DiagnosticSessions>({currentID:'',sessions:[],warning:''})
const diagnosticSource=computed(()=>diagnosticReport.value.sessions.find(session=>session.id===diagnosticSession.value)?.events??[])
const diagnosticEvents=computed(()=>filterDiagnosticEvents(diagnosticSource.value,diagnosticOperation.value,diagnosticOnlyErrors.value))
const diagnosticOperations=computed(()=>[...new Set(diagnosticSource.value.map(event=>event.operation))].sort((a,b)=>(operationNames[a]||a).localeCompare(operationNames[b]||b,'zh-CN')))
watch(diagnosticSession,()=>{diagnosticOperation.value='';diagnosticPath.value=''})
function eventTime(value:string){const d=new Date(value);return Number.isNaN(d.getTime())?'—':d.toLocaleString('zh-CN',{hour12:false})}
async function loadDiagnostics(){const report=await api('DiagnosticSessions') as DiagnosticSessions;diagnosticReport.value=report;if(!report.sessions.some(session=>session.id===diagnosticSession.value))diagnosticSession.value=report.currentID}
async function openDiagnostics(){await task(async()=>{await loadDiagnostics();screen.value='diagnostics'})}
async function exportDiagnostics(){await task(async()=>{const saved:any=await api('ExportDiagnosticSessionAs',diagnosticSession.value);if(saved){diagnosticPath.value=saved;notice.value='诊断日志已导出'}else notice.value='已取消导出'})}
const updateState=ref<UpdateState>({...initialUpdate})
const updateView=computed(()=>updateFeedback(updateState.value))
const downloadView=computed(()=>updateProgress(updateState.value))
const updateProgressPanel=ref<HTMLElement|null>(null)
watch(()=>downloadView.value.visible,async visible=>{if(visible){await nextTick();updateProgressPanel.value?.scrollIntoView({block:'nearest'})}})
const downloadingUpdate=ref(false)
let stopUpdatePolling:(()=>void)|undefined
onUnmounted(()=>stopUpdatePolling?.())
async function refreshReleaseSettings(){updateState.value=await loadReleaseSettings(api)}
async function clearUpdateDownloadCache(){await task(async()=>{updateState.value=await api('ClearUpdateDownloadCache') as UpdateState;notice.value='下载缓存已清理，已保存的安装包保留'})}
async function releaseAction(action:'check'|'download'){
  if(blocked.value)return
  let polling=false,stopped=false
  const poll=async()=>{if(polling||stopped)return;polling=true;try{const state=await api('UpdateInfo') as UpdateState;if(!stopped)updateState.value=state}catch{}finally{polling=false}}
  downloadingUpdate.value=action==='download'
  const timer=action==='download'?setInterval(poll,500):undefined
  stopUpdatePolling=()=>{stopped=true;if(timer)clearInterval(timer)}
  try{await task(async()=>{const result=await withReleaseStateRecovery(()=>runReleaseAction(api,action,updateState.value),refreshReleaseSettings);if(result.update)updateState.value=result.update;notice.value=result.notice},true)}
  finally{stopUpdatePolling?.();stopUpdatePolling=undefined;downloadingUpdate.value=false}
}
const loginStartupData=ref<any>(null),loginStartupEnabled=ref(false)
const cacheData=ref<any>(null),cacheForm=ref({baseDir:'',maxGiB:2})
const cacheMigrationConfirm=ref(false),migrationActive=ref(false),migrationPreview=ref<any>(null),migrationProgress=ref<any>(null)
const pendingMigration=ref<any>(null),discardMigrationConfirm=ref(false)
const migrationConfig=ref({baseDir:'',maxGiB:2})
const cacheHistory=ref<any>({entries:[],warning:''}),cacheHistoryStats=ref<Record<string,any>>({})
const registerCacheDialog=ref(false),historyDirectory=ref(''),forgetCacheRecord=ref<any>(null),clearRetainedRecord=ref<any>(null)
const cacheHistoryKinds:Record<string,string>={migrated:'迁移后保留',changed:'更换位置后保留',incomplete:'未启用副本',manual:'手动登记'}
async function loadCacheHistory(){cacheHistory.value=await api('CacheHistory')}
function registerCache(){historyDirectory.value='';registerCacheDialog.value=true;error.value=''}
async function chooseHistoryDirectory(){await task(async()=>{const path:any=await api('ChooseCacheDirectory',historyDirectory.value);if(path)historyDirectory.value=path})}
async function rememberCache(){await task(async()=>{await api('RememberCacheDirectory',historyDirectory.value);registerCacheDialog.value=false;await loadCacheHistory();notice.value='旧缓存目录已登记'})}
async function scanCacheHistory(id:string){await task(async()=>{
  try{cacheHistoryStats.value[id]={...await api('CacheHistoryUsage',id),scannedAt:new Date().toISOString()}}
  catch(e){cacheHistoryStats.value[id]={error:errorMessage(e)};throw e}
},true)}
async function openCacheHistory(id:string){await task(async()=>{await api('OpenCacheHistory',id);notice.value='已在资源管理器中打开目录'})}
async function clearRetainedCache(){await task(async()=>{const id=clearRetainedRecord.value?.id;if(!id)return;await api('ClearRetainedCache',id);delete cacheHistoryStats.value[id];clearRetainedRecord.value=null;await loadCacheHistory();notice.value='旧缓存已清理，记录和其他文件保留'})}
async function forgetCacheHistory(){await task(async()=>{const id=forgetCacheRecord.value?.id;if(!id)return;await api('ForgetCacheHistory',id);delete cacheHistoryStats.value[id];forgetCacheRecord.value=null;await loadCacheHistory();notice.value='目录记录已移除，文件保留'})}
async function askMigrateCache(){await task(async()=>{
  migrationConfig.value={...cacheForm.value,maxGiB:Number(cacheForm.value.maxGiB)}
  migrationPreview.value=await api('PreviewCacheMigration',migrationConfig.value)
  cacheMigrationConfirm.value=true
})}
async function discardMigration(){await task(async()=>{
  await api('DiscardCacheMigration')
  cacheMigrationFallback.value=false
  discardMigrationConfirm.value=false
  pendingMigration.value=null
  migrationProgress.value=await api('CacheMigrationStatus')
  await loadCache()
  notice.value='迁移任务已放弃，原缓存和副本均保留'
})}
async function migrateCache(resume=false){await task(async()=>{
  cacheMigrationConfirm.value=false;migrationActive.value=true;migrationProgress.value={phase:'preparing',message:'正在准备迁移',bytes:0,totalBytes:0,files:0,totalFiles:0}
  let closed=false,pending=false
  const poll=setInterval(async()=>{
    if(closed||pending)return;pending=true
    try{const status:any=await api('CacheMigrationStatus');if(!closed){migrationProgress.value=status;canCancel.value=['preparing','copying','verifying'].includes(status.phase)}}catch{}finally{pending=false}
  },250)
  try{
    migrationProgress.value=resume?await api('ResumeCacheMigration'):await api('MigrateCache',migrationConfig.value)
    closed=true;clearInterval(poll);canCancel.value=false
    await refresh()
    try{await loadCache()}catch(e){error.value='迁移已完成，缓存统计未能刷新：'+errorMessage(e)}
    notice.value='缓存已迁移并校验，原缓存仍保留'
  }finally{
    closed=true;clearInterval(poll);migrationActive.value=false
    try{migrationProgress.value=await api('CacheMigrationStatus')}catch{}
    try{await loadCacheHistory();pendingMigration.value=await api('PendingCacheMigration')}catch{}
  }
},true)}
async function openSettings(){screen.value='settings';settingsTab.value='general';await task(async()=>{loginStartupData.value=await api('LoginStartupInfo');loginStartupEnabled.value=!!loginStartupData.value.enabled;autoRecoveryState.value=await api('AutoRecoveryInfo');autoRecoveryEnabled.value=!!autoRecoveryState.value.enabled;maintenance.value=await api('MaintenanceInfo');maintenanceSettings.value={...maintenance.value.settings}})}
async function saveMaintenance(){await task(async()=>{maintenance.value=await api('SaveMaintenance',{...maintenanceSettings.value});notice.value='连接维护设置已保存'})}
async function retryMountConnection(){
  const profile=active.value
  if(!profile||!mounted.value||!engineOnline.value||blocked.value)return
  await task(async()=>{
    maintenance.value=await api('RetryMountConnection',profile.id)
    if(maintenance.value.connections[profile.id]?.state==='online')notice.value='网盘连接正常'
  },true)
}
async function saveAutoRecovery(){await task(async()=>{autoRecoveryState.value=await api('SaveAutoRecovery',autoRecoveryEnabled.value);notice.value=autoRecoveryEnabled.value?'自动恢复已开启':'自动恢复已关闭，后续重试已取消'})}
async function saveLoginStartup(){await task(async()=>{loginStartupData.value=await api('SaveLoginStartup',loginStartupEnabled.value);notice.value=loginStartupEnabled.value?'已保存，下次登录 Windows 时启动':'已关闭登录启动'})}
async function loadCache(){cacheData.value=await api('CacheInfo');cacheForm.value={...cacheData.value.settings};await loadCacheHistory();pendingMigration.value=await api('PendingCacheMigration')}
async function openCacheRecovery(){
  if(blocked.value||dialogOpen.value)return
  screen.value='settings'
  await chooseSettingsTab('cache')
}
async function chooseSettingsTab(tab:'general'|'proxy'|'cache'|'transfer'|'about'){if(blocked.value)return;settingsTab.value=tab;if(tab==='cache')await task(loadCache,true);if(tab==='about')await task(refreshReleaseSettings)}
async function chooseCacheDirectory(){await task(async()=>{const path:any=await api('ChooseCacheDirectory',cacheForm.value.baseDir);if(path)cacheForm.value.baseDir=path})}
async function saveCache(){await task(async()=>{await api('SaveCacheSettings',{...cacheForm.value,maxGiB:Number(cacheForm.value.maxGiB)});await loadCache();notice.value='缓存设置已保存；旧缓存保留在原目录'})}
async function clearCache(){await task(async()=>{await api('ClearCache');cacheConfirm.value=false;await loadCache();notice.value='文件缓存已清理'})}
const dialogOpen=computed(()=>s3ReviewOpen.value||directoryReviewOpen.value||writableRemountDialog.value||writablePreviewOpen.value||writableArchiveDialog.value||editCopyDialog.value||writableDialog.value||groupOpen.value||trayDrivesOpen.value||modal.value||confirmDelete.value||cacheConfirm.value||batchModal.value||!!presetRequest.value||recoveryModal.value||cacheMigrationConfirm.value||discardMigrationConfirm.value||registerCacheDialog.value||!!forgetCacheRecord.value||!!clearRetainedRecord.value)
let traySignal=false,trayProcessing=false,pendingTrayShortcut=''
let stopTrayListener:(()=>void)|undefined
function onTrayShortcut(){
  traySignal=true
  void processTrayShortcut()
}
async function processTrayShortcut(){
  if(trayProcessing||blocked.value||dialogOpen.value||(!traySignal&&!pendingTrayShortcut))return
  trayProcessing=true
  try{
    if(traySignal){
      traySignal=false
      const next=await api('TakeTrayShortcut') as string
      if(next)pendingTrayShortcut=next
    }
    if(blocked.value||dialogOpen.value||!pendingTrayShortcut)return
    const shortcut=pendingTrayShortcut
    pendingTrayShortcut=''
    let ready=false
    await task(async()=>{
      await refresh()
      if(shortcut!=='mounted-drives')await loadMountPresets()
      ready=true
    })
    if(!ready)return
    if(blocked.value||dialogOpen.value){
      pendingTrayShortcut=shortcut
      return
    }
    if(shortcut==='mounted-drives')trayDrivesOpen.value=true
    if(shortcut==='mount-presets'||(shortcut==='preset-switch'&&(mountPresets.value.length<2||!engineOnline.value))){
      connectionGroupsControl.value?.openPresetManager()
    }else if(shortcut==='preset-switch'){
      await openPresetPreview('',true,true)
    }
  }catch(e){
    error.value=errorMessage(e)
  }finally{
    trayProcessing=false
    if(traySignal&&!blocked.value&&!dialogOpen.value)void processTrayShortcut()
  }
}
watch([blocked,dialogOpen],()=>void processTrayShortcut())
async function openTrayDrive(id:string){
  await task(async()=>{
    try{await api('OpenMountedDrive',id)}
    catch(e){try{await refresh()}catch{}throw e}
    notice.value='已在资源管理器中打开磁盘'
  })
}
let previousFocus:HTMLElement|null=null
watch(dialogOpen,async open=>{
  if(open){previousFocus=document.activeElement as HTMLElement;await nextTick();(document.querySelector<HTMLElement>('.dialog input:not([disabled])')??document.querySelector<HTMLElement>('.dialog button:not([disabled])'))?.focus()}
  else{await nextTick();if(previousFocus?.isConnected)previousFocus.focus()}
})
function closeDialog(){
  if(busy.value)return
  if(editCopyDialog.value){editCopyDialog.value=false;confirmForgetEdit.value=false;error.value='';return}
  if(s3ReviewOpen.value){s3ReviewOpen.value=false;s3DeleteMode.value=false;s3ReviewResult.value=null;s3ReviewQueue.value=null;error.value='';return}
  if(directoryReviewOpen.value){directoryReviewOpen.value=false;directoryReview.value=null;error.value='';return}
  if(writableRemountDialog.value){writableRemountDialog.value=false;error.value='';return}
  if(writablePreviewOpen.value){writablePreviewOpen.value=false;writablePreview.value=null;error.value='';return}
  if(writableArchiveDialog.value){writableArchiveDialog.value=false;error.value='';return}
  if(writableDialog.value){writableDialog.value=false;error.value='';return}
  if(presetRequest.value){
    presetRequest.value=null
    presetPreview.value=null
    error.value=''
    groupOpen.value=previewFromManager
    void nextTick(()=>{
      if(groupOpen.value)document.querySelector<HTMLElement>('.group-dialog [role="tab"][aria-selected="true"]')?.focus()
      else batchMenu.value?.querySelector('summary')?.focus()
    })
    return
  }
  groupOpen.value=false;trayDrivesOpen.value=false;if(modal.value)clearCloudForm();modal.value=false;confirmDelete.value=false;cacheConfirm.value=false;batchModal.value=false;recoveryModal.value=false;cacheMigrationConfirm.value=false;discardMigrationConfirm.value=false;registerCacheDialog.value=false;forgetCacheRecord.value=null;clearRetainedRecord.value=null;error.value=''
}
function keyboard(event:KeyboardEvent){
  if(event.defaultPrevented||(event.isComposing&&event.key!=='F5'))return
  const target=event.target instanceof HTMLElement?event.target:null
  const shortcut=workspaceShortcut(event,{screen:screen.value,hasProfile:!!active.value,blocked:blocked.value||event.isComposing,dialog:dialogOpen.value})
  if(shortcut){
    event.preventDefault()
    if(shortcut==='refreshStatus')void task(async()=>{await refresh();notice.value='挂载状态已刷新'})
    if(shortcut==='refreshLogs')void task(async()=>{await loadDiagnostics();notice.value='日志已刷新'})
    return
  }
  if(event.key==='Escape'){
    if(dialogOpen.value)closeDialog()
    for(const menu of [connectionMenu.value,batchMenu.value]){
      if(!menu?.open)continue
      menu.open=false
      if(target&&menu.contains(target))menu.querySelector('summary')?.focus()
    }
  }
  if(event.key==='Tab'&&dialogOpen.value){
    const controls=Array.from(document.querySelectorAll<HTMLElement>('.dialog button:not([disabled]),.dialog input:not([disabled]),.dialog select:not([disabled])'))
    const first=controls[0],last=controls.at(-1)
    if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus()}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus()}
  }
}
function closeMenus(event:Event){
  const target=event.target
  for(const menu of [connectionMenu.value,batchMenu.value]){
    if(menu&&target instanceof Node&&!menu.contains(target))menu.open=false
  }
}
let timer:ReturnType<typeof setInterval>
onMounted(()=>{
  stopTrayListener=WailsRuntime.Events?.On('webdavmount:tray-shortcut',onTrayShortcut)
  void task(async()=>{await refresh();await loadMountPresets();await loadEditCopy()}).then(()=>{if(stopTrayListener)onTrayShortcut()})
  timer=setInterval(()=>{if(!busy.value)refresh().catch(e=>{error.value=errorMessage(e)})},3000)
  document.addEventListener('keydown',keyboard);document.addEventListener('pointerdown',closeMenus)
})
onUnmounted(()=>{stopTrayListener?.();clearInterval(timer);document.removeEventListener('keydown',keyboard);document.removeEventListener('pointerdown',closeMenus)})
</script>

<template>
<div class="app-shell">
  <aside class="sidebar">
    <div class="brand"><img class="brand-logo" :src="logoUrl" alt="" /><span>WebDAVMount</span></div>
    <button class="primary add-drive" :disabled="blocked" @click="add"><UiIcon name="plus"/>添加网盘</button>
    <div class="nav-heading"><span>网盘</span>
      <details ref="batchMenu" class="dropdown batch-menu"><summary aria-label="批量操作" :aria-disabled="blocked" title="批量操作">批量<UiIcon name="down" :size="12"/></summary><div class="dropdown-panel"><button :disabled="blocked||!profiles.length||!mountCommandsAvailable" @click="batchConnections(true)">挂载全部网盘</button><button :disabled="blocked||!profiles.length||!mountCommandsAvailable" @click="batchConnections(false)">卸载全部网盘</button><div v-if="mountPresets.length" class="batch-preset-list" aria-label="挂载方案">
  <div v-for="preset in mountPresets" :key="preset.id" class="batch-preset">
    <strong :title="preset.name">{{preset.name}}</strong>
    <button :aria-label="'挂载方案 '+preset.name" :disabled="blocked||!mountCommandsAvailable||preset.connectionIDs.some(id=>!presetKeys.has(id))" @click="openPresetPreview(preset.id,true)">挂载</button>
    <button :aria-label="'卸载方案 '+preset.name" :disabled="blocked||!mountCommandsAvailable||preset.connectionIDs.some(id=>!presetKeys.has(id))" @click="openPresetPreview(preset.id,false)">卸载</button>
  </div>
</div></div></details>
    </div>
    <ConnectionGroups ref="connectionGroupsControl" :profiles="profiles" :selected="selected" :connections-screen="screen==='connections'" :mounted-ids="profiles.filter(p=>isProfileMounted(mounts,p)).map(p=>p.id)" :busy="blocked" :online="mountCommandsAvailable" :open="groupOpen" :error="error" :api="api" :run="task" :reload="refresh" :presets="mountPresets" :preset-read-error="presetReadError" :reload-presets="loadMountPresets" @preset="openPresetPreview" @switch-preset="openPresetPreview('',true,true)" @choose="choose" @manage="value=>{groupOpen=value;error=''}" @notice="notice=$event" @batch="(group,mount)=>batchConnections(mount,group)"/>
    <nav class="nav-tools" aria-label="应用导航"><button :class="{active:screen==='diagnostics'}" :aria-current="screen==='diagnostics'?'page':undefined" :disabled="blocked" @click="openDiagnostics"><UiIcon name="logs"/>诊断日志</button><button :class="{active:screen==='settings'}" :aria-current="screen==='settings'?'page':undefined" :disabled="blocked" @click="openSettings"><UiIcon name="settings"/>设置</button></nav>
    <button class="exit-button" @click="exitApp"><UiIcon name="power"/>退出并卸载</button>
  </aside>

  <main class="main-area">
    <header class="page-header">
      <div class="page-title">
        <h1 :title="title">{{title}}</h1>
        
      </div>
      <div v-if="screen==='connections'&&active" class="header-actions">
        <button v-if="mounted" class="primary" :disabled="blocked" @click="openDrive"><UiIcon name="external" :size="16"/>打开磁盘</button>
        <button :class="{primary:!mounted}" :disabled="blocked||mountStateUnknown||(!engineOnline&&!writableRecovery)" @click="toggleMount"><UiIcon :name="mounted?'eject':'mount'" :size="16"/>{{mounted?'卸载':writableRecovery?'恢复可写挂载':'挂载网盘'}}</button>
        <details ref="connectionMenu" class="dropdown">
          <summary aria-label="网盘更多操作" title="网盘更多操作"><UiIcon name="more" :size="17"/></summary>
          <div class="dropdown-panel connection-menu">
            <button v-if="writableAvailable&&!mounted&&!writableRecovery" :disabled="blocked||!mountCommandsAvailable" @click="askWritable()">可写挂载…</button>
            <button :disabled="blocked||mountStateUnknown||mounted||writableRecovery" @click="edit">编辑网盘</button>
            <button class="danger-text" :disabled="blocked||mountStateUnknown||mounted||writableRecovery" @click="askDelete">删除网盘</button>
          </div>
        </details>
      </div>
      <button v-else-if="screen==='diagnostics'" class="primary" :disabled="blocked" @click="exportDiagnostics"><UiIcon name="external" :size="16"/>导出所选会话</button>
    </header>

    <div v-if="!dialogOpen&&error" class="alert error-alert" role="alert"><UiIcon name="info"/><span>{{error}}</span><button class="icon-button" aria-label="关闭错误提示" @click="error='' "><UiIcon name="close" :size="16"/></button></div>
    <div v-if="!engineOnline&&!autoRecovering" class="alert warning-alert" role="alert"><span>挂载服务已停止，当前磁盘可能无法访问。</span><button :disabled="blocked" @click="recover">恢复挂载</button></div>
    <div v-if="mountStateUnknown" class="alert warning-alert" role="status">
      <span>{{writableProfileID?'盘符状态待确认。可写会话已保留。':'盘符状态待确认。挂载服务暂无法确认现有盘符。'}}</span>
      <button :disabled="blocked||dialogOpen" @click="task(refresh)">刷新状态</button>
      <button v-if="writableProfileID" :disabled="blocked||dialogOpen" @click="showWritablePreview">查看待写回文件</button>
      <button v-if="writableProfileID" :disabled="blocked||dialogOpen" @click="exportWritableCache">导出写回副本…</button>
    </div>
    <div v-if="editCopy||editCopyWarning" class="alert info-alert" role="status">
      <span>{{editCopyWarning||'有一个本机编辑副本待提交'}}</span>
      <button :disabled="blocked||dialogOpen" @click="openEditCopyDialog">管理编辑副本</button>
    </div>
    <div v-if="writableJournalError||writableNeedsRecovery" class="alert warning-alert" role="status">
      <span>{{writableJournalError?'无法读取可写会话记录，写回缓存已保留。':'有可写会话等待恢复。请回到该网盘，手动恢复可写挂载。'}}</span>
      <button v-if="writableNeedsRecovery&&active?.id!==writableProfileID" :disabled="blocked" @click="selectProfile(writableProfileID)">前往网盘</button>
      <button v-if="writableNeedsRecovery" :disabled="blocked||dialogOpen" @click="showWritablePreview">查看待写回文件</button>
      <button v-if="writableNeedsRecovery" :disabled="blocked||dialogOpen" @click="exportWritableCache">导出写回副本…</button>
      <button v-if="writableNeedsRecovery" :disabled="blocked||dialogOpen" @click="writableArchiveDialog=true">保存副本并结束写回…</button>
    </div>
    <div v-if="firstDirectoryReview&&(screen!=='connections'||!activeDirectoryReview)" class="alert info-alert" role="status">
      <span>{{firstDirectoryReview.name}}有目录操作待核对</span>
      <button :disabled="blocked||dialogOpen" @click="selectProfile(firstDirectoryReview.id)">查看网盘</button>
    </div>
    <div v-if="sessionRecovery.previous||sessionRecovery.warning" class="alert info-alert" role="status">
      <span>{{sessionRecovery.warning||'上次会话有网盘可供手动恢复。'}}</span><button :disabled="blocked" @click="recover">查看上次挂载</button>
    </div>
    <div v-if="autoRecoveryState.enabled&&['waiting','running','paused'].includes(autoRecoveryState.state)" class="alert info-alert" role="status"><span>{{autoRecoveryState.message}}<template v-if="autoRecoveryState.nextRetry"> 下次尝试：{{eventTime(autoRecoveryState.nextRetry)}}（已尝试 {{autoRecoveryState.attempts}} / 3 次）</template></span></div>
    <StartupTasks :report="startupTasks" :busy="busy" :api="api" :run="task" :refresh="refresh" @notice="notice=$event"/>
    <div v-if="cacheMigrationFallback" class="alert cache-recovery-alert" role="status">
      <span>迁移目标暂不可用，当前使用原缓存。迁移记录已保留。</span>
      <button v-if="screen!=='settings'||settingsTab!=='cache'" :disabled="blocked||dialogOpen" @click="openCacheRecovery">查看缓存迁移</button>
    </div>
    <div v-if="busy" class="operation-bar" role="status"><span class="spinner"></span><span>{{editCopyOperation|| (migrationActive?(migrationProgress?.message||'正在迁移缓存…'):batchActive?'正在逐个处理网盘，取消将在当前网盘处理完后生效':savingMountedFile?mountedSaveMessage:downloadingUpdate?'正在下载更新包，进度见下方':'正在处理，请稍候…')}}</span><progress v-if="savingMountedFile&&mountedSaveProgress&&mountedSaveProgress.total>0" class="mounted-save-bar" :title="mountedSaveProgress.name" aria-label="写回副本导出进度" :value="mountedSaveProgress.bytes" :max="mountedSaveProgress.total"></progress><button v-if="canCancel" @click="cancel">{{savingMountedFile?'取消导出':batchActive?'取消后续处理':downloadingUpdate?'暂停下载':migrationActive?'暂停迁移':'取消'}}</button></div>

    <section v-if="screen==='connections'&&active" class="workspace mount-workspace">
      <dl class="mount-summary" aria-label="网盘状态">
        <div><dt>盘符</dt><dd class="mount-letter">{{active.drive}}</dd></div>
        <div><dt>状态</dt><dd class="mount-status" :class="{'mount-status-ready':mounted&&engineOnline}">{{mountStateUnknown?'待确认':mounted?(engineOnline?(writableMounted?'已挂载（可写）':'已挂载（只读）'):'挂载服务已停止'):writableRecovery?'等待恢复':'未挂载'}}</dd></div>
        <div><dt>类型</dt><dd>{{providerLabel(active.provider)}}</dd></div>
        <div v-if="active.url" class="mount-summary-wide"><dt>地址</dt><dd class="selectable-path">{{active.url}}</dd></div>
        <div v-if="active.user||usesBrowserGrant(active.provider)"><dt>账户</dt><dd>{{usesBrowserGrant(active.provider)?'浏览器授权':active.user}}</dd></div>
        <div v-if="active.note" class="mount-summary-wide"><dt>备注</dt><dd>{{active.note}}</dd></div>
      </dl>
      <div v-if="writableMounted&&writableStatus?.s3ReviewCount" class="inline-note writeback-note">
        <span role="status">{{writableStatus.s3ReviewCount}} 个 S3 文件待核对</span>
        <button :disabled="blocked||dialogOpen" @click="openS3Review">核对写回…</button>
      </div>
      <div v-if="writableMounted&&writableStatus?.s3DeleteReview" class="inline-note writeback-note">
        <span role="status">有 S3 删除结果待核对</span>
        <button :disabled="blocked||dialogOpen" @click="inspectS3Delete()">核对删除…</button>
      </div>
      <div v-if="activeDirectoryReview" class="inline-note writeback-note">
        <span role="status">有目录操作待核对</span>
        <button v-if="writableMounted" :disabled="blocked||dialogOpen||!mountCommandsAvailable" @click="checkDirectoryReview">核对目录操作…</button>
        <button v-else-if="!mounted" :disabled="blocked||dialogOpen||!mountCommandsAvailable||!writableAvailable" @click="askWritable(true)">挂载并核对…</button>
      </div>
      <div v-if="writableMounted&&deleteMessage" class="inline-note writeback-note">
        <span role="status">{{deleteMessage}}</span>
        <button v-if="deleteNeedsReview" :disabled="blocked||dialogOpen" @click="error='';writableRemountDialog=true">核对后重新挂载…</button>
      </div>
      <div v-if="writableMounted&&writableStatus?.renameBlocked" class="inline-note writeback-note">
        <span role="status">{{writableStatus.renameBlocked}} 个文件改名冲突或结果待确认。请核对源文件和目标文件。</span>
        <button :disabled="blocked||dialogOpen" @click="error='';writableRemountDialog=true">重新挂载…</button>
      </div>
      <div v-if="writableMounted" class="inline-note writeback-note"><span role="status">{{writableMessage}}</span><div class="writeback-actions"><button class="text-button" :disabled="blocked||dialogOpen" @click="showWritablePreview">查看待写回文件</button><button class="text-button" :disabled="blocked||dialogOpen" @click="exportWritableCache">导出写回副本…</button><button class="text-button" :disabled="blocked||dialogOpen" @click="writableArchiveDialog=true">保存副本并结束写回…</button></div></div>
      <div v-if="startup[active.id]?.state==='failed'&&!mounted" class="inline-note">{{startup[active.id].message}}，可点击“挂载网盘”重试。</div>
      <div v-if="mounted&&maintenance.settings.remoteChecks&&activeProbe&&(activeProbe.state==='offline'||activeProbe.state==='paused')" class="inline-note writeback-note">
        <span role="status">{{activeProbe.message}}</span>
        <button :disabled="blocked||dialogOpen||!engineOnline" @click="retryMountConnection">重试连接</button>
      </div>
    </section>

    <section v-else-if="screen==='connections'" class="welcome empty-state"><h2>还没有网盘</h2><button class="primary" :disabled="blocked" @click="add"><UiIcon name="plus" :size="16"/>添加网盘</button></section>


    <section v-else-if="screen==='diagnostics'" class="log-page"><div class="section-toolbar"><label class="diagnostic-session-picker">日志会话<select v-model="diagnosticSession" :disabled="blocked"><option v-for="(session,index) in diagnosticReport.sessions" :key="session.id" :value="session.id">{{diagnosticSessionLabel(session,diagnosticReport.currentID,index)}}</option></select></label><button :disabled="blocked" @click="task(loadDiagnostics)"><UiIcon name="refresh" :size="16"/>刷新日志</button></div><p v-if="diagnosticReport.warning" class="setting-warning" role="alert">{{diagnosticReport.warning}}</p><div class="diagnostic-filters"><label>操作类型<select v-model="diagnosticOperation" :disabled="blocked"><option value="">全部操作</option><option v-for="operation in diagnosticOperations" :key="operation" :value="operation">{{operationNames[operation]||'其他操作'}}</option></select></label><label class="check-row"><input v-model="diagnosticOnlyErrors" type="checkbox" :disabled="blocked">仅显示错误</label><button :disabled="blocked||(!diagnosticOperation&&!diagnosticOnlyErrors)" @click="diagnosticOperation='';diagnosticOnlyErrors=false">清除筛选</button><span class="page-hint">显示 {{diagnosticEvents.length}} / {{diagnosticSource.length}} 条</span></div><label v-if="diagnosticPath" class="export-location">已导出到<input readonly :value="diagnosticPath" @focus="($event.target as HTMLInputElement).select()"></label><div v-if="diagnosticEvents.length" class="log-table-scroll" tabindex="0" aria-label="诊断日志列表"><table class="log-table"><thead><tr><th>时间</th><th>操作</th><th>结果</th><th>耗时</th></tr></thead><tbody><tr v-for="(event,index) in diagnosticEvents" :key="index"><td>{{eventTime(event.time)}}</td><td>{{operationNames[event.operation]||'其他操作'}}</td><td :class="{'danger-text':diagnosticErrorsOnly(event.result)}">{{resultNames[event.result]||'操作失败'}}</td><td>{{event.durationMs}} ms</td></tr></tbody></table></div><div v-else class="empty-state"><UiIcon name="logs" :size="36"/><h2>{{diagnosticSource.length?'没有符合筛选条件的记录':'所选会话还没有记录'}}</h2></div></section>

    <section v-else class="settings-page"><nav class="settings-tabs" aria-label="设置分类"><button :class="{active:settingsTab==='general'}" :aria-current="settingsTab==='general'?'page':undefined" :disabled="blocked" @click="chooseSettingsTab('general')">常规</button><button :class="{active:settingsTab==='proxy'}" :aria-current="settingsTab==='proxy'?'page':undefined" :disabled="blocked" @click="chooseSettingsTab('proxy')">网盘代理</button><button :class="{active:settingsTab==='cache'}" :aria-current="settingsTab==='cache'?'page':undefined" :disabled="blocked" @click="chooseSettingsTab('cache')">缓存</button><button :class="{active:settingsTab==='transfer'}" :aria-current="settingsTab==='transfer'?'page':undefined" :disabled="blocked" @click="chooseSettingsTab('transfer')">备份与迁移</button><button :class="{active:settingsTab==='about'}" :aria-current="settingsTab==='about'?'page':undefined" :disabled="blocked" @click="chooseSettingsTab('about')">关于与更新</button></nav><div class="settings-scroll">
      <template v-if="settingsTab==='general'">
        <section class="setting-section" aria-labelledby="startup-heading">
          <div class="setting-heading"><h2 id="startup-heading">启动</h2><button :disabled="blocked||!loginStartupData?.available" @click="saveLoginStartup">保存启动设置</button></div>
          <label class="check-row setting-option"><input v-model="loginStartupEnabled" type="checkbox" :disabled="blocked||!loginStartupData?.available">登录 Windows 后启动 WebDAVMount</label>
          <p v-if="loginStartupData?.warning" class="setting-warning" role="alert">{{loginStartupData.warning}}</p>
        </section>
        <section class="setting-section" aria-labelledby="mount-recovery-heading">
          <div class="setting-heading"><h2 id="mount-recovery-heading">挂载恢复</h2><button :disabled="blocked" @click="recover">恢复挂载…</button></div>
          <div class="setting-option">
            <label class="check-row"><input v-model="autoRecoveryEnabled" type="checkbox" :disabled="blocked">挂载服务异常后自动恢复</label>
            <button :disabled="blocked" @click="saveAutoRecovery">保存恢复设置</button>
          </div>
          <p v-if="autoRecoveryState.warning" class="setting-warning" role="alert">{{autoRecoveryState.warning}}</p>
          <p v-if="autoRecoveryState.enabled" class="field-help" role="status">{{autoRecoveryState.message}}</p>
        </section>
        <section class="setting-section" aria-labelledby="monitoring-heading">
          <div class="setting-heading"><h2 id="monitoring-heading">监测</h2><button :disabled="blocked" @click="saveMaintenance">保存监测设置</button></div>
          <label class="check-row setting-option"><input type="checkbox" v-model="maintenanceSettings.remoteChecks" :disabled="blocked">检查已挂载网盘的访问状态</label>
          <label class="check-row setting-option"><input type="checkbox" v-model="maintenanceSettings.engineWatchdog" :disabled="blocked"><span>挂载服务无响应时重启<small v-if="maintenanceSettings.engineWatchdog">{{autoRecoveryState.enabled?'可写会话期间暂停自动重启。':'请先启用并保存自动恢复。'}}</small></span></label>
          <p v-if="maintenance.warning" class="setting-warning" role="alert">{{maintenance.warning}}</p>
          <p v-if="maintenance.watchdogMessage&&maintenanceSettings.engineWatchdog" class="field-help">{{maintenance.watchdogMessage}}</p>
        </section>
      </template>
      <NetworkProxySettings v-else-if="settingsTab==='proxy'" :blocked="blocked" :api="api" :run="task" @notice="notice=$event"/>
      <template v-else-if="settingsTab==='cache'"><section class="setting-section"><div class="setting-heading"><h2>缓存位置与容量</h2><button v-if="mountedCount" :disabled="blocked" @click="batchConnections(false)">卸载全部网盘</button></div><p v-if="mountedCount" class="field-help">修改或清理缓存前须卸载全部网盘。</p><div v-if="pendingMigration?.available" class="migration-status" role="status"><strong>有未完成的缓存迁移</strong><p v-if="pendingMigration.warning" class="setting-warning">{{pendingMigration.warning}}</p><template v-else><p>原位置</p><p class="selectable-path">{{pendingMigration.source}}</p><p>目标位置</p><p class="selectable-path">{{pendingMigration.target}}</p><p>{{pendingMigration.files}} 个文件，{{fileSize(pendingMigration.bytes)}}（逻辑大小）。继续时会重新校验完成的文件。</p></template><p class="field-help">恢复迁移前请卸载全部网盘；如果原缓存已变化，请放弃任务后重新迁移。</p><div class="history-actions"><button class="primary" :disabled="blocked||mountStateUnknown||mountedCount>0||!!pendingMigration.warning" @click="migrateCache(true)">继续迁移</button><button :disabled="blocked" @click="discardMigrationConfirm=true;error=''">放弃任务…</button></div></div><form class="cache-form" @submit.prevent="saveCache"><label>缓存保存位置<div class="path-picker"><input v-model="cacheForm.baseDir" placeholder="默认位置" :disabled="blocked"><button type="button" :disabled="blocked" @click="chooseCacheDirectory">浏览…</button></div></label><div class="field-help"><button type="button" class="text-button" :disabled="blocked" @click="cacheForm.baseDir=''">恢复默认位置</button></div><label>缓存容量目标<div class="capacity-input"><input v-model="cacheForm.maxGiB" type="number" min="1" max="1024" step="1" required :disabled="blocked"><span>GiB</span></div></label><div class="cache-save-actions"><button class="primary" :disabled="blocked||mountStateUnknown||pendingMigration?.available">保存并应用</button><button type="button" :disabled="blocked||mountStateUnknown||mountedCount>0||pendingMigration?.available" @click="askMigrateCache">迁移现有缓存并应用…</button></div></form><div v-if="migrationProgress" class="migration-status" role="status"><strong>{{migrationProgress.message}}</strong><p v-if="migrationProgress.historyWarning" class="setting-warning">{{migrationProgress.historyWarning}}</p><p v-if="migrationProgress.reusedFiles">已校验复用 {{migrationProgress.reusedFiles}} 个文件，{{fileSize(migrationProgress.reusedBytes||0)}}；未重复复制。</p><p v-if="migrationActive">{{migrationProgress.files||0}} / {{migrationProgress.totalFiles||0}} 个文件，{{fileSize(migrationProgress.bytes||0)}} / {{fileSize(migrationProgress.totalBytes||0)}}（逻辑大小）</p><template v-if="migrationProgress.phase==='done'"><p>原缓存仍保留在：</p><p class="selectable-path">{{migrationProgress.source}}</p></template><template v-if="migrationProgress.retainedDirectory&&!migrationActive"><p>未启用的副本保留在下方位置，请勿当作有效缓存使用：</p><p class="selectable-path">{{migrationProgress.retainedDirectory}}</p></template></div></section><section class="setting-section"><div class="setting-heading"><h2>当前缓存</h2><button :disabled="blocked" @click="task(loadCache,true)"><UiIcon name="refresh" :size="16"/>刷新统计</button></div><p v-if="cacheData?.warning&&!cacheMigrationFallback" class="setting-warning">{{cacheData.warning}}</p><p class="selectable-path">{{cacheData?.directory||'正在读取…'}}</p><div class="cache-metrics"><div><span>文件逻辑大小</span><strong>{{fileSize(cacheData?.logicalBytes??0)}}</strong><small>{{cacheData?.files??0}} 个文件</small></div><div><span>磁盘占用（估算）</span><strong>{{cacheData?.allocatedBytes==null?'暂不可用':fileSize(cacheData.allocatedBytes)}}</strong></div></div><p v-if="cacheData?.allocationWarning" class="page-hint">{{cacheData.allocationWarning}}</p><p class="field-help">本机缓存未额外加密。</p><button class="danger-text" :disabled="blocked||mountStateUnknown||!cacheData||pendingMigration?.available" @click="error='';cacheConfirm=true">清理文件缓存…</button></section><CacheCleanupSettings :api="api" :run="task" :blocked="blocked||mountStateUnknown" :mounted="mountedCount"/><section class="setting-section"><div class="setting-heading"><h2>保留的缓存目录</h2><button :disabled="blocked" @click="task(loadCacheHistory)">刷新记录</button></div><button :disabled="blocked" @click="registerCache">登记已有缓存目录…</button><p v-if="cacheHistory.warning" class="setting-warning">{{cacheHistory.warning}}</p><p v-if="!cacheHistory.entries.length" class="page-hint">暂无目录记录。</p><ul v-else class="cache-history-list"><li v-for="entry in cacheHistory.entries" :key="entry.id"><div class="history-heading"><strong>{{cacheHistoryKinds[entry.kind]||'保留目录'}}</strong><span v-if="entry.current" class="status-tag mounted">当前使用</span><small>{{eventTime(entry.recordedAt)}}</small></div><p class="selectable-path">{{entry.path}}</p><p v-if="cacheHistoryStats[entry.id]?.error" class="setting-warning">{{cacheHistoryStats[entry.id].error}}</p><template v-else-if="cacheHistoryStats[entry.id]"><p class="page-hint">上次统计：{{eventTime(cacheHistoryStats[entry.id].scannedAt)}}<br>{{cacheHistoryStats[entry.id].files}} 个文件，逻辑大小 {{fileSize(cacheHistoryStats[entry.id].logicalBytes)}}，磁盘占用 {{cacheHistoryStats[entry.id].allocatedBytes==null?'暂不可用':fileSize(cacheHistoryStats[entry.id].allocatedBytes)}}</p><p v-if="cacheHistoryStats[entry.id].warning" class="page-hint">{{cacheHistoryStats[entry.id].warning}}</p></template><p v-else class="page-hint">尚未统计占用</p><div class="history-actions"><button :disabled="blocked" @click="scanCacheHistory(entry.id)">统计占用</button><button :disabled="blocked" @click="openCacheHistory(entry.id)">打开目录</button><button class="danger-text" :disabled="blocked||mountStateUnknown||mountedCount>0||entry.current" @click="clearRetainedRecord=entry;error=''">清理旧缓存…</button><button class="danger-text" :disabled="blocked" @click="forgetCacheRecord=entry;error=''">移除记录…</button></div></li></ul></section>
</template>
      <ConnectionTransfer v-else-if="settingsTab==='transfer'" :profiles="profiles" :blocked="blocked" :api="api" :run="task" :refresh="async()=>{await refresh();await loadMountPresets()}" @notice="notice=$event"/>
      <template v-else-if="settingsTab==='about'">
        <section class="setting-section"><div class="setting-heading"><h2>关于 WebDAVMount</h2><button :disabled="blocked" @click="task(refreshReleaseSettings)">刷新状态</button></div><dl class="setting-details"><dt>版本</dt><dd>{{updateState.currentVersion||clientVersion||'—'}}</dd><template v-if="loginStartupData?.executable"><dt>程序位置</dt><dd class="selectable-path">{{loginStartupData.executable}}</dd></template></dl></section>
        <section class="setting-section"><h2>程序更新</h2><p class="page-hint" role="status">{{updateView.message}}</p><p v-if="updateState.availableVersion" class="page-hint">可用版本：{{updateState.availableVersion}}</p><div v-if="downloadView.visible" ref="updateProgressPanel" class="update-download" aria-label="更新包下载进度">
  <div class="update-download-heading"><strong>{{updateState.state==='verifying'?'正在校验更新包':'正在下载更新包'}}</strong><span>{{downloadView.percent.toFixed(1)}}%</span></div>
  <progress :value="downloadView.percent" max="100" aria-label="下载完成百分比"></progress>
  <div class="update-download-metrics"><span>{{downloadView.size}}</span><span>{{downloadView.speed}}</span><span>已用 {{downloadView.elapsed}}</span><span>剩余 {{downloadView.remaining}}</span></div>
  <p class="update-transfer-detail"><span>{{downloadView.mode}}</span><span v-if="downloadView.resumed">本次复用 {{downloadView.resumed}}</span><span v-if="downloadView.retries">{{downloadView.retries}}</span></p>
  <div class="update-download-foot"><small>暂停会保留已完成分段；完整校验通过后才保存安装包。</small><button v-if="canCancel" @click="cancel">暂停下载</button></div>
</div>
<div class="setting-actions release-actions"><button :disabled="blocked||!updateView.canCheck" @click="releaseAction('check')">检查更新</button><button class="primary" :disabled="blocked||!updateView.canDownload" @click="releaseAction('download')">{{updateState.resumeAvailable?'继续下载并安装':updateState.state==='error'?'重试下载并安装':'下载并安装更新'}}</button><button :disabled="blocked||!updateState.configured" @click="clearUpdateDownloadCache">清理下载缓存</button></div><p v-if="updateState.resumeAvailable" class="page-hint">已保留 {{downloadView.resumed||downloadView.size}} 的下载分段，继续时会校验后复用。可重新选择保存位置。</p><button v-if="updateState.downloadedPath&&updateState.state==='downloaded'" class="primary" :disabled="blocked" @click="retryInstallUpdate">重试打开安装程序并退出</button><template v-if="updateState.downloadedPath"><p class="page-hint">更新包保存位置</p><p class="selectable-path">{{updateState.downloadedPath}}</p><p class="page-hint">校验通过后自动打开安装向导并退出客户端，停止所有挂载和传输。安装步骤由你确认。</p></template></section>
      </template>
    </div></section>

    <footer class="status-bar" :class="{'status-bar-idle':!notice&&!batchReport&&!recoveryItems.length}"><span :title="notice" role="status">{{notice}}</span><button v-if="batchReport" class="text-button result-link" :disabled="busy" @click="batchModal=true">查看批量结果</button><button v-if="recoveryItems.length" class="text-button result-link" :disabled="busy" @click="recover">查看恢复结果</button></footer>
  </main>

  <div v-if="editCopyDialog" class="overlay" @click.self="closeDialog">
    <section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="edit-copy-title">
      <div class="dialog-header"><h2 id="edit-copy-title">WebDAV 编辑副本</h2><button class="icon-button" aria-label="关闭编辑副本" :disabled="busy" @click="closeDialog"><UiIcon name="close"/></button></div>
      <div class="dialog-body">
        <template v-if="editCopy">
          <p>远端文件 <strong>{{editCopy.remote}}</strong></p>
          <p>本机副本</p><p class="selectable-path">{{editCopy.local}}</p>
          <button :disabled="blocked" @click="task(async()=>{await api('OpenEditCopyFolder')})">打开文件夹</button>
          <p>修改本机文件并关闭编辑器，再提交修改。提交前会在远端同目录创建并清理一个小型验证文件。远端版本有变化时，本机副本会保留。</p>
        </template>
        <p v-else-if="editCopyWarning">{{editCopyWarning}}</p>
        <p v-if="confirmForgetEdit">结束记录后，本机文件仍可打开。此记录将无法继续提交。</p>
        <p v-if="editCopyOperation" role="status">{{editCopyOperation}}</p>
        <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
      </div>
      <div class="dialog-footer">
        <button :disabled="busy" @click="closeDialog">稍后处理</button>
        <button v-if="confirmForgetEdit" class="danger-text" :disabled="blocked" @click="forgetEditCopy">确认结束记录</button>
        <template v-else>
          <button :disabled="blocked" @click="confirmForgetEdit=true">结束记录…</button>
          <button v-if="editCopy" class="primary" :disabled="blocked" @click="submitEditCopy">提交修改</button>
        </template>
      </div>
    </section>
  </div>



  <div v-if="s3ReviewOpen" class="overlay" @click.self="closeDialog">
    <section class="dialog" role="dialog" aria-modal="true" aria-labelledby="s3-review-title">
      <div class="dialog-header"><h2 id="s3-review-title">{{s3DeleteMode?'核对 S3 删除':'核对 S3 写回'}}</h2></div>
      <div class="dialog-body">
        <p v-if="s3ReviewQueue?.total===0" role="status">没有待核对的文件。</p>
        <ul v-if="s3ReviewQueue?.paths?.length" class="batch-result-list writeback-file-list">
          <li v-for="name in s3ReviewQueue.paths" :key="name">
            <div><strong :title="name">{{name}}</strong><button :disabled="blocked" @click="inspectS3Write(name)">核对</button></div>
          </li>
        </ul>
        <p v-if="s3ReviewResult" role="status"><strong>{{s3ReviewResult.path}}</strong> {{s3ReviewMessages[s3ReviewResult.state]||'暂时无法核对。'}}</p>
        <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
      </div>
      <div class="dialog-footer">
        <button :disabled="busy" @click="closeDialog">关闭</button>
        <button v-if="!s3DeleteMode" :disabled="blocked" @click="closeDialog();exportWritableCache()">导出写回副本…</button>
        <button v-if="s3DeleteMode&&!s3ReviewResult?.recordID" :disabled="blocked" @click="inspectS3Delete()">重新核对</button>
        <button v-if="s3ReviewResult?.recordID" class="primary" :disabled="blocked" @click="s3DeleteMode?inspectS3Delete(s3ReviewResult.recordID):acknowledgeS3Write()">完成核对</button>
      </div>
    </section>
  </div>

  <div v-if="directoryReviewOpen" class="overlay" @click.self="closeDialog">
    <section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="directory-review-title">
      <div class="dialog-header"><h2 id="directory-review-title">核对目录操作</h2></div>
      <div class="dialog-body">
        <template v-if="directoryReview">
          <p v-if="directoryReview.source">源目录 <strong class="selectable-path">{{directoryReview.source}}</strong></p>
          <p v-if="directoryReview.target">目标目录 <strong class="selectable-path">{{directoryReview.target}}</strong></p>
          <p role="status">{{directoryReviewMessages[directoryReview.state]||'暂时无法确认远端结果。'}}</p>
        </template>
        <p v-else-if="busy" role="status">正在核对远端状态…</p>
        <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
      </div>
      <div class="dialog-footer">
        <button :disabled="busy" @click="closeDialog">关闭</button>
        <button :disabled="blocked" @click="checkDirectoryReview">重新核对</button>
        <button v-if="directoryReview?.recordID" class="primary" :disabled="blocked" @click="acknowledgeDirectoryReview">完成核对</button>
      </div>
    </section>
  </div>

  <div v-if="writableRemountDialog" class="overlay" @click.self="closeDialog">
    <section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="writable-remount-title">
      <div class="dialog-header"><h2 id="writable-remount-title">重新挂载可写网盘</h2></div>
      <div class="dialog-body">
        <p>请先在服务商网页核对相关文件，再关闭此盘符中正在使用的文件。</p>
        <p>重新挂载会解除本次文件操作的限制。仍有待上传文件时，客户端会保留当前挂载。</p>
        <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
      </div>
      <div class="dialog-footer"><button :disabled="busy" @click="closeDialog">返回</button><button class="primary" :disabled="blocked" @click="confirmWritableRemount">已核对，重新挂载</button></div>
    </section>
  </div>
  <div v-if="writablePreviewOpen" class="overlay" @click.self="closeDialog">
    <section class="dialog batch-dialog" role="dialog" aria-modal="true" aria-labelledby="writable-preview-title">
      <div class="dialog-header"><h2 id="writable-preview-title">待写回文件</h2></div>
      <div class="dialog-body" :aria-busy="busy">
        <p class="dialog-intro">根据本机缓存检查。正在编辑或上传的文件状态可能变化，可刷新复查。</p>
        <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
        <p v-else-if="busy" role="status">正在检查写回缓存…</p>
        <template v-else-if="writablePreview">
          <p v-if="!writablePreviewRows.total" role="status">当前未发现待写回文件。</p>
          <template v-else>
            <p role="status">可导出 {{writablePreview.ready}} 项，需核对 {{writablePreview.skipped}} 项</p>
            <ul class="batch-result-list writeback-file-list">
              <li v-for="item in writablePreviewRows.items" :key="item.path">
                <div><strong :title="item.path">{{item.path}}</strong><b :class="{'danger-text':item.reason}">{{item.reason?'需核对':'可导出'}}</b></div>
                <p v-if="item.reason">{{item.reason}}</p>
              </li>
            </ul>
            <div v-if="writablePreviewRows.pages>1" class="writeback-file-pages">
              <button :disabled="writablePreviewRows.page<=1" @click="writablePreviewPage=writablePreviewRows.page-1">上一页</button>
              <span>{{writablePreviewRows.start}}–{{writablePreviewRows.end}} / {{writablePreviewRows.total}}</span>
              <button :disabled="writablePreviewRows.page>=writablePreviewRows.pages" @click="writablePreviewPage=writablePreviewRows.page+1">下一页</button>
            </div>
          </template>
        </template>
      </div>
      <div class="dialog-footer">
        <button :disabled="blocked" @click="showWritablePreview">刷新</button>
        <button v-if="writablePreviewRows.total" class="primary" :disabled="blocked" @click="closeDialog();exportWritableCache()">导出写回副本…</button>
        <button :disabled="busy" @click="closeDialog">关闭</button>
      </div>
    </section>
  </div>
  <div v-if="writableArchiveDialog" class="overlay" @click.self="closeDialog">
    <section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="writable-archive-title">
      <div class="dialog-header"><h2 id="writable-archive-title">保存副本并结束写回</h2></div>
      <div class="dialog-body">
        <p>请先关闭编辑器。客户端会卸载这个可写盘符，保存并复核本机 ZIP，然后结束这批写回。</p>
        <p>完整文件会放进 ZIP，跳过的项目会列在恢复说明里。原缓存保留在本机，之后可以重新挂载网盘。</p>
        <p>保存或复核失败时，写回会话仍可恢复。</p>
      </div>
      <div class="dialog-footer"><button @click="closeDialog">取消</button><button class="primary" @click="archiveWritableCache">选择 ZIP 位置…</button></div>
    </section>
  </div>
  <div v-if="writableDialog" class="overlay" @click.self="closeDialog">
    <section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="writable-title">
      <div class="dialog-header"><h2 id="writable-title">{{reviewAfterMount?'挂载并核对目录操作':writableRecovery?'恢复可写挂载':'可写挂载'}}</h2></div>
      <div class="dialog-body">
        <p>“{{active?.name}}”将以可写方式挂载到 {{active?.drive}}。支持新建和修改文件。直接删除暂受限制。</p>
        <p v-if="s3WritableCandidate">本机 S3 测试挂载支持新建与修改。结果待确认时，可核对远端或导出本机副本。</p>
        <p v-else>文件关闭后会在后台核对服务器的条件写入能力。检测到写回冲突时，可导出本机副本。</p>
        <p v-if="!s3WritableCandidate">支持同目录单文件改名，服务端需通过条件移动检查。同名目标、正在使用或待写回的文件会阻止改名。</p>
        <p>卸载前请关闭编辑器并等待上传完成。恢复时会继续使用编辑前记录的版本。缺少版本的旧缓存可先导出。</p>
        <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
      </div>
      <div class="dialog-footer"><button :disabled="busy" @click="closeDialog">取消</button><button class="primary" :disabled="blocked||(!engineOnline&&!writableRecovery)" @click="confirmWritable">{{writableRecovery?'恢复可写挂载':'确认可写挂载'}}</button></div>
    </section>
  </div>
  <div v-if="modal" class="overlay"><form ref="formElement" class="dialog connection-dialog" role="dialog" aria-modal="true" aria-labelledby="connection-title" @submit.prevent="save(!form.id)"><div class="dialog-header"><h2 id="connection-title">{{form.id?'编辑网盘':'添加网盘'}}</h2><button type="button" class="icon-button" aria-label="关闭网盘窗口" :disabled="busy" @click="closeDialog"><UiIcon name="close"/></button></div><div class="dialog-body"><label>连接类型<select :value="formProvider" :disabled="busy||!!form.id" @change="changeProvider"><option v-for="(option,key) in storageProviders" :key="key" :value="key">{{option.label}}</option></select></label><p class="dialog-intro">{{providerFields.help}}</p><div class="form-columns"><label>网盘名称<input v-model="form.name" required maxlength="80" placeholder="例如：123云盘" :disabled="busy"></label><label>挂载盘符
  <select v-model="form.drive" :disabled="busy" required :aria-describedby="chosenDrive?.occupied||chosenDrive?.configured?'drive-selection-help':undefined">
    <option value="" disabled>请选择盘符</option>
    <option v-for="option in driveOptions" :key="option.drive" :value="option.drive">
      {{option.drive}}{{option.occupied?'（系统占用）':option.configured?'（已有配置）':''}}
    </option>
  </select>
</label></div>
<p v-if="chosenDrive?.occupied" id="drive-selection-help" class="field-help" role="status">此盘符已被 Windows 占用。可保存配置，释放盘符后再挂载。</p>
<p v-else-if="chosenDrive?.configured" id="drive-selection-help" class="field-help" role="status">另有网盘配置使用此盘符，同一时间只能挂载一个。</p>
<template v-if="oauthForm">
  <div v-if="!cloudStep">
    <label v-if="formProvider==='sharepoint'">SharePoint 站点地址<input v-model="cloudSite" type="url" placeholder="https://contoso.sharepoint.com/sites/team" :disabled="busy"></label>
    <template v-if="googleForm">
      <p class="field-help">在 Google Cloud 启用 Drive API，在 OAuth 客户端中选择「桌面应用」。使用组织账号时，由组织管理员批准应用及访问所选共享盘。</p>
      <label>Google 桌面应用 Client ID<input v-model="cloudClientID" autocomplete="off" :disabled="busy"></label>
      <label>Google 桌面应用 Client Secret<input v-model="cloudClientSecret" type="password" autocomplete="new-password" :disabled="busy"></label>
    </template>
    <p v-if="formProvider==='microsoft365'||formProvider==='sharepoint'" class="field-help">请使用组织的工作或学校账号。若登录页要求管理员批准，完成组织审批后重新授权。</p>
    <button type="button" :disabled="busy||(googleForm&&(!cloudClientID.trim()||!cloudClientSecret.trim()))||(formProvider==='sharepoint'&&!cloudSite.trim())" @click="authorizeCloud">{{form.id?'重新授权':'打开浏览器授权'}}</button>
  </div>
  <template v-else-if="!cloudStep.ready">
    <label v-if="(cloudStep.choices?.length||0)>5">搜索{{formProvider==='sharepoint'?'文档库':'驱动器'}}<input v-model="cloudFilter" :disabled="busy"></label>
    <label>{{formProvider==='sharepoint'?'选择文档库':formProvider==='workspace'?'选择共享云端硬盘':'选择驱动器'}}<select v-model="cloudChoice" :disabled="busy"><option value="">请选择</option><option v-for="item in filteredCloudChoices" :key="item.value" :value="item.value">{{item.title||item.value}}</option></select></label>
    <p v-if="!filteredCloudChoices.length" class="field-help">未找到匹配项。</p>
    <button type="button" :disabled="busy||!cloudChoice" @click="authorizeCloud">确认选择</button>
    <button type="button" :disabled="busy" @click="clearCloudForm">重新授权</button>
  </template>
  <p v-else role="status">授权完成，请保存连接。</p>
  <button v-if="authorizingCloud" type="button" @click="cancelTask(()=>api('CancelOperation'))">取消授权</button>
</template>
<template v-else><label v-if="formProvider!=='mega'">{{objectStorageForm?'服务地址（含桶或容器名）':'服务器地址'}}<input v-model="form.url" required type="url" :placeholder="providerFields.placeholder" :disabled="busy"></label>
<label v-if="formProvider==='aws'||formProvider==='azure'">认证方式<select v-model="storageOptions.mode" :disabled="busy" @change="form.password='';storageOptions.sessionToken=''">
<option value="">{{formProvider==='azure'?'存储账户密钥':'访问密钥'}}</option>
<option v-if="formProvider==='aws'" value="session">AWS 临时会话凭据</option>
<option v-if="formProvider==='azure'" value="sas">容器 SAS</option>
<option v-if="formProvider==='azure'" value="entra">Entra 应用服务主体</option>
</select></label>
<label v-if="formProvider==='aws'||formProvider==='minio'">自定义端点区域（选填）<input v-model.trim="storageOptions.region" maxlength="63" placeholder="例如：us-east-1" :disabled="busy"></label>
<template v-if="formProvider==='azure'&&storageOptions.mode==='entra'">
  <label>租户 ID<input v-model.trim="storageOptions.tenant" maxlength="36" required :disabled="busy"></label>
  <label>应用（客户端）ID<input v-model.trim="storageOptions.clientId" maxlength="36" required :disabled="busy"></label>
</template>
<label>{{formProvider==='azure'?'存储账户名':objectStorageForm?'访问密钥 ID':formProvider==='mega'?'MEGA 邮箱':'用户名'}}<input v-model="form.user" autocomplete="username" :required="objectStorageForm||formProvider==='mega'||formProvider==='sftp'" :disabled="busy"></label><label v-if="formProvider==='sftp'">SSH 服务器公钥<input v-model="form.hostKey" required maxlength="8192" placeholder="ssh-ed25519 AAAA…" :disabled="busy"></label><label>{{formProvider==='azure'?(storageOptions.mode==='sas'?'容器 SAS URL 或查询字符串':storageOptions.mode==='entra'?'应用客户端密钥':'存储账户密钥'):objectStorageForm?'访问密钥 Secret':formProvider==='webdav'||formProvider==='nextcloud'?'应用密码':'密码'}}<input v-model="form.password" type="password" autocomplete="new-password" :placeholder="form.id?'留空保留已保存的密码':'填写此连接的凭据'" :disabled="busy||form.clearPassword"></label><label v-if="formProvider==='aws'&&storageOptions.mode==='session'">会话令牌<input v-model="storageOptions.sessionToken" type="password" autocomplete="new-password" maxlength="16384" :placeholder="form.id?'留空保留同一组已保存的会话凭据':''" :disabled="busy||form.clearPassword"></label>
<label v-if="form.id" class="check-row"><input v-model="form.clearPassword" type="checkbox" :disabled="busy"><span>清除已保存的凭据</span></label></template>
<label v-if="formProvider==='mega'">双重验证码（按需填写）<input v-model.trim="megaCode" type="password" minlength="6" maxlength="6" pattern="[0-9]{6}" inputmode="numeric" autocomplete="one-time-code" :disabled="busy||form.clearPassword"></label>
<details v-if="proxyAllowed" class="connection-proxy-form" :open="connectionProxy.mode!=='inherit'||undefined">
  <summary>此网盘的代理</summary>
  <label>连接方式<select v-model="connectionProxy.mode" :disabled="busy||!!cloudStep"><option value="inherit">跟随全局代理</option><option value="direct">直连</option><option value="manual">使用指定代理</option></select></label>
  <template v-if="connectionProxy.mode==='manual'">
    <div class="form-columns"><label>代理协议<select :value="connectionProxy.protocol||'http'" @change="connectionProxy.protocol=($event.target as HTMLSelectElement).value" :disabled="busy||!!cloudStep"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option></select></label><label>端口<input v-model.number="connectionProxy.port" type="number" min="1" max="65535" required :disabled="busy||!!cloudStep"></label></div>
    <label>代理主机<input v-model.trim="connectionProxy.host" maxlength="253" required :disabled="busy||!!cloudStep"></label>
    <div class="form-columns"><label>代理用户名<input v-model="connectionProxy.username" autocomplete="off" :disabled="busy||!!cloudStep"></label><label>代理密码<input v-model="connectionProxy.password" type="password" autocomplete="new-password" :placeholder="form.id?'留空保留相同代理的密码':''" :disabled="busy||!!cloudStep"></label></div>
  </template>
</details>
<button v-if="formProvider==='mega'" type="button" :disabled="busy" @click="probeConnectionMEGA">测试 MEGA 网络</button>
<p v-if="formProvider==='mega'&&megaNetworkMessage" class="field-help" role="status">{{megaNetworkMessage}}</p>
<label class="check-row"><input v-model="form.autoMount" type="checkbox" :disabled="busy"><span>打开程序后自动挂载此网盘</span></label><p class="field-help">连接凭据在本机加密保存。自动挂载不会设置 Windows 登录启动。</p><div v-if="error" class="dialog-error" role="alert">{{error}}</div></div><div class="dialog-footer"><button v-if="!form.id" type="button" class="save-only" :disabled="busy||(oauthForm&&!cloudStep?.ready)" @click="save(false)">仅保存</button><button type="button" :disabled="busy" @click="closeDialog">取消</button><button class="primary" :disabled="busy||(!form.id&&chosenDrive?.occupied)||(oauthForm&&!form.id&&!cloudStep?.ready)">{{busy?'正在保存…':form.id?'保存修改':'保存并挂载'}}</button></div></form></div>
  <div v-if="confirmDelete||cacheConfirm" class="overlay"><section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="confirm-title"><div class="dialog-header"><h2 id="confirm-title">{{confirmDelete?'删除网盘配置':'清理文件缓存'}}</h2></div><div class="dialog-body"><p v-if="confirmDelete">确定删除“{{active?.name}}”？只删除本机保存的连接配置和密码，网盘文件与缓存会保留。</p><p v-else>确定清理当前缓存文件及索引？网盘文件、本机连接配置和其他文件会保留，下次读取需要重新下载。</p><div v-if="error" class="dialog-error" role="alert">{{error}}</div></div><div class="dialog-footer"><button :disabled="busy" @click="closeDialog">取消</button><button class="danger" :disabled="busy" @click="confirmDelete?remove():clearCache()">{{busy?'正在处理…':confirmDelete?'删除配置':'确认清理'}}</button></div></section></div>
  <div v-if="trayDrivesOpen" class="overlay" @click.self="closeDialog">
    <section class="dialog batch-dialog" role="dialog" aria-modal="true" aria-labelledby="tray-drives-title">
      <div class="dialog-header"><h2 id="tray-drives-title">已挂载网盘</h2><button class="icon-button" :disabled="busy" aria-label="关闭已挂载网盘" @click="closeDialog"><UiIcon name="close"/></button></div>
      <div class="dialog-body">
        <p v-if="error" class="dialog-error" role="alert">{{error}}</p>
        <p v-if="!engineOnline" class="page-hint">挂载服务暂不可用。请在主窗口恢复挂载。</p>
        <p v-else-if="mountStateUnknown" class="page-hint">盘符状态待确认。</p>
        <p v-else-if="!trayMountedProfiles.length" class="page-hint">当前没有已挂载的网盘。</p>
        <ul v-else class="batch-result-list tray-drive-list">
          <li v-for="profile in trayMountedProfiles" :key="profile.id">
            <div><strong :title="profile.name">{{profile.name}}</strong><span>{{profile.drive}}</span><button :disabled="blocked" :aria-label="'在资源管理器中打开 '+profile.name" @click="openTrayDrive(profile.id)"><UiIcon name="external" :size="14"/>打开磁盘</button></div>
          </li>
        </ul>
      </div>
      <div class="dialog-footer"><button :disabled="busy" @click="closeDialog">关闭</button></div>
    </section>
  </div>
  <div v-if="presetRequest" class="overlay" @click.self="closeDialog">
    <section class="dialog batch-dialog" role="dialog" aria-modal="true" aria-labelledby="preset-preview-title">
      <div class="dialog-header">
        <h2 id="preset-preview-title">{{presetRequest.switchMode?'切换挂载方案':presetRequest.mount?'挂载方案预览':'卸载方案预览'}}</h2>
        <button ref="presetCloseButton" class="icon-button" :aria-disabled="busy" aria-label="关闭方案预览" @click="closeDialog"><UiIcon name="close"/></button>
      </div>
      <div class="dialog-body">
        <div v-if="presetRequest.switchMode&&!batchActive" class="form-columns preset-switch-fields">
          <label>原方案<select v-model="switchSource" :disabled="busy" @change="resetPresetPreview">
            <option value="">选择原方案</option>
            <option v-for="preset in mountPresets" :key="preset.id" :value="preset.id" :disabled="preset.id===presetRequest.id">{{preset.name}}</option>
          </select></label>
          <label>目标方案<select v-model="presetRequest.id" :disabled="busy" @change="resetPresetPreview">
            <option value="">选择目标方案</option>
            <option v-for="preset in mountPresets" :key="preset.id" :value="preset.id" :disabled="preset.id===switchSource">{{preset.name}}</option>
          </select></label>
        </div>
        <div v-if="error" class="dialog-error" role="alert">{{error}}</div>
        <p v-if="batchActive" role="status">{{notice||'正在处理方案中的网盘…'}}</p>
        <p v-else-if="busy" role="status">正在检查盘符…</p>
        <template v-if="presetPreview">
          <strong>{{presetPreview.name}}</strong>
          <p v-if="presetPreview.blocked" class="dialog-error" role="status">有 {{presetPreview.blocked}} 个网盘的盘符冲突。处理后请选择「重新检查」。</p>
          <p v-else class="page-hint" role="status">{{presetPreview.ready?'本次将处理 '+presetPreview.ready+' 个网盘。':'当前所有网盘均无需操作。'}}</p>
          <ul class="batch-result-list">
            <li v-for="item in presetPreview.items" :key="item.id">
              <div><strong :title="item.name">{{item.name}}</strong><span>{{item.drive}}</span><b :class="{'danger-text':item.state==='blocked'}">{{item.state==='ready'?'待处理':item.state==='blocked'?'冲突':'保持原状'}}</b></div>
              <p>{{item.message}}</p>
            </li>
          </ul>
        </template>
      </div>
      <div class="dialog-footer">
        <button v-if="batchActive&&canCancel" @click="cancel">停止后续处理</button>
        <template v-else>
          <button v-if="!presetPreview||presetPreview.blocked" :disabled="blocked||(presetRequest.switchMode&&!canPreviewSwitch)" @click="refreshPresetPreview">{{presetRequest.switchMode&&!error&&!presetPreview?'预览切换':'重新检查'}}</button>
          <button :disabled="busy" @click="closeDialog">关闭</button>
          <button v-if="presetPreview" class="primary" :disabled="blocked||!!presetPreview.blocked||!presetPreview.ready" @click="confirmPresetPreview">{{presetRequest.switchMode?'确认切换':presetRequest.mount?'确认挂载':'确认卸载'}}</button>
        </template>
      </div>
    </section>
  </div>
  <div v-if="batchModal&&batchReport" class="overlay" @click.self="closeDialog"><section class="dialog batch-dialog" role="dialog" aria-modal="true" aria-labelledby="batch-title"><div class="dialog-header"><h2 id="batch-title">批量处理结果</h2><button class="icon-button" aria-label="关闭批量结果" @click="closeDialog"><UiIcon name="close"/></button></div><div class="dialog-body"><p class="dialog-intro">取消仅停止后续处理，已完成的操作会保留。</p><ul class="batch-result-list"><li v-for="item in batchReport.items" :key="item.id"><div><strong>{{item.name}}</strong><span>{{item.drive}}</span><b :class="{'danger-text':item.state==='failed'}">{{batchStates[item.state]}}</b></div><p>{{item.message}}</p></li></ul></div><div class="dialog-footer"><button class="primary" @click="closeDialog">知道了</button></div></section></div>
</div>

  <div v-if="recoveryModal" class="overlay" @click.self="closeDialog"><RecoveryCenter :busy="blocked" :error="error" :results="recoveryItems" :api="api" :run="task" :refresh="refresh" :cancel="cancel" @close="closeDialog" @notice="notice=$event"/></div>
  <div v-if="discardMigrationConfirm" class="overlay"><section class="dialog" role="dialog" aria-modal="true" aria-labelledby="discard-migration-title"><div class="dialog-header"><h2 id="discard-migration-title">放弃这次迁移？</h2><button class="icon-button" aria-label="关闭放弃迁移确认" @click="closeDialog"><UiIcon name="close"/></button></div><div class="dialog-body"><p>只移除任务记录。原缓存和已经复制的文件均保留，不会删除或自动启用副本。</p><p>放弃后不能继续这次迁移；需要时可重新选择其他目标位置。</p><p v-if="pendingMigration?.stage" class="selectable-path">{{pendingMigration.stage}}</p></div><div class="dialog-footer"><button @click="closeDialog">返回</button><button class="danger-text" @click="discardMigration">放弃任务并保留文件</button></div></section></div>
  <div v-if="cacheMigrationConfirm&&migrationPreview" class="overlay"><section class="dialog batch-dialog" role="dialog" aria-modal="true" aria-labelledby="migration-title"><div class="dialog-header"><h2 id="migration-title">迁移现有缓存</h2><button class="icon-button" aria-label="关闭缓存迁移确认" @click="closeDialog"><UiIcon name="close"/></button></div><div class="dialog-body"><p class="page-hint">从当前缓存位置复制到新位置，逐文件校验成功后切换。原缓存保留，目标已有目录不会覆盖或合并。</p><p class="page-hint">原位置</p><p class="selectable-path">{{migrationPreview.source}}</p><p class="page-hint">新位置</p><p class="selectable-path">{{migrationPreview.target}}</p><p class="page-hint">复制和校验阶段可暂停；切换阶段请等待完成。暂停或退出会保留任务和副本，重开程序后可在此继续。已完成文件会重新校验后复用，未完成的单个文件重新复制。原缓存发生变化时会停止续迁。稀疏文件按逻辑长度校验，较大文件可能需要更多时间。</p></div><div class="dialog-footer"><button @click="closeDialog">取消</button><button class="primary" @click="migrateCache(false)">开始迁移</button></div></section></div>
  <div v-if="registerCacheDialog" class="overlay"><form class="dialog batch-dialog" role="dialog" aria-modal="true" aria-labelledby="history-register-title" @submit.prevent="rememberCache"><div class="dialog-header"><h2 id="history-register-title">登记已有缓存目录</h2><button type="button" class="icon-button" aria-label="关闭目录登记" :disabled="busy" @click="closeDialog"><UiIcon name="close"/></button></div><div class="dialog-body"><p class="page-hint">选择本程序的旧缓存专用目录本身，或未完成的迁移副本。不要选择其父目录。登记只保存目录位置，不会移动、启用或删除文件。</p><label>目录路径<div class="path-picker"><input v-model="historyDirectory" required placeholder="旧缓存专用目录的完整路径" :disabled="busy"><button type="button" :disabled="busy" @click="chooseHistoryDirectory">浏览…</button></div></label><div v-if="error" class="dialog-error" role="alert">{{error}}</div></div><div class="dialog-footer"><button type="button" :disabled="busy" @click="closeDialog">取消</button><button class="primary" :disabled="busy">登记目录</button></div></form></div>
    <div v-if="clearRetainedRecord" class="overlay"><section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="retained-clear-title"><div class="dialog-header"><h2 id="retained-clear-title">清理这处旧缓存？</h2></div><div class="dialog-body"><p>仅删除下列目录内的 vfs 和 vfsMeta 缓存内容，不删除远端文件。目录记录、该目录本身和其他文件保留。</p><p class="selectable-path">{{clearRetainedRecord.path}}</p><p>请先卸载全部网盘。开始清理后无法撤销；文件占用时可能仅清理一部分，可稍后重试。当前使用的缓存及重叠位置不能在此清理。</p><div v-if="error" class="dialog-error" role="alert">{{error}}</div></div><div class="dialog-footer"><button :disabled="busy" @click="closeDialog">取消</button><button class="danger" :disabled="busy" @click="clearRetainedCache">确认清理旧缓存</button></div></section></div><div v-if="forgetCacheRecord" class="overlay"><section class="dialog confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="history-forget-title"><div class="dialog-header"><h2 id="history-forget-title">移除目录记录</h2></div><div class="dialog-body"><p>仅从列表移除此条记录，目录内文件和当前缓存设置都会保留。</p><p class="selectable-path">{{forgetCacheRecord.path}}</p><div v-if="error" class="dialog-error" role="alert">{{error}}</div></div><div class="dialog-footer"><button :disabled="busy" @click="closeDialog">取消</button><button class="primary" :disabled="busy" @click="forgetCacheHistory">仅移除记录</button></div></section></div>
</template>
