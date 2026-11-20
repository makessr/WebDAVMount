#requires -Version 5.1
[CmdletBinding()]
param([string]$TargetDirectory,[string]$SourceDirectory,[switch]$CheckOnly,[switch]$RecoverOnly)

Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
if(!$SourceDirectory){$SourceDirectory=$PSScriptRoot}

function Get-UpgradeRoot([string]$Path){
    if(!$Path -or $Path -notmatch '^(?:[A-Za-z]:[\\/]|\\\\[^\\]+\\[^\\]+\\)'){throw 'Choose an absolute folder path.'}
    $full=[IO.Path]::GetFullPath($Path).TrimEnd('\','/')
    if($full -eq [IO.Path]::GetPathRoot($full).TrimEnd('\','/')){throw 'A drive root cannot be a program folder.'}
    if(!(Test-Path -LiteralPath $full -PathType Container)){throw 'The program folder does not exist.'}
    Assert-UpgradeNoReparse $full
    return $full
}
function Assert-UpgradeNoReparse([string]$Path){
    $current=[IO.Path]::GetFullPath($Path)
    while($current){
        if(Test-Path -LiteralPath $current){
            $item=Get-Item -LiteralPath $current -Force
            if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Reparse points are not supported by this updater.'}
        }
        $parent=[IO.Path]::GetDirectoryName($current)
        if($parent -eq $current){break};$current=$parent
    }
}
function Get-UpgradePath([string]$Root,[string]$Relative){
    $full=[IO.Path]::GetFullPath([IO.Path]::Combine($Root,$Relative.Replace('/','\')))
    if(!$full.StartsWith($Root+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'A package path escapes its folder.'}
    Assert-UpgradeNoReparse $full
    return $full
}
function Test-UpgradeOwnedName([string]$Name){
    foreach($part in $Name.Split('/')){if($part.EndsWith('.') -or $part.EndsWith(' ')){return $false}}
    if($Name -in @('WebDAVMount.exe','rclone.exe','使用说明.txt','THIRD_PARTY_NOTICES.txt','RCLONE-README.txt','Update-WebDAVMount.ps1')){return $true}
    if($Name -cmatch '^engine-build/(?:ENGINE\.json|SPEC\.json|MODULES\.json|BACKEND-ALL\.txt|MOUNT-RC\.txt|apply-writeback\.cjs|WEBDAV-WRITEBACK\.txt|apply-mounted-move\.cjs|WEBDAV-MOVE\.txt|VFS-MOVE\.txt|CACHE-MOVE\.txt|BUILD-INFO\.txt|README\.txt|REBUILD\.ps1)$'){return $true}
    return $Name -cmatch '^licenses/[A-Za-z0-9_.-]+/(?:LICENSE|LICENCE|COPYING|NOTICE|PATENTS|THIRD-PARTY-LICENSES?)(?:[._-][A-Za-z0-9_.-]+)?$' -and $Name -notmatch '(^|/)\.{1,2}(/|$)'
}
function Get-UpgradeHash([string]$Path){return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()}
function Get-UpgradePEMachine([string]$Path){
    $reader=[IO.BinaryReader]::new([IO.File]::OpenRead($Path))
    try{
        if($reader.BaseStream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5A4D){return 0}
        $reader.BaseStream.Position=60
        $offset=$reader.ReadUInt32()
        if($offset -lt 64 -or $offset -gt ($reader.BaseStream.Length-6)){return 0}
        $reader.BaseStream.Position=$offset
        if($reader.ReadUInt32() -ne 0x4550){return 0}
        return $reader.ReadUInt16()
    }finally{$reader.Dispose()}
}
function Test-UpgradeRegistryKey([string]$Key){
    return $Key -in @('Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDAVMount',
        'Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDAVMount-ARM64') -or
        $Key -match '^Software\\WebDAVMount\\Tests\\Installer-[a-f0-9]{32}$'
}
function Read-UpgradeManifest([string]$Root,[bool]$NewPackage){
    $manifest=Get-UpgradePath $Root 'SHA256SUMS.txt'
    $bytes=[IO.File]::ReadAllBytes($manifest)
    if($bytes.Length -gt 1MB){throw 'The package manifest is too large.'}
    $sha=[Security.Cryptography.SHA256]::Create()
    try{$digest=([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-','').ToLowerInvariant()}finally{$sha.Dispose()}
    $text=([Text.UTF8Encoding]::new($false,$true)).GetString($bytes).TrimStart([char]0xFEFF)
    $files=@{}
    foreach($line in [regex]::Split($text,'\r?\n')){
        if($line -eq ''){continue}
        if($line -notmatch '^([0-9a-fA-F]{64})  (.+)$'){throw 'Invalid SHA256SUMS.txt format.'}
        $hash=$Matches[1].ToLowerInvariant();$relative=$Matches[2]
        if(!(Test-UpgradeOwnedName $relative) -or $files.ContainsKey($relative)){throw 'Unexpected or duplicate package path.'}
        $path=Get-UpgradePath $Root $relative
        if(!(Test-Path -LiteralPath $path -PathType Leaf) -or (Get-UpgradeHash $path) -ne $hash){throw ('Package file failed integrity verification: '+$relative)}
        $files[$relative]=$hash
    }
    $required=@('WebDAVMount.exe','rclone.exe','使用说明.txt','THIRD_PARTY_NOTICES.txt','RCLONE-README.txt')
    if($NewPackage){$required+='Update-WebDAVMount.ps1'}
    foreach($name in $required){if(!$files.ContainsKey($name)){throw ('Missing required package file: '+$name)}}
    return [pscustomobject]@{Files=$files;ManifestHash=$digest}
}
function Assert-UpgradeNotRunning([string]$Root){
    foreach($process in Get-Process){
        $path=$null
        try{$path=$process.Path}catch{}
        if($path -and $path.StartsWith($Root+'\',[StringComparison]::OrdinalIgnoreCase)){
            throw 'A program from the target folder is running. Use Exit and Unmount before updating.'
        }
    }
}
function New-UpgradeParent([string]$Path){[IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Path))|Out-Null}
function Publish-UpgradeFile([string]$From,[string]$To){
    if(!('WebDAVMount.DurableUpdate' -as [type])){
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace WebDAVMount { public static class DurableUpdate {
[DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
public static extern bool MoveFileEx(string from,string to,uint flags);
}}
'@
    }
    if(![WebDAVMount.DurableUpdate]::MoveFileEx($From,$To,9)){throw [ComponentModel.Win32Exception]::new([Runtime.InteropServices.Marshal]::GetLastWin32Error())}
}
function Copy-UpgradeDurable([string]$From,[string]$To){
    Assert-UpgradeNoReparse $From;Assert-UpgradeNoReparse $To
    New-UpgradeParent $To
    # Windows PowerShell may reject a long sibling path even when $To fits.
    $temp=Join-Path ([IO.Path]::GetDirectoryName($To)) ([IO.Path]::GetRandomFileName())
    $created=$false
    try{
        $inputFile=[IO.File]::Open($From,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
        try{
            $outputFile=[IO.FileStream]::new($temp,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None,65536,[IO.FileOptions]::WriteThrough)
            $created=$true
            try{$inputFile.CopyTo($outputFile);$outputFile.Flush($true)}finally{$outputFile.Dispose()}
        }finally{$inputFile.Dispose()}
        Publish-UpgradeFile $temp $To
        $created=$false
    }finally{if($created -and [IO.File]::Exists($temp)){[IO.File]::Delete($temp)}}
}
function Write-UpgradeRecord([string]$Backup,[string]$Status,[object[]]$Items,[hashtable]$Registration){
    $data=[ordered]@{format=2;target=[IO.Path]::GetDirectoryName($Backup);status=$Status;utc=[DateTime]::UtcNow.ToString('o');registration=$Registration;files=@($Items|ForEach-Object{[ordered]@{name=$_.Name;oldHash=$_.OldHash;newHash=$_.Hash;backupOnly=[bool]($_.PSObject.Properties['BackupOnly'] -and $_.BackupOnly)}})}
    $path=Join-Path $Backup 'UpgradeRecord.json';$temp=Join-Path $Backup ('.record-'+[guid]::NewGuid().ToString('N')+'.tmp')
    try{
        [IO.File]::WriteAllText($temp,($data|ConvertTo-Json -Depth 6),[Text.UTF8Encoding]::new($false))
        $stream=[IO.File]::Open($temp,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
        try{$stream.Flush($true)}finally{$stream.Dispose()}
        Publish-UpgradeFile $temp $path
    }finally{if([IO.File]::Exists($temp)){[IO.File]::Delete($temp)}}
}
function Restore-PendingUpgradeLocked([string]$Root,[switch]$ValidateOnly){
    $pending=@()
    foreach($folder in Get-ChildItem -LiteralPath $Root -Directory -Filter 'WebDAVMount-upgrade-backup-*'){
        if($folder.Name -cnotmatch '^WebDAVMount-upgrade-backup-[a-f0-9]{32}$'){continue}
        Assert-UpgradeNoReparse $folder.FullName
        $recordPath=Join-Path $folder.FullName 'UpgradeRecord.json'
        if(!(Test-Path -LiteralPath $recordPath -PathType Leaf)){continue}
        Assert-UpgradeNoReparse $recordPath
        if((Get-Item -LiteralPath $recordPath).Length -gt 4MB){throw 'Upgrade recovery record exceeds limit.'}
        $record=[IO.File]::ReadAllText($recordPath)|ConvertFrom-Json
        if($record.status -in @('complete','rolled_back','recovered')){continue}
        if(!$record.PSObject.Properties['format'] -or $record.format -ne 2 -or $record.status -notin @('prepared','rollback_failed','recovering')){throw 'Unrecognized unfinished upgrade; preserve its backup for manual review.'}
        if(!([string]$record.target).Equals($Root,[StringComparison]::OrdinalIgnoreCase)){throw 'Recovery target mismatch.'}
        $pending+=@{folder=$folder.FullName;record=$record}
    }
    if($pending.Count -gt 1){throw 'Multiple unfinished upgrades; preserve backups and resolve before proceeding.'}
    if(!$pending.Count){return}
    if($ValidateOnly){throw 'An interrupted update needs recovery; rerun the new installer or updater without CheckOnly.'}
    Assert-UpgradeNotRunning $Root
    $backup=$pending[0].folder;$record=$pending[0].record;$rows=@($record.files)
    if($rows.Count -lt 5 -or $rows.Count -gt 5000){throw 'Recovery file count invalid.'}
    $old=Read-UpgradeManifest $backup $false;$seen=@{};$items=@()
    foreach($row in $rows){
        $name=[string]$row.name
        $allowed=(Test-UpgradeOwnedName $name) -or $name -in @('SHA256SUMS.txt','WebDAVMount.install','uninstall.exe','安装与卸载.txt','licenses/NSIS/COPYING')
        if(!$allowed -or $seen.ContainsKey($name) -or $row.newHash -cnotmatch '^[a-f0-9]{64}$' -or ($null -ne $row.oldHash -and $row.oldHash -cnotmatch '^[a-f0-9]{64}$')){throw 'Recovery path/hash invalid.'}
        $seen[$name]=$true
        if($old.Files.ContainsKey($name) -and $row.oldHash -cne $old.Files[$name]){throw 'Recovery old manifest mismatch.'}
        if($name -eq 'SHA256SUMS.txt' -and $row.oldHash -cne $old.ManifestHash){throw 'Recovery old checksum index mismatch.'}
        $from=Get-UpgradePath $backup $name;$to=Get-UpgradePath $Root $name
        if($null -ne $row.oldHash -and (!(Test-Path -LiteralPath $from -PathType Leaf) -or (Get-UpgradeHash $from) -cne $row.oldHash)){throw 'Recovery backup integrity failure; no restoration started.'}
        $items+=[pscustomobject]@{Name=$name;OldHash=$row.oldHash;Hash=$row.newHash;BackupOnly=[bool]$row.backupOnly}
    }
    foreach($name in @($old.Files.Keys)+@('SHA256SUMS.txt')){if(!$seen.ContainsKey($name)){throw 'Incomplete recovery manifest.'}}
    $registration=$null;$key=$null;$base=$null
    try{
        if($record.registration){
            $r=$record.registration
            if(!(Test-UpgradeRegistryKey $r.key) -or $r.oldVersion -notmatch '^\d+\.\d+\.\d+$' -or $r.newVersion -notmatch '^\d+\.\d+\.\d+$' -or [version]$r.oldVersion -gt [version]$r.newVersion){throw 'Recovery registration invalid.'}
            $base=[Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::CurrentUser,[Microsoft.Win32.RegistryView]::Registry64);$key=$base.OpenSubKey($r.key,$true)
            if(!$key -or !([string]$key.GetValue('InstallLocation')).Equals($Root,[StringComparison]::OrdinalIgnoreCase) -or $key.GetValue('DisplayVersion') -notin @($r.oldVersion,$r.newVersion)){throw 'Recovery registration changed externally.'}
            $registration=@{key=$r.key;oldVersion=$r.oldVersion;newVersion=$r.newVersion}
        }
        Write-UpgradeRecord $backup 'recovering' $items $registration
        # Preserve unrecognized/torn current bytes before replacing known-owned files.
        foreach($item in $items){
            $to=Get-UpgradePath $Root $item.Name
            if(Test-Path -LiteralPath $to -PathType Leaf){$h=Get-UpgradeHash $to;if($h -ne $item.OldHash -and $h -ne $item.Hash){$conflict=Get-UpgradePath $backup ('recovery-conflicts/'+[guid]::NewGuid().ToString('N')+'/'+$item.Name);Copy-UpgradeDurable $to $conflict}}
        }
        $ordered=@($items|Where-Object Name -ne 'WebDAVMount.exe')+@($items|Where-Object Name -eq 'WebDAVMount.exe')
        foreach($item in $ordered){$to=Get-UpgradePath $Root $item.Name;if($null -ne $item.OldHash){Copy-UpgradeDurable (Get-UpgradePath $backup $item.Name) $to;if((Get-UpgradeHash $to) -cne $item.OldHash){throw 'Recovery restored bytes mismatch.'}}elseif(Test-Path -LiteralPath $to -PathType Leaf){[IO.File]::Delete($to)}}
        if($key){$key.SetValue('DisplayVersion',$registration.oldVersion);$key.Flush()}
        Read-UpgradeManifest $Root $false|Out-Null
        Write-UpgradeRecord $backup 'recovered' $items $registration
    }finally{if($key){$key.Dispose()};if($base){$base.Dispose()}}
}
function Remove-UpgradeStage([string]$Target,[string]$Stage,[object[]]$Items){
    if(!$Stage.StartsWith($Target+'\.webdavmount-update-stage-',[StringComparison]::OrdinalIgnoreCase)){throw 'Invalid staging cleanup path.'}
    $directories=@{}
    foreach($item in $Items){
        $path=Get-UpgradePath $Stage $item.Name
        if(Test-Path -LiteralPath $path -PathType Leaf){[IO.File]::Delete($path)}
        $parent=[IO.Path]::GetDirectoryName($path)
        while($parent -and $parent.StartsWith($Stage+'\',[StringComparison]::OrdinalIgnoreCase)){$directories[$parent]=$true;$parent=[IO.Path]::GetDirectoryName($parent)}
    }
    foreach($path in @($directories.Keys|Sort-Object Length -Descending)){
        Assert-UpgradeNoReparse $path
        if(Test-Path -LiteralPath $path){[IO.Directory]::Delete($path,$false)}
    }
    [IO.Directory]::Delete($Stage,$false)
}

function Get-UpgradeMutex([string]$Root){
    $sha=[Security.Cryptography.SHA256]::Create()
    try{$name=([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Root.ToUpperInvariant())))).Replace('-','')}finally{$sha.Dispose()}
    $mutex=[Threading.Mutex]::new($false,('Local\WebDAVMount-Update-'+$name));$owns=$false
    try{try{$owns=$mutex.WaitOne(0)}catch [Threading.AbandonedMutexException]{$owns=$true};if(!$owns){throw 'Another update or recovery is using this installation.'};return $mutex}catch{$mutex.Dispose();throw}
}
function Invoke-WebDAVMountUpdate {
    param([string]$Source,[string]$Target,[switch]$ValidateOnly,[scriptblock]$AfterInstall,
          [switch]$InstalledPackage,[scriptblock]$BeforeComplete,[hashtable]$Registration,[scriptblock]$CommitPoint)
    $sourceRoot=Get-UpgradeRoot $Source;$targetRoot=Get-UpgradeRoot $Target
    if($sourceRoot.Equals($targetRoot,[StringComparison]::OrdinalIgnoreCase) -or $sourceRoot.StartsWith($targetRoot+'\',[StringComparison]::OrdinalIgnoreCase) -or $targetRoot.StartsWith($sourceRoot+'\',[StringComparison]::OrdinalIgnoreCase)){
        throw 'Source and target folders must be separate and must not contain each other.'
    }
    $mutex=Get-UpgradeMutex $targetRoot
    try {
    $new=Read-UpgradeManifest $sourceRoot $true
    Restore-PendingUpgradeLocked $targetRoot -ValidateOnly:$ValidateOnly
    $old=Read-UpgradeManifest $targetRoot $false
    if((Test-Path -LiteralPath (Join-Path $targetRoot 'WebDAVMount.install')) -and !$InstalledPackage){
        throw 'This is an installed application. Use the new setup executable to upgrade it.'
    }
    $items=@()
    foreach($name in @($new.Files.Keys|Sort-Object)){
        $destination=Get-UpgradePath $targetRoot $name
        if((Test-Path -LiteralPath $destination) -and !$old.Files.ContainsKey($name)){throw ('A user file would be overwritten: '+$name)}
        $previous=$null;if($old.Files.ContainsKey($name)){$previous=$old.Files[$name]}
        $items+=[pscustomobject]@{Name=$name;Hash=$new.Files[$name];OldHash=$previous}
    }
    $items+=[pscustomobject]@{Name='SHA256SUMS.txt';Hash=$new.ManifestHash;OldHash=$old.ManifestHash}
    if($InstalledPackage){
        $marker='WebDAVMount-8AC56D6F-43A6-4E37-A0D0-36DF969B1648'
        foreach($root in @($sourceRoot,$targetRoot)){
            if([IO.File]::ReadAllText((Get-UpgradePath $root 'WebDAVMount.install')) -ne $marker){throw 'Installation marker mismatch.'}
        }
        foreach($name in @('WebDAVMount.install','uninstall.exe','安装与卸载.txt','licenses/NSIS/COPYING')){
            if($new.Files.ContainsKey($name) -or $old.Files.ContainsKey($name)){throw 'Installer file overlaps the portable manifest.'}
            $from=Get-UpgradePath $sourceRoot $name;$previous=Get-UpgradePath $targetRoot $name
            $items+=[pscustomobject]@{Name=$name;Hash=(Get-UpgradeHash $from);OldHash=(Get-UpgradeHash $previous)}
        }
    }
    # A complete old-version backup also contains owned files omitted by the
    # new manifest (for example notices for removed dependencies). Retain these
    # at the target; they are not new package files and are never overwritten.
    $backupOnly=@(foreach($name in $old.Files.Keys){
        if(!$new.Files.ContainsKey($name)){
            [pscustomobject]@{Name=$name;Hash=$old.Files[$name];OldHash=$old.Files[$name];BackupOnly=$true}
        }
    })
    $journalItems=@($items)+@($backupOnly)
    Assert-UpgradeNotRunning $targetRoot
    foreach($item in $journalItems){
        if($null -ne $item.OldHash){
            $path=Get-UpgradePath $targetRoot $item.Name
            $stream=[IO.File]::Open($path,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
            $stream.Dispose()
        }
    }
    if($ValidateOnly){return [pscustomobject]@{Status='validated';Files=$items.Count;Target=$targetRoot;Backup=$null}}

    $id=[guid]::NewGuid().ToString('N')
    $stage=Join-Path $targetRoot ('.webdavmount-update-stage-'+$id)
    $backup=Join-Path $targetRoot ('WebDAVMount-upgrade-backup-'+$id)
    # Windows PowerShell 5.1 uses .NET Framework path limits for file copies.
    # Reject before creating working folders or changing any program file.
    foreach($item in $items){
        if((Get-UpgradePath $stage $item.Name).Length -ge 260 -or (Get-UpgradePath $backup $item.Name).Length -ge 260){
            throw 'Upgrade working paths exceed the Windows PowerShell 5.1 limit. Use a shorter installation path.'
        }
    }
    foreach($item in $backupOnly){
        if((Get-UpgradePath $backup $item.Name).Length -ge 260){throw 'Upgrade working paths exceed the Windows PowerShell 5.1 limit. Use a shorter installation path.'}
    }
    if((Test-Path -LiteralPath $stage) -or (Test-Path -LiteralPath $backup)){throw 'Upgrade working folder already exists.'}
    [IO.Directory]::CreateDirectory($stage)|Out-Null
    foreach($item in $items){
        $from=Get-UpgradePath $sourceRoot $item.Name;$to=Get-UpgradePath $stage $item.Name
        New-UpgradeParent $to;Copy-UpgradeDurable $from $to
        # Go module licenses can be read-only. Normalize our private staged copy
        # so cleanup and a later update can replace it; leave the source untouched.
        $attributes=[IO.File]::GetAttributes($to)
        [IO.File]::SetAttributes($to,($attributes -band (-bnot [IO.FileAttributes]::ReadOnly)))
        if((Get-UpgradeHash $to) -ne $item.Hash){throw 'Staging integrity verification failed. Existing program files have not changed.'}
    }
    [IO.Directory]::CreateDirectory($backup)|Out-Null
    foreach($item in $journalItems){
        if($null -ne $item.OldHash){
            $from=Get-UpgradePath $targetRoot $item.Name;$to=Get-UpgradePath $backup $item.Name
            if((Get-UpgradeHash $from) -ne $item.OldHash){throw 'Target files changed during validation. No update was applied.'}
            New-UpgradeParent $to;Copy-UpgradeDurable $from $to
            if((Get-UpgradeHash $to) -ne $item.OldHash){throw 'Backup verification failed. No update was applied.'}
        }
    }
    Write-UpgradeRecord $backup 'prepared' $journalItems $Registration
    if($CommitPoint){& $CommitPoint 'prepared'}
    $touched=[Collections.Generic.List[object]]::new();$entryMoved=$false
    $ordered=@($items|Where-Object{$_.Name -notin @('WebDAVMount.exe','SHA256SUMS.txt')})+@($items|Where-Object Name -eq 'SHA256SUMS.txt')+@($items|Where-Object Name -eq 'WebDAVMount.exe')
    try{
        Assert-UpgradeNotRunning $targetRoot
        $entry=Get-UpgradePath $targetRoot 'WebDAVMount.exe'
        # Keep the normal entry point unavailable until all supporting files are in place.
        Publish-UpgradeFile $entry (Get-UpgradePath $backup 'original-entrypoint.exe');$entryMoved=$true
        if($CommitPoint){& $CommitPoint 'entry-moved'}
        foreach($item in $ordered){
            $from=Get-UpgradePath $stage $item.Name;$to=Get-UpgradePath $targetRoot $item.Name
            New-UpgradeParent $to;$touched.Add($item)
            Copy-UpgradeDurable $from $to
            if((Get-UpgradeHash $to) -ne $item.Hash){throw 'Installed file integrity verification failed.'}
            if($AfterInstall){& $AfterInstall $item.Name}
        }
        if($BeforeComplete){& $BeforeComplete}
        Write-UpgradeRecord $backup 'complete' $journalItems $Registration
        if($CommitPoint){& $CommitPoint 'complete'}
    }catch{
        $failure=$_;$rollbackFailure=$null
        try{
            foreach($item in @($touched|Where-Object Name -ne 'WebDAVMount.exe')){
                $to=Get-UpgradePath $targetRoot $item.Name
                if($null -ne $item.OldHash){
                    $from=Get-UpgradePath $backup $item.Name
                    if((Get-UpgradeHash $from) -ne $item.OldHash){throw 'Backup integrity verification failed during rollback.'}
                    Copy-UpgradeDurable $from $to
                    if((Get-UpgradeHash $to) -ne $item.OldHash){throw 'Restored file verification failed.'}
                }elseif(Test-Path -LiteralPath $to -PathType Leaf){[IO.File]::Delete($to)}
            }
            if($entryMoved){
                $from=Get-UpgradePath $backup 'WebDAVMount.exe';$to=Get-UpgradePath $targetRoot 'WebDAVMount.exe'
                if((Get-UpgradeHash $from) -ne $old.Files['WebDAVMount.exe']){throw 'Entry point backup verification failed.'}
                Copy-UpgradeDurable $from $to
                if((Get-UpgradeHash $to) -ne $old.Files['WebDAVMount.exe']){throw 'Restored entry point verification failed.'}
            }
            if($Registration){
                $r=$Registration
                if(!(Test-UpgradeRegistryKey $r.key) -or $r.oldVersion -notmatch '^\d+\.\d+\.\d+$' -or $r.newVersion -notmatch '^\d+\.\d+\.\d+$'){throw 'Rollback registration invalid.'}
                $registryBase=[Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::CurrentUser,[Microsoft.Win32.RegistryView]::Registry64)
                $registryEntry=$null
                try{
                    $registryEntry=$registryBase.OpenSubKey($r.key,$true)
                    if(!$registryEntry -or !([string]$registryEntry.GetValue('InstallLocation')).Equals($targetRoot,[StringComparison]::OrdinalIgnoreCase) -or $registryEntry.GetValue('DisplayVersion') -notin @($r.oldVersion,$r.newVersion)){throw 'Rollback registration changed externally.'}
                    $registryEntry.SetValue('DisplayVersion',$r.oldVersion);$registryEntry.Flush()
                }finally{if($registryEntry){$registryEntry.Dispose()};$registryBase.Dispose()}
            }
            Write-UpgradeRecord $backup 'rolled_back' $journalItems $Registration
        }catch{$rollbackFailure=$_;try{Write-UpgradeRecord $backup 'rollback_failed' $journalItems $Registration}catch{}}
        if($rollbackFailure){throw ('Update failed and rollback was incomplete. Preserve backup: '+$backup)}
        throw ('Update failed; previous program files were restored. Backup: '+$backup+'; '+$failure.Exception.Message)
    }
    try{Remove-UpgradeStage $targetRoot $stage $items}catch{Write-Warning ('Update succeeded; staging cleanup was incomplete: '+$stage)}
    return [pscustomobject]@{Status='updated';Files=$items.Count;Target=$targetRoot;Backup=$backup}
    }finally{$mutex.ReleaseMutex();$mutex.Dispose()}
}

if($MyInvocation.InvocationName -ne '.'){
    if(!$TargetDirectory){throw 'Specify -TargetDirectory with the existing complete portable program folder.'}
    if($RecoverOnly){$root=Get-UpgradeRoot $TargetDirectory;$mutex=Get-UpgradeMutex $root;try{Restore-PendingUpgradeLocked $root}finally{$mutex.ReleaseMutex();$mutex.Dispose()}}
    else {Invoke-WebDAVMountUpdate -Source $SourceDirectory -Target $TargetDirectory -ValidateOnly:$CheckOnly}
}
