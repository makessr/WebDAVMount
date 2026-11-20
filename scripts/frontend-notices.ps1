function Get-FrontendNoticeInputs([string]$Project,[string]$Executable,[string]$Version) {
    $result = & node (Join-Path $Project 'frontend/build/dependency-inventory.mjs') select $Project $Executable
    if ($LASTEXITCODE -ne 0) { throw 'Frontend notice inventory verification failed' }
    $selection = $result | ConvertFrom-Json
    if($Version -and [version]$Version -gt [version]'0.25.0' -and $selection.mode -ne 'bundle-inventory') { throw 'New releases require a matching frontend build receipt' }
    return $selection
}
