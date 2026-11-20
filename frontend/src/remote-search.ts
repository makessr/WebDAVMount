export type SearchItem={Path:string,Name:string,IsDir:boolean,Size:number,ModTime?:string}
export type SearchState={active:boolean,phase:string,message:string,profileId?:string,base?:string,query?:string,directories?:number,scanned?:number,matched?:number,failedDirectories?:number,skipped?:number,limit?:string,items?:SearchItem[]}
export const emptySearch=():SearchState=>({active:false,phase:'idle',message:'输入文件名关键词后开始搜索。'})
export const searchPageSize=50
export function searchPage<T>(items:readonly T[],requested:number){
 const pages=Math.max(1,Math.ceil(items.length/searchPageSize)),page=Math.max(1,Math.min(pages,Number.isFinite(requested)?Math.trunc(requested):1))
 return {page,pages,total:items.length,items:items.slice((page-1)*searchPageSize,page*searchPageSize)}
}
export function searchLocation(item:SearchItem):{directory:string,selectedPath:string}|undefined{
 const value=item.Path
 if(!value||value.length>4096||value.startsWith('/')||value.includes('\\')||/[\x00-\x1f\x7f]/.test(value)||value.split('/').some(p=>!p||p==='.'||p==='..'))return
 return item.IsDir?{directory:value,selectedPath:''}:{directory:value.includes('/')?value.slice(0,value.lastIndexOf('/')):'',selectedPath:value}
}
export function searchComplete(state:SearchState){return state.phase==='done'}
