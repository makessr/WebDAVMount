#requires -Version 7.0
param(
    [Parameter(Mandatory)][string]$PreviewArchive,
    [Parameter(Mandatory)][string]$Compiler,
    [Parameter(Mandatory)][string]$OutputDirectory
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$expected = 'ff90d09dfdea9eb70c30bd821a6ec48b4247f646c65cd94e7e054d6db5e649e0'
if ((Get-FileHash -LiteralPath $PreviewArchive -Algorithm SHA256).Hash -ine $expected) {
    throw 'The input must be the published Windows ARM64 Preview 1 archive.'
}
$output = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $output) { throw 'Candidate output directory must be new.' }
[IO.Directory]::CreateDirectory($output) | Out-Null
$expanded = Join-Path $output 'input'
Expand-Archive -LiteralPath $PreviewArchive -DestinationPath $expanded
$baseName = 'WebDAVMount-0.72.7-windows-arm64'
$payload = Join-Path $expanded $baseName
$project = Split-Path $PSScriptRoot -Parent
$update = Join-Path $payload 'Update-WebDAVMount.ps1'
Copy-Item -LiteralPath (Join-Path $project 'build/portable/Update-WebDAVMount.ps1') -Destination $update
$manifest = Join-Path $payload 'SHA256SUMS.txt'
$hash = (Get-FileHash -LiteralPath $update -Algorithm SHA256).Hash.ToLowerInvariant()
$lines = @(Get-Content -LiteralPath $manifest)
$entry = '^[a-f0-9]{64}  Update-WebDAVMount\.ps1$'
if (@($lines | Where-Object { $_ -cmatch $entry }).Count -ne 1) { throw 'Cannot find updater entry in portable manifest.' }
[IO.File]::WriteAllLines($manifest, ($lines -creplace $entry, "$hash  Update-WebDAVMount.ps1"), [Text.UTF8Encoding]::new($false))
$portable = Join-Path $output "$baseName.zip"
Compress-Archive -LiteralPath $payload -DestinationPath $portable
$setup = Join-Path $output 'WebDAVMount-0.72.7-arm64-setup.exe'
$previousEpoch = $env:SOURCE_DATE_EPOCH
try {
    $env:SOURCE_DATE_EPOCH = '1795123200'
    & (Join-Path $PSScriptRoot 'installer.ps1') -Version 0.72.7 -Architecture arm64 -PortableArchive $portable -Compiler $Compiler -Output $setup | Out-Host
} finally {
    $env:SOURCE_DATE_EPOCH = $previousEpoch
}
[pscustomobject]@{
    Setup = $setup
    Portable = $portable
    Payload = $payload
    InstallerSha256 = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
    PayloadManifestSha256 = (Get-FileHash -LiteralPath $manifest -Algorithm SHA256).Hash.ToLowerInvariant()
}
