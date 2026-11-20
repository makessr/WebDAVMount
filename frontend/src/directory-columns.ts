import type {DirectoryOrder} from './directory-view'
export type SortColumn='name'|'size'|'modified'
export function columnDirection(order:DirectoryOrder,column:SortColumn):'ascending'|'descending'|'none'{
  return order===`${column}Asc`?'ascending':order===`${column}Desc`?'descending':'none'
}
export function nextColumnOrder(order:DirectoryOrder,column:SortColumn):DirectoryOrder{
  const direction=columnDirection(order,column)
  if(direction==='ascending')return `${column}Desc`
  if(direction==='descending')return `${column}Asc`
  return column==='name'?'nameAsc':`${column}Desc`
}
