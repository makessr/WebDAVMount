#requires -Version 7.0
param([string]$Version='0.20.0',[string]$ArtifactDirectory,[string]$Report,
      [ValidateSet('amd64','arm64')][string]$Architecture='amd64',
      [string]$DesktopExecutable,[string]$Engine,[string]$EngineManifest)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$project=Split-Path $PSScriptRoot -Parent
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Invalid release version'}
if(!$ArtifactDirectory){$ArtifactDirectory=Join-Path $project 'dist'}
if($Architecture -ceq 'arm64' -and (!$DesktopExecutable -or !$Engine -or !$EngineManifest)){
    throw 'ARM64 audit requires the exact desktop, engine and engine provenance'
}
if(!$DesktopExecutable){$DesktopExecutable=Join-Path $project "bin/WebDAVMount-$Version.exe"}
if(!$Engine){$Engine=Join-Path $project 'bin/rclone.exe'}
if(!$Report){$Report=Join-Path $project ('testdata/release-audit-'+[guid]::NewGuid().ToString('N')+'.json')}
if(Test-Path -LiteralPath $Report){throw 'Audit report already exists'}
$activation=Join-Path $env:LOCALAPPDATA 'Programs/MultiPanDev/Activate.ps1'
if(Test-Path -LiteralPath $activation){. $activation}
function Assert-Audit([bool]$Value,[string]$Message){if(!$Value){throw $Message}}
function File-Hash([string]$Path){return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()}
function Module-Info([string]$Path){
    $lines=@(& go version -m $Path)
    if($LASTEXITCODE -ne 0){throw 'Cannot inspect binary build information'}
    $mainName=$null;$mainVersion=$null
    foreach($line in $lines){if($line -match '^\s*mod\s+(\S+)\s+(\S+)'){$mainName=$Matches[1];$mainVersion=$Matches[2]}}
    $deps=@(foreach($line in $lines){if($line -match '^\s*dep\s+(\S+)\s+(\S+)\s+(h1:\S+)'){
        [ordered]@{name=$Matches[1];version=$Matches[2];sum=$Matches[3]}
    }})
    return [ordered]@{header=$lines[0];modules=$deps;module=$mainName;version=$mainVersion}
}
$artifacts=@()
$setupSuffix=if($Architecture -ceq 'arm64'){'-arm64'}else{''}
foreach($name in @("WebDAVMount-$Version-windows-$Architecture.zip","WebDAVMount-$Version$setupSuffix-setup.exe")){
    $path=Join-Path $ArtifactDirectory $name
    $sidecar=[IO.File]::ReadAllText($path+'.sha256').Trim()
    Assert-Audit ($sidecar -match '^([0-9a-fA-F]{64})  (.+)$') 'Invalid release checksum file'
    $expected=$Matches[1].ToLowerInvariant();$listedName=$Matches[2]
    $hash=File-Hash $path
    Assert-Audit ($listedName -ceq $name -and $hash -eq $expected) ('Artifact checksum mismatch: '+$name)
    $artifacts+=[ordered]@{name=$name;sha256=$hash}
}
$archive=[IO.Compression.ZipFile]::OpenRead((Join-Path $ArtifactDirectory $artifacts[0].name))
$hashes=@{};$contents=@{};$prefix="WebDAVMount-$Version-windows-$Architecture/"
try{
    foreach($entry in $archive.Entries){
        $full=$entry.FullName.Replace('\','/')
        Assert-Audit ($full.StartsWith($prefix,[StringComparison]::Ordinal)) 'Unexpected archive root'
        $relative=$full.Substring($prefix.Length)
        Assert-Audit ($relative -notmatch '(^|/)\.{1,2}(/|$)|:') 'Unsafe archive path'
        if($full.EndsWith('/')){continue}
        Assert-Audit (!$hashes.ContainsKey($relative)) 'Duplicate archive file'
        $stream=$entry.Open();$sha=[Security.Cryptography.SHA256]::Create()
        try{$hashes[$relative]=([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant()}finally{$sha.Dispose();$stream.Dispose()}
        if($relative -in @('SHA256SUMS.txt','licenses/rclone-index/NOTICE.json')){
            Assert-Audit ($entry.Length -le 1MB) 'Oversized manifest'
            $reader=[IO.StreamReader]::new($entry.Open(),[Text.UTF8Encoding]::new($false,$true))
            try{$contents[$relative]=$reader.ReadToEnd()}finally{$reader.Dispose()}
        }
    }
}finally{$archive.Dispose()}
Assert-Audit ($contents.ContainsKey('SHA256SUMS.txt')) 'Missing package manifest'
$listed=@{}
foreach($line in [regex]::Split($contents['SHA256SUMS.txt'],'\r?\n')){
    if(!$line){continue}
    Assert-Audit ($line -match '^([0-9a-fA-F]{64})  (.+)$') 'Invalid package manifest'
    $expected=$Matches[1].ToLowerInvariant();$name=$Matches[2]
    Assert-Audit (!$listed.ContainsKey($name) -and $hashes.ContainsKey($name) -and $hashes[$name] -eq $expected) ('Package checksum mismatch: '+$name)
    $listed[$name]=$true
}
Assert-Audit ($hashes.Count -eq $listed.Count+1) 'Unlisted file in release archive'
foreach($pair in @(@('WebDAVMount.exe',$DesktopExecutable),@('rclone.exe',$Engine))){
    Assert-Audit ($hashes[$pair[0]] -eq (File-Hash $pair[1])) ('Binary differs from build output: '+$pair[0])
    if($Architecture -ceq 'arm64'){
        $build=@(go version -m $pair[1])
        Assert-Audit ($LASTEXITCODE -eq 0 -and $build.Trim() -ccontains "build`tGOARCH=arm64") ('Wrong binary architecture: '+$pair[0])
    }
}
function Audit-Notices([string]$Root,[string]$Folder){
    $files=@(Get-ChildItem -LiteralPath $Root -File | Where-Object Name -Match '^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS|THIRD-PARTY-LICENSES?)([._-].*)?$')
    Assert-Audit ($files.Count -gt 0) ('No source notices: '+$Folder)
    return @(foreach($file in $files){
        $name='licenses/'+$Folder+'/'+$file.Name
        Assert-Audit ($hashes.ContainsKey($name) -and $hashes[$name] -eq (File-Hash $file.FullName)) ('Notice missing or changed: '+$name)
        $name
    })
}
$main=Module-Info $DesktopExecutable
$goDependencies=@(foreach($module in $main.modules){
    $source=& go -C $project list -m -f '{{.Version}}|{{.Dir}}' ($module.name)
    Assert-Audit ($LASTEXITCODE -eq 0 -and $source.StartsWith($module.version+'|')) ('Build inputs no longer match binary: '+$module.name)
    $folder=$module.name -replace '[^A-Za-z0-9.-]','_'
    [ordered]@{name=$module.name;version=$module.version;notices=@(Audit-Notices $source.Split('|',2)[1] $folder)}
})
. (Join-Path $PSScriptRoot 'frontend-notices.ps1')
$frontendNotices=Get-FrontendNoticeInputs $project $DesktopExecutable $Version
if($frontendNotices.mode -eq 'bundle-inventory'){
    Assert-Audit ($hashes.ContainsKey('licenses/frontend-index/NOTICE.json') -and $hashes['licenses/frontend-index/NOTICE.json'] -eq (File-Hash $frontendNotices.receipt)) 'Frontend build receipt missing or changed'
}else{Assert-Audit (!$hashes.ContainsKey('licenses/frontend-index/NOTICE.json')) 'Frontend receipt has no matching local build record'}
$npm=@(foreach($entry in $frontendNotices.packages|Sort-Object Key){
    $root=Join-Path $project ('frontend/'+$entry.Key)
    if($entry.Key -eq 'node_modules/@wailsio/runtime'){$root=& go -C $project list -m -f '{{.Dir}}' github.com/wailsapp/wails/v3;if($LASTEXITCODE -ne 0){throw 'Cannot locate Wails notices'}}
    $folder=('npm_'+$entry.Key) -replace '[^A-Za-z0-9.-]','_'
    [ordered]@{name=$entry.Key;version=$entry.version;notices=@(Audit-Notices $root $folder)}
})
$engineInfo=Module-Info $Engine
. (Join-Path $PSScriptRoot 'engine-identity.ps1')
$engineIdentity=Get-EngineIdentity $Engine $EngineManifest
Assert-Audit ([version]$Version -lt [version]'0.23.0' -or $engineIdentity.Kind -eq 'nosmb') 'Current release must use the verified no-SMB engine'
$noticeCollection=[ordered]@{included=$false;files=0;modules=0}
if($contents.ContainsKey('licenses/rclone-index/NOTICE.json')){
    $noticeIndex=$contents['licenses/rclone-index/NOTICE.json']|ConvertFrom-Json
    Assert-Audit ($noticeIndex.engineSha256 -ceq $hashes['rclone.exe'] -and $noticeIndex.failed -eq 0) 'Notice index does not match the engine'
    $expected=@{};foreach($module in $engineInfo.modules){$expected[$module.name]=@($module.version,$module.sum)}
    $known=@{};$knownPaths=@{};$noticeCount=0
    foreach($record in $noticeIndex.records){
        Assert-Audit (!$known.ContainsKey($record.name) -and $record.status -ceq 'collected') 'Duplicate or unresolved notice module'
        $known[$record.name]=$true
        if($record.name -ne 'github.com/rclone/rclone'){
            Assert-Audit ($expected.ContainsKey($record.name)) 'Unknown notice module'
            $identity=$expected[$record.name]
            Assert-Audit ($record.version -ceq $identity[0] -and $record.expectedSum -ceq $identity[1] -and $record.downloadSum -ceq $identity[1]) 'Notice module checksum mismatch'
        }else{
            Assert-Audit ($record.version -ceq $engineIdentity.Version -and $noticeIndex.engineVersion -ceq $engineIdentity.Version) 'Engine source version mismatch'
            if($engineIdentity.Kind -eq 'nosmb'){Assert-Audit ($record.expectedSum -ceq $engineIdentity.MainSum -and $record.downloadSum -ceq $engineIdentity.MainSum) 'Custom source checksum mismatch'}
        }
        Assert-Audit ($record.folder -cmatch '^rclone_[A-Za-z0-9_.-]+$' -and $record.files.Count -gt 0) 'Invalid notice module path or empty notices'
        foreach($file in $record.files){
            Assert-Audit ($file.file -cmatch '^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS)([._-][A-Za-z0-9_.-]+)?$') 'Invalid notice filename'
            $name='licenses/'+$record.folder+'/'+$file.file
            Assert-Audit (!$knownPaths.ContainsKey($name) -and $hashes.ContainsKey($name) -and $hashes[$name] -ceq $file.sha256) 'Archived notice differs from index'
            $knownPaths[$name]=$true;$noticeCount++
        }
    }
    Assert-Audit ($known.Count -eq $expected.Count+1 -and $known.ContainsKey('github.com/rclone/rclone') -and $noticeIndex.dependencies -eq $expected.Count -and $noticeIndex.collected -eq $known.Count) 'Incomplete engine notice inventory'
    $noticeCollection=[ordered]@{included=$true;files=$noticeCount;modules=$known.Count}
}
$goRoot=& go env GOROOT
if($LASTEXITCODE -ne 0){throw 'Cannot locate Go standard library notices'}
$goNotices=@(Audit-Notices $goRoot 'Go-standard-library')
Assert-Audit ($hashes['RCLONE-README.txt'] -eq (File-Hash $engineIdentity.Readme)) 'rclone README differs from matched distribution'
if($engineIdentity.Kind -eq 'nosmb'){
 foreach($file in $engineIdentity.ProvenanceFiles){
  Assert-Audit ($hashes.ContainsKey('engine-build/'+$file) -and $hashes['engine-build/'+$file] -eq (File-Hash (Join-Path $engineIdentity.Directory $file))) ('Custom engine provenance missing or changed: '+$file)
 }
}
Assert-Audit ($hashes['licenses/rclone-source-access/NOTICE.txt'] -eq (File-Hash $engineIdentity.SourceNotice)) 'Engine source access notice differs from selected build'
$reportData=[ordered]@{
    version=$Version;architecture=$Architecture;utc=[DateTime]::UtcNow.ToString('o');integrity='PASS';artifacts=$artifacts;archiveFiles=$hashes.Count
    desktopGo=$goDependencies;frontendProduction=$npm;frontendNoticeMode=$frontendNotices.mode;rcloneEmbeddedModules=$engineInfo.modules
    goStandardLibraryNotices=$goNotices;rcloneReadme='matches verified selected engine distribution';engineKind=$engineIdentity.Kind;engineSourceVersion=$engineIdentity.Version
    rcloneNoticeCollection=$noticeCollection
    scope='Desktop Go and selected frontend notice files compared with local build inputs; frontendNoticeMode identifies bundle receipts or the legacy production superset. This does not establish complete redistribution compliance.'
    pending=@(
        if(Test-Path -LiteralPath (Join-Path $project 'LICENSE')){'Project license text exists; scope not reviewed by this tool'}else{'Project release license not selected'}
        if(!$noticeCollection.included){'rclone embedded module notice collection not included in this artifact'}
        'Collected notice text still requires attribution and legal-terms review'
        'Nested or vendored notices inside dependencies require review'
    )
    externalRuntimes=@('WinFsp','Microsoft WebView2')
}
$bytes=[Text.UTF8Encoding]::new($false).GetBytes(($reportData|ConvertTo-Json -Depth 8))
$out=[IO.File]::Open($Report,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
try{$out.Write($bytes,0,$bytes.Length)}finally{$out.Dispose()}
Write-Output ("Release integrity PASS; desktop Go modules: {0}; frontend notices: {1}; rclone embedded modules: {2}; archived engine notices: {3}; report: {4}" -f $goDependencies.Count,$npm.Count,$engineInfo.modules.Count,$noticeCollection.files,$Report)
