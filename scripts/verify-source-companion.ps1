#requires -Version 7.0
param([string]$Version='0.72.7',[string]$ArtifactDirectory,[string]$Report,
      [string]$EnginePath,[string]$EngineManifest,[string]$EngineNotices)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$project=Split-Path $PSScriptRoot -Parent
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Invalid source companion version'}
if(!$ArtifactDirectory){$ArtifactDirectory=Join-Path $project dist}
if(!$Report){$Report=Join-Path $project ('testdata/source-audit-'+[guid]::NewGuid().ToString('N')+'.json')}
if(Test-Path -LiteralPath $Report){throw 'Source audit report already exists'}
$activate=Join-Path $env:LOCALAPPDATA 'Programs/MultiPanDev/Activate.ps1';if(Test-Path $activate){. $activate}
. (Join-Path $PSScriptRoot engine-identity.ps1)
if(!$EnginePath){$EnginePath=Join-Path $project bin/rclone.exe}
$engine=Get-EngineIdentity $EnginePath $EngineManifest
if(!$EngineNotices){$EngineNotices=$engine.Collection}
if($engine.Kind -ne 'nosmb'){throw 'This source companion verifier requires the custom engine'}
$path=Join-Path $ArtifactDirectory ('WebDAVMount-'+$Version+'-reviewed-dependency-sources.zip')
$actual=(Get-FileHash $path).Hash.ToLowerInvariant()
$sidecar=[IO.File]::ReadAllText($path+'.sha256').Trim()
if($sidecar -cne ($actual+'  '+[IO.Path]::GetFileName($path))){throw 'Source archive checksum mismatch'}
$zip=[IO.Compression.ZipFile]::OpenRead($path);$hashes=@{};$index=$null
try{foreach($entry in $zip.Entries){
 $name=$entry.FullName.Replace('\','/');$prefix='reviewed-dependency-sources/'
 if(!$name.StartsWith($prefix,[StringComparison]::Ordinal)){throw 'Unexpected source archive root'}
 $name=$name.Substring($prefix.Length)
 if($name -match '(^|/)\.{1,2}(/|$)|:'){throw 'Unsafe source archive path'}
 if($name.EndsWith('/')){continue}
 if($hashes.ContainsKey($name)){throw 'Duplicate source archive path'}
 $stream=$entry.Open();$sha=[Security.Cryptography.SHA256]::Create()
 try{$hashes[$name]=([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant()}finally{$sha.Dispose();$stream.Dispose()}
 if($name -eq 'SOURCE_INDEX.json'){
  if($entry.Length -gt 1MB){throw 'Oversized source index'}
  $reader=[IO.StreamReader]::new($entry.Open());try{$index=$reader.ReadToEnd()|ConvertFrom-Json}finally{$reader.Dispose()}
 }
}}finally{$zip.Dispose()}
if(!$index -or $index.engineSha256 -cne (Get-FileHash $EnginePath).Hash.ToLowerInvariant()){throw 'Source index engine mismatch'}
$collection=Get-Content (Join-Path $EngineNotices collection.json) -Raw|ConvertFrom-Json
$expected=@{};$seen=@{}
$names=@('github.com/hashicorp/go-cleanhttp','github.com/hashicorp/go-retryablehttp','github.com/hashicorp/go-uuid','github.com/hashicorp/golang-lru/v2')
foreach($record in $index.records){
 if($record.name -notin $names -or $seen.ContainsKey($record.name)){throw 'Unknown or duplicate reviewed source module'}
 $seen[$record.name]=$true
 $module=@($collection.records|Where-Object name -ceq $record.name)
 if($module.Count -ne 1 -or $record.version -cne $module[0].version -or $record.moduleSum -cne $module[0].expectedSum){throw 'Reviewed source identity mismatch'}
 if($record.file -cne ($module[0].folder+'.zip')){throw 'Reviewed source archive name mismatch'}
 $download=(& go -C $project mod download -json ($record.name+'@'+$record.version)) -join "`n"
 if($LASTEXITCODE -ne 0){throw 'Cannot verify exact source module'}
 $source=$download|ConvertFrom-Json
 if($source.Sum -cne $record.moduleSum){throw 'Reviewed source module checksum mismatch'}
 $expected[$record.file]=(Get-FileHash $source.Zip).Hash.ToLowerInvariant()
 if($record.sha256 -cne $expected[$record.file]){throw 'Reviewed source ZIP checksum mismatch'}
}
if($seen.Count -ne 4){throw 'Incomplete reviewed source modules'}
$spec=Get-Content (Join-Path $engine.Directory SPEC.json) -Raw|ConvertFrom-Json
$sourceName='engine-rebuild/rclone-'+$engine.Version+'-original-module.zip'
if($index.engineSource.file -cne $sourceName -or $index.engineSource.sum -cne $engine.MainSum -or $index.engineSource.version -cne $engine.Version -or $index.engineSource.sha256 -cne $spec.sourceZipSHA256.ToLowerInvariant()){throw 'Engine source archive identity mismatch'}
$expected[$sourceName]=$spec.sourceZipSHA256.ToLowerInvariant()
$expected['NOTICE.txt']=(Get-FileHash $engine.SourceNotice).Hash.ToLowerInvariant()
foreach($name in $engine.PolicyFiles){$expected['engine-rebuild/build/engine-nosmb/'+$name]=(Get-FileHash (Join-Path $engine.Directory $name)).Hash.ToLowerInvariant()}
$expected['engine-rebuild/scripts/build-engine.ps1']=(Get-FileHash (Join-Path $engine.Directory REBUILD.ps1)).Hash.ToLowerInvariant()
$expected['engine-rebuild/scripts/engine-identity.ps1']=(Get-FileHash (Join-Path $PSScriptRoot engine-identity.ps1)).Hash.ToLowerInvariant()
$expected['engine-rebuild/BUILT-ENGINE.json']=(Get-FileHash $engine.Manifest).Hash.ToLowerInvariant()
foreach($name in $expected.Keys){if(!$hashes.ContainsKey($name) -or $hashes[$name] -cne $expected[$name]){throw ('Source companion file mismatch: '+$name)}}
$allowed=@($expected.Keys)+@('SOURCE_INDEX.json','engine-rebuild/go.mod','engine-rebuild/README.txt')
if($hashes.Count -ne $allowed.Count){throw 'Unexpected source companion file count'}
foreach($name in $hashes.Keys){if($name -notin $allowed){throw 'Unexpected source companion file'}}
if(!$hashes.ContainsKey('engine-rebuild/go.mod') -or !$hashes.ContainsKey('engine-rebuild/README.txt')){throw 'Missing rebuild instructions'}
$result=[ordered]@{status='PASS';version=$Version;archive=$path;sha256=$actual;files=$hashes.Count;reviewedModules=4;engineSource=$engine.Version;engineSourceH1=$engine.MainSum;scope='Exact original module ZIPs and recorded engine rebuild inputs verified; not an offline toolchain or complete legal review'}
$result|ConvertTo-Json -Depth 4|Set-Content -LiteralPath $Report
Write-Output ('Source companion verified: four reviewed modules and exact engine/rebuild inputs; '+$Report)
