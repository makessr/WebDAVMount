#requires -Version 7.0
param([string]$Version='0.72.7',[string]$TestKey,[string]$Output,
      [string]$PortableArchive,
      [ValidateSet('amd64','arm64')][string]$Architecture='amd64',
      [switch]$TestInterruptAfterRegistration,
      [string]$Compiler=(Join-Path (Split-Path $PSScriptRoot -Parent) 'tools/nsis-toolchain/nsis-3.13/makensis.exe'))
$ErrorActionPreference='Stop'
$project=Split-Path $PSScriptRoot -Parent
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Invalid release version'}
if($TestKey -and $TestKey -notmatch '^Installer-[a-f0-9]{32}$'){throw 'Invalid isolated test registry key'}
if($TestInterruptAfterRegistration -and (!$TestKey -or !$Output)){throw 'Interruption tests require an isolated test registry key and an explicit output file'}
if(!(Test-Path -LiteralPath $Compiler)){throw 'NSIS 3.13 compiler is required; see README'}
$archive=if($PortableArchive){[IO.Path]::GetFullPath($PortableArchive)}else{Join-Path $project "dist/WebDAVMount-$Version-windows-$Architecture.zip"}
$work=Join-Path $project ('dist/installer-stage-'+[guid]::NewGuid().ToString('N'))
Expand-Archive -LiteralPath $archive -DestinationPath $work
$payload=Join-Path $work "WebDAVMount-$Version-windows-$Architecture"
. (Join-Path $project 'build/portable/Update-WebDAVMount.ps1')
$manifest=Read-UpgradeManifest $payload $true
$machine=if($Architecture -ceq 'arm64'){0xAA64}else{0x8664}
foreach($name in @('WebDAVMount.exe','rclone.exe')){
 if((Get-UpgradePEMachine (Get-UpgradePath $payload $name)) -ne $machine){throw "Package architecture mismatch: $name"}
}
$entries=@($manifest.Files.Keys|Sort-Object)+@('SHA256SUMS.txt','licenses/NSIS/COPYING','安装与卸载.txt')
$license=Join-Path (Split-Path $Compiler -Parent) 'COPYING'
function Escape-Nsis([string]$Value){return $Value.Replace('$','$$').Replace('"','$\"')}
$install=@();$remove=@();$checks=@();$parents=@{}
foreach($entry in $entries){
 $relative=$entry.Replace('/','\');$source=Join-Path $payload $relative
 if($entry -eq 'licenses/NSIS/COPYING'){$source=$license}
 if($entry -eq '安装与卸载.txt'){$source=Join-Path $project 'build/windows/安装与卸载.txt'}
 $parent=[IO.Path]::GetDirectoryName($relative)
 $destination='$PayloadDirectory';if($parent){$destination+='\'+(Escape-Nsis $parent)}
 $install+='SetOutPath "'+$destination+'"'
 $install+='File "/oname='+(Escape-Nsis ([IO.Path]::GetFileName($relative)))+'" "'+(Escape-Nsis $source)+'"'
 $checks+='Push "$INSTDIR\'+(Escape-Nsis $relative)+'"';$checks+='Call un.CheckPath'
 $checks+='Push "$INSTDIR\'+(Escape-Nsis $relative)+'"';$checks+='Call un.CheckFileLock'
 $remove+='ClearErrors';$remove+='Delete "$INSTDIR\'+(Escape-Nsis $relative)+'"';$remove+='IfErrors 0 +2';$remove+='Call un.DeleteError'
 while($parent){$parents[$parent]=$true;$parent=[IO.Path]::GetDirectoryName($parent)}
}
foreach($parent in @($parents.Keys|Sort-Object Length -Descending)){$remove+='RMDir "$INSTDIR\'+(Escape-Nsis $parent)+'"'}
$files=Join-Path $work 'files.nsh';$deletes=Join-Path $work 'deletes.nsh';$validation=Join-Path $work 'checks.nsh'
[IO.File]::WriteAllLines($files,$install,[Text.UTF8Encoding]::new($true))
[IO.File]::WriteAllLines($deletes,$remove,[Text.UTF8Encoding]::new($true))
[IO.File]::WriteAllLines($validation,$checks,[Text.UTF8Encoding]::new($true))
if(!$Output){
 $suffix=if($Architecture -ceq 'arm64'){'-arm64'}else{''}
 $Output=Join-Path $project "dist/WebDAVMount-$Version$suffix-setup.exe"
}
$Output=[IO.Path]::GetFullPath($Output)
if(Test-Path -LiteralPath $Output){throw 'Installer output already exists'}
$arguments=@('/V2',"/DVERSION=$Version","/DOUTPUT=$Output","/DFILE_LIST=$files","/DDELETE_LIST=$deletes","/DUNINSTALL_CHECKS=$validation")
if($Architecture -ceq 'arm64'){$arguments+='/DARM64_PACKAGE'}
if($TestKey){$arguments+="/DTEST_KEY=$TestKey"}
if($TestInterruptAfterRegistration){
 $original=[IO.File]::ReadAllText((Join-Path $project 'build/windows/Upgrade-Installed.ps1'))
 $anchor="throw 'Version registration verification failed.'}"
 $index=$original.IndexOf($anchor,[StringComparison]::Ordinal)
 if($index -lt 0 -or $original.LastIndexOf($anchor,[StringComparison]::Ordinal) -ne $index){throw 'Cannot locate the single registry commit checkpoint'}
 $fixture=Join-Path $work 'Test-Upgrade-Installed.ps1'
 $checkpoint=@'

                    $ready=Get-UpgradePath $targetRoot 'test-registry-ready'
                    $temp=Get-UpgradePath $targetRoot 'test-registry-ready.tmp'
                    $marker=[IO.FileStream]::new($temp,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None,4096,[IO.FileOptions]::WriteThrough)
                    try{$marker.WriteByte(1);$marker.Flush($true)}finally{$marker.Dispose()}
                    Publish-UpgradeFile $temp $ready
                    if($env:WEBDAVMOUNT_TEST_POWER_OFF -eq '1'){
                        Start-Sleep -Seconds 300
                        $expired=[IO.FileStream]::new((Get-UpgradePath $targetRoot 'test-registry-expired'),[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                        try{$expired.WriteByte(1);$expired.Flush($true)}finally{$expired.Dispose()}
                        [Environment]::Exit(92)
                    }
                    [Environment]::Exit(91)
'@
 $text=$original.Insert($index+$anchor.Length,$checkpoint)
 [IO.File]::WriteAllText($fixture,$text,[Text.UTF8Encoding]::new($true))
 $arguments+="/DTEST_UPGRADE_SCRIPT=$fixture"
}
& $Compiler @arguments (Join-Path $project 'build/windows/installer.nsi')
if($LASTEXITCODE -ne 0){throw 'Installer compilation failed'}
$hash=Get-FileHash -LiteralPath $Output
[IO.File]::WriteAllText(($Output+'.sha256'),($hash.Hash.ToLowerInvariant()+'  '+[IO.Path]::GetFileName($Output)),[Text.UTF8Encoding]::new($false))
Write-Output $hash
