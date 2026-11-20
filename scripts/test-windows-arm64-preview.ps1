#requires -Version 7.0
param([Parameter(Mandatory)][string]$OutputDirectory)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or
    [Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne 'Arm64') {
    throw 'This test installs WinFsp and must run on a disposable GitHub-hosted Windows ARM64 VM.'
}
$root = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $root) { throw 'Evidence directory must be new.' }
[IO.Directory]::CreateDirectory($root) | Out-Null
$record = [ordered]@{status='RUNNING'; osArchitecture='Arm64'; osBuild=[Environment]::OSVersion.Version.ToString(); checks=@()}
$children = @()
$release = 'https://github.com/makessr/WebDAVMount/releases/download/v0.72.7-arm64-preview.1'
$registry = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDAVMount-ARM64'

function Get-VerifiedFile([string]$Url, [string]$Name, [string]$Hash) {
    $destination = Join-Path $root $Name
    Invoke-WebRequest -Uri $Url -OutFile $destination -TimeoutSec 90 -MaximumRetryCount 2
    if ((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -ine $Hash) {
        throw "Download checksum mismatch: $Name"
    }
    return $destination
}

function Start-CheckedProcess([string]$Exe, [string[]]$Arguments, [int[]]$ExitCodes = @(0)) {
    $child = Start-Process -FilePath $Exe -ArgumentList $Arguments -WindowStyle Hidden -PassThru
    if (!$child.WaitForExit(90000)) { $child.Kill(); throw "Process timed out: $([IO.Path]::GetFileName($Exe))" }
    if ($child.ExitCode -notin $ExitCodes) { throw "Unexpected exit $($child.ExitCode): $([IO.Path]::GetFileName($Exe))" }
}

function Get-DependencyReport([string]$Executable, [string]$Name) {
    $path = Join-Path $root $Name
    Start-CheckedProcess $Executable @('-check-dependencies','-report',('"' + $path + '"')) @(0,2)
    Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
}

function Get-LoopbackAddress {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try { "127.0.0.1:$($listener.LocalEndpoint.Port)" } finally { $listener.Stop() }
}

function Wait-For([scriptblock]$Condition, [string]$Message) {
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    do {
        if (& $Condition) { return }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    throw $Message
}

try {
    if (Test-Path -LiteralPath $registry) { throw 'ARM64 is already installed on the test VM.' }
    $setup = Get-VerifiedFile "$release/WebDAVMount-0.72.7-arm64-setup.exe" 'setup.exe' 'dc46f65463b4cfce8343c255bcc051b1ac462860ab807b7307e6f2135b48d496'
    $archive = Get-VerifiedFile "$release/WebDAVMount-0.72.7-windows-arm64.zip" 'portable.zip' 'ff90d09dfdea9eb70c30bd821a6ec48b4247f646c65cd94e7e054d6db5e649e0'
    Expand-Archive -LiteralPath $archive -DestinationPath (Join-Path $root 'portable')
    $portable = Join-Path $root 'portable/WebDAVMount-0.72.7-windows-arm64/WebDAVMount.exe'
    $baseline = Get-DependencyReport $portable 'dependencies-before.json'
    if (!($baseline.components | Where-Object { $_.name -eq 'rclone' -and $_.ready })) { throw 'The bundled ARM64 rclone failed.' }
    $record.checks += 'verified-public-package-and-native-engine'

    if (!($baseline.components | Where-Object { $_.name -eq 'WinFsp' -and $_.ready })) {
        $msi = Get-VerifiedFile 'https://github.com/winfsp/winfsp/releases/download/v2.2/winfsp-2.2.26323.msi' 'winfsp.msi' '36ce01f9eedbbe6842b7fb4bedc79d7d4f71983e8aceb6877b46a07b331c7e19'
        if ((Get-AuthenticodeSignature -LiteralPath $msi).Status -ne 'Valid') { throw 'WinFsp MSI signature is not valid.' }
        Start-CheckedProcess (Join-Path $env:windir 'System32/msiexec.exe') @('/i',('"' + $msi + '"'),'/qn','/norestart','/L*v',('"' + (Join-Path $root 'winfsp-install.log') + '"'))
        $record.checks += 'missing-winfsp-detected-before-install'
    }

    $target = Join-Path $root 'ARM 安装'
    Start-CheckedProcess $setup @('/S',("/D=$target"))
    if ((Get-ItemProperty -LiteralPath $registry).InstallLocation -ne $target) { throw 'ARM64 installation registration mismatch.' }
    $exe = Join-Path $target 'WebDAVMount.exe'
    $engine = Join-Path $target 'rclone.exe'
    $deps = Get-DependencyReport $exe 'dependencies-after.json'
    if (!$deps.ready -or $deps.version -ne '0.72.7') { throw 'Installed dependencies failed, see dependencies-after.json.' }
    $record.checks += 'native-install-registration-and-dependency-load'

    $files = Join-Path $root 'fixture'
    [IO.Directory]::CreateDirectory($files) | Out-Null
    $expected = 'WebDAVMount ARM64 isolated fixture'
    [IO.File]::WriteAllText((Join-Path $files 'probe.txt'), $expected)
    $server = Get-LoopbackAddress
    $rc = Get-LoopbackAddress
    $config = Join-Path $root 'rclone-fixture.conf'
    [IO.File]::WriteAllText($config, "[fixture]`n type = webdav`n url = http://$server/`n vendor = other`n")
    $children += Start-Process -FilePath $engine -ArgumentList @('serve','webdav',('"' + $files + '"'),'--addr',$server,'--config','NUL') -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $root 'webdav.log')
    $children += Start-Process -FilePath $engine -ArgumentList @('rcd','--rc-addr',$rc,'--rc-user','fixture','--rc-pass','loopback-test','--config',('"' + $config + '"')) -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $root 'engine.log')
    $basic = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes('fixture:loopback-test'))
    $headers = @{Authorization="Basic $basic"}
    Wait-For {
        try {
            (Invoke-WebRequest -Uri "http://$server/probe.txt" -TimeoutSec 2).Content -eq $expected -and
                !!(Invoke-RestMethod -Uri "http://$rc/core/version" -Method Post -Headers $headers -TimeoutSec 2)
        } catch { $false }
    } 'Loopback WebDAV or rclone RC did not start.'

    $occupied = @(Get-PSDrive -PSProvider FileSystem | ForEach-Object Name)
    $letter = @('W','X','Y','Z','V' | Where-Object { $_ -notin $occupied } | Select-Object -First 1)
    if ($letter.Count -ne 1) { throw 'No isolated test drive is available.' }
    $drive = "$($letter[0]):"
    $body = @{fs='fixture:';mountPoint=$drive;mountType='cmount';mountOpt=@{ReadOnly=$true};vfsOpt=@{ReadOnly=$true;CacheMode=0}}
    Invoke-RestMethod -Uri "http://$rc/mount/mount" -Method Post -Headers $headers -ContentType 'application/json' -Body ($body | ConvertTo-Json -Compress) -TimeoutSec 25 | Out-Null
    Wait-For { [IO.File]::Exists("$drive\probe.txt") } 'WinFsp drive did not expose the fixture.'
    if ([IO.File]::ReadAllText("$drive\probe.txt") -cne $expected) { throw 'WinFsp read did not match the fixture.' }
    $writeRefused = $false
    try { [IO.File]::WriteAllText("$drive\must-not-write.txt", 'read-only test') }
    catch [IO.IOException] { $writeRefused = $true }
    catch [UnauthorizedAccessException] { $writeRefused = $true }
    if (!$writeRefused -or [IO.File]::Exists((Join-Path $files 'must-not-write.txt'))) { throw 'Read-only mount accepted a write.' }
    Invoke-RestMethod -Uri "http://$rc/mount/unmount" -Method Post -Headers $headers -ContentType 'application/json' -Body (@{mountPoint=$drive} | ConvertTo-Json -Compress) -TimeoutSec 20 | Out-Null
    Wait-For { $drive -notin [IO.DriveInfo]::GetDrives().Name.TrimEnd('\') } 'WinFsp drive remained after unmount.'
    foreach ($child in $children) { if (!$child.HasExited) { $child.Kill(); $child.WaitForExit(5000) | Out-Null } }
    $record.checks += 'native-winfsp-read-write-refusal-and-unmount'

    $marker = Join-Path $target 'synthetic-user-file.txt'
    [IO.File]::WriteAllText($marker, 'preserve')
    Start-CheckedProcess $setup @('/S')
    if ([IO.File]::ReadAllText($marker) -cne 'preserve') { throw 'Upgrade changed the extra file.' }
    $afterUpgrade = Get-DependencyReport $exe 'dependencies-upgrade.json'
    if (!$afterUpgrade.ready) { throw 'Dependencies failed after an in-place upgrade.' }
    $record.checks += 'in-place-reinstall-preserved-extra-file'

    $uninstaller = Join-Path $root 'uninstall-runner.exe'
    Copy-Item -LiteralPath (Join-Path $target 'uninstall.exe') -Destination $uninstaller
    Start-CheckedProcess $uninstaller @('/S',("_?=$target"))
    if ((Test-Path -LiteralPath $registry) -or (Test-Path -LiteralPath $exe) -or [IO.File]::ReadAllText($marker) -cne 'preserve') {
        throw 'ARM64 uninstall did not remove the program and preserve the extra file.'
    }
    $record.checks += 'registered-uninstall-preserved-extra-file'
    $record.status = 'PASS'
} catch {
    $record.status = 'FAIL'
    $record.failure = $_.Exception.Message
    throw
} finally {
    foreach ($child in $children) { if (!$child.HasExited) { $child.Kill(); $child.WaitForExit(5000) | Out-Null } }
    $record | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $root 'results.json') -Encoding UTF8
}
