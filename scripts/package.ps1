param([string]$Version='0.72.7',[string]$ToolchainRoot=(Join-Path $env:LOCALAPPDATA 'Programs\MultiPanDev'),[string]$ArtifactDirectory,
      [ValidateSet('amd64','arm64')][string]$Architecture='amd64',
      [string]$DesktopExecutable,[string]$Engine,[string]$EngineManifest,[string]$EngineNotices)
$ErrorActionPreference='Stop'
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Invalid version'}
Set-Location (Split-Path $PSScriptRoot -Parent)
$activate=Join-Path $ToolchainRoot 'Activate.ps1';if(Test-Path $activate){. $activate}
$name="WebDAVMount-$Version-windows-$Architecture"
if(!$ArtifactDirectory){$ArtifactDirectory=Join-Path $PWD 'dist'}
$ArtifactDirectory=[IO.Path]::GetFullPath($ArtifactDirectory)
[IO.Directory]::CreateDirectory($ArtifactDirectory)|Out-Null
$stage=Join-Path $ArtifactDirectory ("staging-"+[guid]::NewGuid().ToString('N')+"\$name")
$archive=Join-Path $ArtifactDirectory ($name+'.zip')
if((Test-Path -LiteralPath $stage) -or (Test-Path -LiteralPath $archive)){throw 'Release destination already exists; preserve it or choose a new version'}
$exe=if($DesktopExecutable){[IO.Path]::GetFullPath($DesktopExecutable)}else{Join-Path $PWD "bin\WebDAVMount-$Version.exe"}
if(!$Engine){$Engine=Join-Path $PWD 'bin\rclone.exe'}
if(!(Test-Path -LiteralPath $exe)){throw 'Build the desktop release first'}
if($Architecture -ceq 'arm64'){
 if(!$EngineManifest -or !$EngineNotices){throw 'ARM64 engine provenance and notices are required'}
 $appBuild=@(go version -m $exe)
 if($LASTEXITCODE -ne 0 -or !$appBuild -or !($appBuild.Trim() -ccontains "build`tGOARCH=arm64")){throw 'ARM64 desktop executable required'}
}
New-Item -ItemType Directory -Path $stage -Force|Out-Null
Copy-Item -LiteralPath $exe -Destination (Join-Path $stage 'WebDAVMount.exe')
Copy-Item -LiteralPath $Engine -Destination (Join-Path $stage 'rclone.exe')
$readme=if($Architecture -ceq 'arm64'){'Windows-ARM64.txt'}else{'使用说明.txt'}
Copy-Item -LiteralPath (Join-Path $PWD ('build/portable/'+$readme)) -Destination (Join-Path $stage '使用说明.txt')
Copy-Item -LiteralPath (Join-Path $PWD 'build/portable/THIRD_PARTY_NOTICES.txt') -Destination $stage
Copy-Item -LiteralPath (Join-Path $PWD 'build\portable\Update-WebDAVMount.ps1') -Destination $stage
$engineHash=(Get-FileHash -LiteralPath (Join-Path $stage 'rclone.exe')).Hash
. (Join-Path $PSScriptRoot 'engine-identity.ps1')
$engineIdentity=Get-EngineIdentity (Join-Path $stage 'rclone.exe') $EngineManifest
if($engineIdentity.Kind -ceq 'nosmb' -and $engineIdentity.Architecture -cne $Architecture){throw 'Package engine architecture mismatch'}
if([version]$Version -ge [version]'0.23.0' -and $engineIdentity.Kind -ne 'nosmb'){throw 'Current releases require the verified no-SMB engine'}
if([version]$Version -ge [version]'0.67.0' -and (!($engineIdentity.PolicyFiles -contains 'WEBDAV-WRITEBACK.txt'))){throw 'Current releases require the guarded writeback engine'}
if([version]$Version -ge [version]'0.67.0' -and [version]$Version -lt [version]'0.70.0' -and 'VFS-MOVE.txt' -in $engineIdentity.PolicyFiles){throw 'This earlier release requires its original guarded writeback engine'}
if([version]$Version -ge [version]'0.70.0' -and 'VFS-MOVE.txt' -notin $engineIdentity.PolicyFiles){throw 'This release requires the protected rename engine'}
Copy-Item -LiteralPath $engineIdentity.Readme -Destination (Join-Path $stage 'RCLONE-README.txt')
if($engineIdentity.Kind -eq 'nosmb'){
 $buildFolder=Join-Path $stage 'engine-build';[IO.Directory]::CreateDirectory($buildFolder)|Out-Null
 foreach($file in $engineIdentity.ProvenanceFiles){Copy-Item -LiteralPath (Join-Path $engineIdentity.Directory $file) -Destination (Join-Path $buildFolder $file)}
}
$licenseRoot=Join-Path $stage 'licenses';New-Item -ItemType Directory -Path $licenseRoot|Out-Null
$projectLicense=Join-Path $PWD 'LICENSE'
$projectNotice=Join-Path $PWD 'NOTICE'
if ((Test-Path -LiteralPath $projectLicense -PathType Leaf) -and (Test-Path -LiteralPath $projectNotice -PathType Leaf)) {
 $projectLicenseDirectory=Join-Path $licenseRoot 'WebDAVMount'
 [IO.Directory]::CreateDirectory($projectLicenseDirectory)|Out-Null
 Copy-Item -LiteralPath $projectLicense,$projectNotice -Destination $projectLicenseDirectory
}
function Copy-Notices([string]$Source,[string]$Name){
 $items=Get-ChildItem -LiteralPath $Source -File | Where-Object Name -Match '^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS|THIRD-PARTY-LICENSES?)([._-].*)?$'
 if(!$items){throw "Missing dependency license: $Name"}
 $target=Join-Path $licenseRoot ($Name -replace '[^A-Za-z0-9.-]','_');New-Item -ItemType Directory -Path $target -Force|Out-Null
 foreach($item in $items){Copy-Item -LiteralPath $item.FullName -Destination $target}
}
$modules=go list -deps -f '{{with .Module}}{{.Path}}|{{.Dir}}{{end}}' . | Where-Object {$_ -ne ''} | Sort-Object -Unique
if($LASTEXITCODE -ne 0){throw 'Cannot enumerate Go dependency notices'}
foreach($entry in $modules){$parts=$entry.Split('|');if($parts[0] -ne 'webdavmount'){Copy-Notices $parts[1] $parts[0]}}
$goRoot=go env GOROOT
Copy-Notices $goRoot 'Go-standard-library'
. (Join-Path $PSScriptRoot 'frontend-notices.ps1')
$frontendNotices=Get-FrontendNoticeInputs $PWD $exe $Version
if($frontendNotices.mode -eq 'bundle-inventory'){
 $frontendIndex=Join-Path $licenseRoot 'frontend-index';[IO.Directory]::CreateDirectory($frontendIndex)|Out-Null
 Copy-Item -LiteralPath $frontendNotices.receipt -Destination (Join-Path $frontendIndex 'NOTICE.json')
}
foreach($entry in $frontendNotices.packages){
 if($entry.Key -eq 'node_modules/@wailsio/runtime'){
  $upstream=go list -m -f '{{.Dir}}' github.com/wailsapp/wails/v3
  if($LASTEXITCODE -ne 0){throw 'Cannot locate Wails runtime source license'}
  Copy-Notices $upstream ('npm_'+$entry.Key)
 }else{Copy-Notices (Join-Path $PWD ('frontend\'+$entry.Key)) ('npm_'+$entry.Key)}
}
if(!$engineNotices){$engineNotices=$engineIdentity.Collection}
& (Join-Path $PSScriptRoot 'verify-rclone-notices.ps1') -Collection $engineNotices -Engine (Join-Path $stage 'rclone.exe') -EngineManifest $EngineManifest
if($LASTEXITCODE -ne 0){throw 'rclone notice verification failed'}
$collection=Get-Content -LiteralPath (Join-Path $engineNotices 'collection.json') -Raw|ConvertFrom-Json
foreach($module in $collection.records){
 $target=Join-Path $licenseRoot $module.folder
 [IO.Directory]::CreateDirectory($target)|Out-Null
 foreach($file in $module.files){
  Copy-Item -LiteralPath (Join-Path (Join-Path $engineNotices $module.folder) $file.file) -Destination (Join-Path $target $file.file)
 }
}
$index=Join-Path $licenseRoot 'rclone-index'
[IO.Directory]::CreateDirectory($index)|Out-Null
Copy-Item -LiteralPath (Join-Path $engineNotices 'collection.json') -Destination (Join-Path $index 'NOTICE.json')
$sourceAccess=Join-Path $licenseRoot 'rclone-source-access'
[IO.Directory]::CreateDirectory($sourceAccess)|Out-Null
Copy-Item -LiteralPath $engineIdentity.SourceNotice -Destination (Join-Path $sourceAccess 'NOTICE.txt')
& (Join-Path $PSScriptRoot 'verify-rclone-notices.ps1') -Collection $licenseRoot -Manifest (Join-Path $index 'NOTICE.json') -Engine (Join-Path $stage 'rclone.exe') -EngineManifest $EngineManifest
if($LASTEXITCODE -ne 0){throw 'Staged rclone notice verification failed'}
$lines=Get-ChildItem -LiteralPath $stage -Recurse -File | Sort-Object FullName | ForEach-Object { ((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant())+'  '+[IO.Path]::GetRelativePath($stage,$_.FullName).Replace('\','/') }
[IO.File]::WriteAllLines((Join-Path $stage 'SHA256SUMS.txt'),$lines,[Text.UTF8Encoding]::new($false))
Compress-Archive -LiteralPath $stage -DestinationPath $archive -CompressionLevel Optimal
$hash=Get-FileHash -LiteralPath $archive -Algorithm SHA256
[IO.File]::WriteAllText(($archive+'.sha256'),($hash.Hash.ToLowerInvariant()+'  '+[IO.Path]::GetFileName($archive)),[Text.UTF8Encoding]::new($false))
Write-Output $hash
