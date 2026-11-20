#requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$Source,
      [Parameter(Mandatory=$true)][string]$Target,
      [Parameter(Mandatory=$true)][string]$Version,
      [Parameter(Mandatory=$true)][string]$RegistryKey)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
# NSIS can inherit PowerShell 7 module paths from its launcher. Windows
# PowerShell does not normalize that environment across this native boundary;
# resolve only its own built-in modules, never a user/PowerShell 7 shadow module.
$env:PSModulePath=$PSHOME+'\Modules'
$upgradeMutex=$null;$ownsMutex=$false
try {
    if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Invalid release version.'}
    if($RegistryKey -notin @('Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDAVMount','Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDAVMount-ARM64') -and
       $RegistryKey -notmatch '^Software\\WebDAVMount\\Tests\\Installer-[a-f0-9]{32}$'){throw 'Invalid registration key.'}
    . (Join-Path $Source 'Update-WebDAVMount.ps1')
    $targetRoot=Get-UpgradeRoot $Target
    if((Get-Command Get-UpgradePEMachine -CommandType Function -ErrorAction SilentlyContinue) -or $RegistryKey.EndsWith('-ARM64')){
        $sourceRoot=Get-UpgradeRoot $Source
        $sourceMachine=Get-UpgradePEMachine (Get-UpgradePath $sourceRoot 'WebDAVMount.exe')
        $targetMachine=Get-UpgradePEMachine (Get-UpgradePath $targetRoot 'WebDAVMount.exe')
        if($sourceMachine -notin @(0x8664,0xAA64) -or $sourceMachine -ne $targetMachine){throw 'Installed executable architecture does not match the upgrade.'}
    }
    $hasher=[Security.Cryptography.SHA256]::Create()
    try{$lockName=([BitConverter]::ToString($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($targetRoot.ToUpperInvariant())))).Replace('-','')}finally{$hasher.Dispose()}
    $upgradeMutex=[Threading.Mutex]::new($false,('Local\WebDAVMount-Installer-'+$lockName))
    try{$ownsMutex=$upgradeMutex.WaitOne(0)}catch [Threading.AbandonedMutexException]{$ownsMutex=$true}
    if(!$ownsMutex){throw 'Another upgrade is already using this installation.'}
    $base=[Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::CurrentUser,[Microsoft.Win32.RegistryView]::Registry64)
    try {
        $key=$base.OpenSubKey($RegistryKey,$true)
        if(!$key){throw 'Installation registration missing.'}
        try {
            $location=[string]$key.GetValue('InstallLocation')
            if(!(Get-UpgradeRoot $location).Equals($targetRoot,[StringComparison]::OrdinalIgnoreCase)){throw 'Installation registration does not match target.'}
            $recoveryMutex=Get-UpgradeMutex $targetRoot
            try{Restore-PendingUpgradeLocked $targetRoot}finally{$recoveryMutex.ReleaseMutex();$recoveryMutex.Dispose()}
            $oldVersion=[string]$key.GetValue('DisplayVersion')
            if($oldVersion -notmatch '^\d+\.\d+\.\d+$' -or [version]$oldVersion -gt [version]$Version){throw 'Downgrade or invalid installed version refused.'}
            # Only the version changes: same entry point, shortcuts, Run value and user data.
            $commit={
                try {
                    if([string]$key.GetValue('InstallLocation') -ne $location -or [string]$key.GetValue('DisplayVersion') -ne $oldVersion){throw 'Installation registration changed during upgrade.'}
                    $key.SetValue('DisplayVersion',$Version,[Microsoft.Win32.RegistryValueKind]::String)
                    $key.Flush()
                    if($key.GetValue('DisplayVersion') -ne $Version){throw 'Version registration verification failed.'}
                } catch {
                    # Restore only our own change; leave externally modified values alone.
                    if($key.GetValue('DisplayVersion') -eq $Version){$key.SetValue('DisplayVersion',$oldVersion);$key.Flush()}
                    throw
                }
            }
            Invoke-WebDAVMountUpdate -Source $Source -Target $targetRoot -InstalledPackage -BeforeComplete $commit -Registration @{key=$RegistryKey;oldVersion=$oldVersion;newVersion=$Version} | Out-Null
        } finally {$key.Dispose()}
    } finally {$base.Dispose()}
    exit 0
} catch {
    if($RegistryKey -match '^Software\\WebDAVMount\\Tests\\Installer-'){
        [IO.File]::WriteAllText((Join-Path $Source 'upgrade-debug.txt'),($PSVersionTable.PSVersion.ToString()+"`r`n"+$PSHOME+"`r`n"+$env:PSModulePath+"`r`n"+$_.ScriptStackTrace),[Text.Encoding]::Unicode)
    }
    # Local text for the installer, never a shell command or executable content.
    [IO.File]::WriteAllText((Join-Path $Source 'upgrade-error.txt'),$_.Exception.Message,[Text.Encoding]::Unicode)
    exit 7
} finally {
    if($upgradeMutex){if($ownsMutex){$upgradeMutex.ReleaseMutex()};$upgradeMutex.Dispose()}
}
