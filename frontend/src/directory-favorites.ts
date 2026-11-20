export type DirectoryFavorite={id:string,name:string,path:string}
export function favoriteName(path:string){return path?path.split('/').at(-1)||'常用目录':'根目录'}
export function favoriteDirectionAllowed(index:number,length:number,direction:-1|1){return index>=0&&index<length&&index+direction>=0&&index+direction<length}
