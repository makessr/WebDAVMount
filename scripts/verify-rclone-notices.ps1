#requires -Version 7.0
param([Parameter(Mandatory=$true)][string]$Collection,[string]$Engine,[string]$Manifest,[string]$EngineManifest)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$project=Split-Path $PSScriptRoot -Parent
if(!$Engine){$Engine=Join-Path $project 'bin/rclone.exe'}
$activation=Join-Path $env:LOCALAPPDATA 'Programs/MultiPanDev/Activate.ps1'
if(Test-Path -LiteralPath $activation){. $activation}
. (Join-Path $PSScriptRoot 'engine-identity.ps1')
$engineIdentity=Get-EngineIdentity $Engine $EngineManifest
$root=[IO.Path]::GetFullPath($Collection)
if(!$Manifest){$Manifest=Join-Path $root 'collection.json'}
$report=Get-Content -LiteralPath $Manifest -Raw|ConvertFrom-Json
if($report.engineSha256 -cne (Get-FileHash -LiteralPath $Engine -Algorithm SHA256).Hash.ToLowerInvariant()){throw 'Engine hash mismatch'}
if($report.failed -ne 0){throw 'Collection contains unresolved modules'}
$build=@(& go version -m $Engine)
if($LASTEXITCODE -ne 0){throw 'Cannot inspect engine'}
$expected=@{};$mainVersion=$null
foreach($line in $build){
    if($line -match '^\s*=>\s'){throw 'Replaced module refused'}
    if($line -match '^\s*mod\s+github.com/rclone/rclone\s+(v\S+)'){$mainVersion=$Matches[1]}
    if($line -match '^\s*dep\s+(\S+)\s+(\S+)\s+(h1:\S+)'){$expected[$Matches[1]]=@($Matches[2],$Matches[3])}
}
$mainVersion=$engineIdentity.Version
if($report.engineVersion -cne $mainVersion){throw 'Engine version mismatch'}
$expected['github.com/rclone/rclone']=@($mainVersion,$null)
$seen=@{};$paths=@{};$count=0
foreach($record in $report.records){
    if($seen.ContainsKey($record.name) -or !$expected.ContainsKey($record.name)){throw 'Unknown or duplicate module'}
    $seen[$record.name]=$true;$identity=$expected[$record.name]
    if($record.version -cne $identity[0] -or $record.status -cne 'collected'){throw 'Module identity/status mismatch'}
    if($identity[1] -and ($record.expectedSum -cne $identity[1] -or $record.downloadSum -cne $identity[1])){throw 'Embedded module checksum mismatch'}
    if($record.name -ceq 'github.com/rclone/rclone' -and $engineIdentity.Kind -eq 'nosmb' -and ($record.expectedSum -cne $engineIdentity.MainSum -or $record.downloadSum -cne $engineIdentity.MainSum)){throw 'Custom engine source checksum mismatch'}
    if($record.folder -cnotmatch '^rclone_[A-Za-z0-9_.-]+$'){throw 'Invalid module folder'}
    if(!$record.files.Count){throw 'Module has no notice files'}
    foreach($file in $record.files){
        if($file.file -cnotmatch '^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS)([._-][A-Za-z0-9_.-]+)?$'){throw 'Invalid notice filename'}
        $relative=$record.folder+'/'+$file.file
        if($paths.ContainsKey($relative)){throw 'Duplicate notice path'};$paths[$relative]=$true
        $path=Join-Path $root $relative
        $parent=[IO.Path]::GetFullPath($path)
        while($parent){
            if(([IO.File]::GetAttributes($parent) -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Reparse path refused'}
            $parent=[IO.Path]::GetDirectoryName($parent)
        }
        if((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256 -or (Get-Item -LiteralPath $path).Length -ne $file.bytes){throw 'Notice hash/size mismatch'}
        $count++
    }
}
if($seen.Count -ne $expected.Count -or $report.collected -ne $expected.Count -or $report.dependencies -ne $expected.Count-1){throw 'Module inventory is incomplete'}
Write-Output ("Notice collection verified: {0} modules, {1} files; exact engine checksum matched. Legal review remains separate." -f $seen.Count,$count)
