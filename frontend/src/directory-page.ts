export const directoryPageSize = 200

export function directoryPage<T>(items:readonly T[], requested:number) {
  const total=items.length
  const pages=Math.max(1,Math.ceil(total/directoryPageSize))
  const page=Math.min(pages,Math.max(1,Number.isFinite(requested)?Math.trunc(requested):1))
  const offset=(page-1)*directoryPageSize
  return {page,pages,total,start:total?offset+1:0,end:Math.min(offset+directoryPageSize,total),items:items.slice(offset,offset+directoryPageSize)}
}
