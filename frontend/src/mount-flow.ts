import {errorMessage} from './operation-feedback.ts'

// A directory error must not erase the successful mount result.
export async function mountAndRead(mount:()=>Promise<unknown>,read:()=>Promise<void>) {
  await mount()
  try {await read();return {directoryLoaded:true,error:null,cancelled:false}}
  catch(error){return {directoryLoaded:false,error,cancelled:errorMessage(error)==='操作已取消'}}
}
