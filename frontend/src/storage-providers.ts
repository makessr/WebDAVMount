export const storageProviders = {
  webdav: {label: 'WebDAV', placeholder: 'https://dav.example.com/dav/', help: '填写服务商给出的 WebDAV 地址及应用密码。'},
  nextcloud: {label: 'Nextcloud', placeholder: 'https://cloud.example.com/remote.php/dav/files/用户名/', help: '从 Nextcloud 的文件设置复制完整 WebDAV 地址，建议使用应用密码。'},
  mega: {label: 'MEGA', placeholder: '', help: '使用 MEGA 账号邮箱和密码。开启双重验证时，填写验证器当前的六位验证码。'},
  tencent: {label: '腾讯云 COS', placeholder: 'https://cos.ap-guangzhou.myqcloud.com/my-bucket-APPID', help: '地址末尾填写带 APPID 的桶名。使用有该桶列表和读取权限的密钥。'},
  minio: {label: 'MinIO', placeholder: 'https://storage.example.com/my-bucket', help: '填写 S3 API 地址和桶名。当前支持兼容 us-east-1 的配置。'},
  r2: {label: 'Cloudflare R2', placeholder: 'https://账户ID.r2.cloudflarestorage.com/my-bucket', help: '使用账户的 S3 API 端点和桶名，凭据选择该桶的对象读取权限。'},
  sftp: {label: 'SFTP', placeholder: 'sftp://files.example.com:22', help: '从服务器管理员处获取并核验 SSH 服务器公钥。当前使用密码登录。'},
  ftp: {label: 'FTP / FTPS', placeholder: 'ftpes://files.example.com:21', help: '推荐 ftpes://（显式 TLS）或 ftps://（隐式 TLS）。ftp:// 会明文传输账号、密码和文件。'},
  aws: {label: 'AWS S3', placeholder: 'https://s3.us-east-1.amazonaws.com/my-bucket', help: '填写 AWS S3 区域端点和桶名。使用该桶的读取权限密钥。'},
  azure: {label: 'Azure Blob Storage', placeholder: 'https://account.blob.core.windows.net/my-container', help: '填写 Azure 公有云的容器地址、存储账户名和账户密钥。'},
  alibaba: {label: '阿里云 OSS', placeholder: 'https://oss-cn-hangzhou.aliyuncs.com/my-bucket', help: '填写 OSS 公网地域端点和桶名。使用该桶的读取权限密钥。'},
  backblaze: {label: 'Backblaze B2', placeholder: 'https://api.backblazeb2.com/my-bucket', help: '填写桶名和限定该桶的 B2 应用密钥。'},
  googledrive: {label: 'Google Drive', placeholder: '', help: '通过 Google 桌面应用凭据发起只读授权，连接个人云端硬盘。'},
  workspace: {label: 'Google Workspace', placeholder: '', help: '使用组织账号授权后选择一个 Google 共享云端硬盘。'},
  onedrive: {label: 'OneDrive', placeholder: '', help: '在浏览器登录 Microsoft 账号后选择 OneDrive 驱动器。'},
  microsoft365: {label: 'Microsoft 365', placeholder: '', help: '使用工作或学校账号登录，再选择 OneDrive for Business 驱动器。'},
  sharepoint: {label: 'SharePoint Online', placeholder: '', help: '填写站点地址。登录 Microsoft 账号后选择站点文档库。'},
  yandex: {label: 'Yandex Disk', placeholder: '', help: '在浏览器登录 Yandex 并核对授权页面权限。客户端以只读方式挂载。'},
} as const

export type StorageProvider = keyof typeof storageProviders

export function usesBrowserGrant(provider?: StorageProvider) {
  return provider !== undefined && ['googledrive','workspace','onedrive','microsoft365','sharepoint','yandex'].includes(provider)
}

export function providerLabel(provider?: StorageProvider) {
  return storageProviders[provider || 'webdav']?.label || '未知连接类型'
}
