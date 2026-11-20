#requires -Version 7.0
param([string]$Version='0.72.7',[string]$ArtifactDirectory,
      [string]$Engine,[string]$EngineManifest,[string]$EngineNotices)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$project=Split-Path $PSScriptRoot -Parent
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Invalid version'}
if(!$ArtifactDirectory){$ArtifactDirectory=Join-Path $project 'dist'}
[IO.Directory]::CreateDirectory($ArtifactDirectory)|Out-Null
$archive=Join-Path $ArtifactDirectory ('WebDAVMount-'+$Version+'-reviewed-dependency-sources.zip')
if(Test-Path -LiteralPath $archive){throw 'Source artifact already exists'}
$activation=Join-Path $env:LOCALAPPDATA 'Programs/MultiPanDev/Activate.ps1'
if(Test-Path -LiteralPath $activation){. $activation}
. (Join-Path $PSScriptRoot engine-identity.ps1)
if(!$Engine){$Engine=Join-Path $project bin/rclone.exe}
$engineIdentity=Get-EngineIdentity $Engine $EngineManifest
if(!$EngineNotices){$EngineNotices=$engineIdentity.Collection}
$collection=Get-Content -LiteralPath (Join-Path $EngineNotices collection.json) -Raw|ConvertFrom-Json
& (Join-Path $project 'scripts/verify-rclone-notices.ps1') -Collection $EngineNotices -Engine $Engine -EngineManifest $EngineManifest|Out-Null
if($LASTEXITCODE -ne 0){throw 'Engine notice collection does not match'}
$names=@('github.com/hashicorp/go-cleanhttp','github.com/hashicorp/go-retryablehttp','github.com/hashicorp/go-uuid','github.com/hashicorp/golang-lru/v2')
if($engineIdentity.Kind -eq 'official'){$names=@('github.com/cloudsoda/sddl')+$names}
$root=Join-Path $ArtifactDirectory ('reviewed-source-stage-'+[guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root)|Out-Null
[IO.File]::WriteAllText((Join-Path $root 'go.mod'),"module webdavmount.source.collector`n`ngo 1.26.0`n",[Text.UTF8Encoding]::new($false))
$payload=Join-Path $root 'reviewed-dependency-sources'
[IO.Directory]::CreateDirectory($payload)|Out-Null
$records=@()
foreach($name in $names){
    $entry=@($collection.records|Where-Object name -ceq $name)
    if($entry.Count -ne 1){throw 'Missing reviewed module'}
    $entry=$entry[0]
    $json=(& go -C $root mod download -json ($entry.name+'@'+$entry.version)) -join "`n"
    if($LASTEXITCODE -ne 0){throw 'Exact source download failed'}
    $source=$json|ConvertFrom-Json
    if($source.Path -cne $entry.name -or $source.Version -cne $entry.version -or $source.Sum -cne $entry.expectedSum){throw 'Source module does not match binary checksum'}
    $file=$entry.folder+'.zip';$destination=Join-Path $payload $file
    [IO.File]::Copy($source.Zip,$destination,$false)
    $hash=(Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
    if($hash -cne (Get-FileHash -LiteralPath $source.Zip -Algorithm SHA256).Hash.ToLowerInvariant()){throw 'Source ZIP copy differs'}
    $license=if($name -eq 'github.com/cloudsoda/sddl'){'LICENSE: LGPL-3.0; README: MIT; unresolved conflict'}else{'MPL-2.0'}
    $records+=[ordered]@{name=$name;version=$entry.version;moduleSum=$source.Sum;file=$file;sha256=$hash;licenseFinding=$license;source='https://proxy.golang.org/'+$name+'/@v/'+$entry.version+'.zip'}
}
$engineSource=$null
if($engineIdentity.Kind -eq 'nosmb'){
 $tree=Join-Path $payload 'engine-rebuild';[IO.Directory]::CreateDirectory((Join-Path $tree scripts))|Out-Null
 [IO.Directory]::CreateDirectory((Join-Path $tree 'build/engine-nosmb'))|Out-Null
 Copy-Item -LiteralPath (Join-Path $engineIdentity.Directory REBUILD.ps1) -Destination (Join-Path $tree 'scripts/build-engine.ps1')
 Copy-Item -LiteralPath (Join-Path $PSScriptRoot engine-identity.ps1) -Destination (Join-Path $tree 'scripts/engine-identity.ps1')
 foreach($name in $engineIdentity.PolicyFiles){Copy-Item (Join-Path $engineIdentity.Directory $name) (Join-Path $tree ('build/engine-nosmb/'+$name))}
 [IO.File]::WriteAllText((Join-Path $tree go.mod),"module webdavmount.engine.rebuild`n`ngo 1.26.0`n",[Text.UTF8Encoding]::new($false))
 $mainJson=(& go -C $root mod download -json ('github.com/rclone/rclone@'+$engineIdentity.Version)) -join "`n"
 if($LASTEXITCODE -ne 0){throw 'Engine source download failed'}
 $main=$mainJson|ConvertFrom-Json
 if($main.Sum -cne $engineIdentity.MainSum){throw 'Engine source module checksum mismatch'}
 $zipName='rclone-'+$engineIdentity.Version+'-original-module.zip'
 [IO.File]::Copy($main.Zip,(Join-Path $tree $zipName),$false)
 Copy-Item (Join-Path $engineIdentity.Directory ENGINE.json) (Join-Path $tree 'BUILT-ENGINE.json')
 $engineSource=[ordered]@{name='github.com/rclone/rclone';version=$engineIdentity.Version;sum=$main.Sum;file=('engine-rebuild/'+$zipName);sha256=(Get-FileHash (Join-Path $tree $zipName)).Hash.ToLowerInvariant()}
 $architectureOption=if($engineIdentity.Architecture -ceq 'arm64'){' -Architecture arm64'}else{''}
 [IO.File]::WriteAllText((Join-Path $tree README.txt),"Custom rclone rebuild inputs. Not an official rclone binary.`r`nRun scripts/build-engine.ps1$architectureOption in PowerShell 7 with pinned Go 1.27.4 windows/amd64 and Node.js for the conditional writeback patch.`r`nThe script verifies the original source ZIP and module sums, uses the recorded overlay, and refuses an existing destination.`r`nOther Go dependencies need a module cache or network access; this is not a fully offline toolchain or vendor archive.`r`nThe original module ZIP is preserved here for inspection. No desktop application source license is granted.`r`n",[Text.UTF8Encoding]::new($false))
}
$report=[ordered]@{engineSha256=$collection.engineSha256;scope='Original reviewed source modules and, for custom builds, exact engine source/rebuild inputs; no desktop app source license is granted';records=$records;engineSource=$engineSource;pending='Other redistribution, notice and attribution review remains separate; historical official builds retain their earlier license findings'}
[IO.File]::WriteAllText((Join-Path $payload 'SOURCE_INDEX.json'),($report|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
Copy-Item -LiteralPath $engineIdentity.SourceNotice -Destination (Join-Path $payload 'NOTICE.txt')
Compress-Archive -LiteralPath $payload -DestinationPath $archive
$hash=(Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText(($archive+'.sha256'),($hash+'  '+[IO.Path]::GetFileName($archive)),[Text.UTF8Encoding]::new($false))
Write-Output ('Prepared reviewed source archives and applicable rebuild inputs: '+$archive)
