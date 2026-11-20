type MountedConnection={ProfileID?:string,MountPoint?:string}
type Connection={id:string,drive:string}
export function isProfileMounted(mounts:readonly MountedConnection[],profile:Connection|undefined):boolean {
  return !!profile&&mounts.some(m=>m.ProfileID===profile.id&&m.MountPoint?.toUpperCase()===profile.drive.toUpperCase())
}
