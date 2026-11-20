# Shared build/notice/package identity checks. Hashes establish local integrity,
# not publisher authentication or final redistribution compliance.
function Get-EngineIdentity {
    param([string]$Engine,[string]$EngineManifest)
    $project=Split-Path $PSScriptRoot -Parent
    $lines=@(& go version -m $Engine)
    if($LASTEXITCODE -ne 0){throw 'Cannot inspect engine build information'}
    $mainVersion=$null;$mainSum=$null;$deps=@{};$settings=@{}
    foreach($line in $lines){
        if($line -match '^\s*=>\s'){throw 'Replaced engine modules refused'}
        if($line -match '^\s*mod\s+github.com/rclone/rclone\s+(\S+)(?:\s+(h1:\S+))?'){$mainVersion=$Matches[1];if($Matches.ContainsKey(2)){$mainSum=$Matches[2]}}
        if($line -match '^\s*dep\s+(\S+)\s+(\S+)\s+(h1:\S+)'){if($deps.ContainsKey($Matches[1])){throw 'Duplicate embedded engine module'};$deps[$Matches[1]]=@($Matches[2],$Matches[3])}
        if($line -match '^\s*build\s+(\S+?)=(.*)$'){$settings[$Matches[1]]=$Matches[2].Trim('"')}
    }
    $hash=(Get-FileHash -LiteralPath $Engine).Hash.ToLowerInvariant()
    if($mainVersion -match '^v\d+\.\d+\.\d+'){
        $distribution=Get-ChildItem (Join-Path $project tools) -Directory -Filter 'rclone-*'|Where-Object {(Test-Path (Join-Path $_.FullName rclone.exe)) -and (Get-FileHash (Join-Path $_.FullName rclone.exe)).Hash.ToLowerInvariant() -eq $hash}|Select-Object -First 1
        if(!$distribution){throw 'Matching official rclone distribution required'}
        return [pscustomobject]@{Kind='official';Version=$mainVersion;MainSum=$mainSum;Modules=$deps;Readme=(Join-Path $distribution.FullName README.txt);Directory=$distribution.FullName;Manifest=$null;Collection=(Join-Path $project ('build/rclone-notices/'+$mainVersion));SourceNotice=(Join-Path $project 'build/rclone-review/NOTICE.txt')}
    }
    if($mainVersion -cne '(devel)'){throw 'Unknown engine main module identity'}
    if(!$EngineManifest){
        $EngineManifest=foreach($variant in @('rclone-v1.76.0-nosmb-remount1-proxy2-writeback2-move1','rclone-v1.76.0-nosmb-remount1-proxy2-writeback2','rclone-nosmb-v1.76.0-remount1-proxy2')){
            $candidate=Join-Path $project ('tools/'+$variant+'/ENGINE.json')
            if((Test-Path -LiteralPath $candidate) -and ((Get-Content -LiteralPath $candidate -Raw|ConvertFrom-Json).engineSha256 -ceq $hash)){
                $candidate
                break
            }
        }
    }
    if(!$EngineManifest -or !(Test-Path -LiteralPath $EngineManifest)){throw 'Custom engine provenance required'}
    $manifest=Get-Content -LiteralPath $EngineManifest -Raw|ConvertFrom-Json
    $directory=Split-Path ([IO.Path]::GetFullPath($EngineManifest)) -Parent
    $specRoot=Join-Path $project 'build/engine-nosmb'
    $spec=Get-Content (Join-Path $specRoot SPEC.json) -Raw|ConvertFrom-Json
    if($manifest.kind -cne 'nosmb' -or $manifest.engineSha256 -cne $hash){throw 'Custom engine hash/kind mismatch'}
    if($manifest.sourceVersion -cne $spec.sourceVersion -or $manifest.sourceSum -cne $spec.sourceSum -or $manifest.sourceZipSHA256 -cne $spec.sourceZipSHA256){throw 'Custom source identity mismatch'}
    $rename=$manifest.runtimeVersion -ceq ('rclone '+$spec.sourceVersion+'-'+$spec.versionSuffix+'-writeback2-move1')
    $writeback=$rename -or $manifest.runtimeVersion -ceq ('rclone '+$spec.sourceVersion+'-'+$spec.versionSuffix+'-writeback2')
    if(!$writeback -and $manifest.runtimeVersion -cne ('rclone '+$spec.sourceVersion+'-'+$spec.versionSuffix)){throw 'Custom engine version label mismatch'}
    $policyInputs=@('SPEC.json','MODULES.json','BACKEND-ALL.txt','MOUNT-RC.txt')
    $collection='v1.76.0-nosmb-remount1-proxy2'
    if($writeback){
        $spec.versionSuffix += '-writeback2'
        $collection += '-writeback2'
        $policyInputs += @('apply-writeback.cjs','WEBDAV-WRITEBACK.txt')
    }
    $renameInputs=@()
    if($rename){
        $spec.versionSuffix += '-move1'
        $collection += '-move1'
        $renameInputs=@('apply-mounted-move.cjs','WEBDAV-MOVE.txt','VFS-MOVE.txt','CACHE-MOVE.txt')
        $policyInputs += $renameInputs
    }
    $required=$policyInputs+@('BUILD-INFO.txt','README.txt','REBUILD.ps1')
    $files=@{};foreach($file in $manifest.files){
        if($file.name -notin $required -or $files.ContainsKey($file.name)){throw 'Unknown or duplicate provenance file'}
        $path=Join-Path $directory $file.name
        $ancestor=[IO.Path]::GetFullPath($path)
        while($ancestor){if(([IO.File]::GetAttributes($ancestor) -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Reparse provenance path refused'};$ancestor=[IO.Path]::GetDirectoryName($ancestor)}
        if((Get-FileHash $path).Hash.ToLowerInvariant() -cne $file.sha256){throw 'Custom provenance file changed'}
        $files[$file.name]=$true
    }
    if($files.Count -ne $required.Count){throw 'Incomplete custom provenance'}
    foreach($name in $policyInputs){
        $inputRoot=if($name -in $renameInputs -and !(Test-Path -LiteralPath (Join-Path $specRoot $name))){Join-Path $specRoot 'candidate-move'}else{$specRoot}
        if((Get-FileHash (Join-Path $directory $name)).Hash -cne (Get-FileHash (Join-Path $inputRoot $name)).Hash){throw 'Custom build policy mismatch'}
    }
    if((Get-FileHash (Join-Path $directory MOUNT-RC.txt)).Hash -cne $spec.mountRCSHA256){throw 'Custom mount patch identity mismatch'}
    if($manifest.goVersion -cne $spec.goVersion -or $lines[0] -notmatch ': go1\.27\.4$'){throw 'Custom engine toolchain mismatch'}
    $architecture=if($manifest.PSObject.Properties['architecture']){$manifest.architecture}else{$spec.goarch}
    if($architecture -cnotin @('amd64','arm64')){throw 'Unsupported engine architecture'}
    $cpu=if($architecture -ceq 'arm64'){@('GOARM64','v8.0')}else{@('GOAMD64',$spec.goamd64)}
    foreach($pair in @(@('GOOS',$spec.goos),@('GOARCH',$architecture),$cpu,@('CGO_ENABLED','0'),@('-tags',$spec.tags),@('-trimpath','true'))){if(!$settings.ContainsKey($pair[0]) -or $settings[$pair[0]] -cne $pair[1]){throw 'Custom engine build settings mismatch'}}
    # Go intentionally omits -ldflags from build-info under -trimpath. Keep
    # the pinned command plus the builder's observed runtime label in provenance.
    $link='-X github.com/rclone/rclone/fs.VersionSuffix='+$spec.versionSuffix
    if($manifest.linkerFlags -cne $link -or $manifest.runtimeVersion -cne ('rclone '+$spec.sourceVersion+'-'+$spec.versionSuffix)){throw 'Custom engine version label mismatch'}
    if($settings.ContainsKey('-ldflags') -and $settings['-ldflags'] -cne $link){throw 'Custom engine linker settings mismatch'}
    $policy=@(Get-Content (Join-Path $specRoot MODULES.json) -Raw|ConvertFrom-Json)
    if($deps.Count -ne $spec.externalModules -or $policy.Count -ne $deps.Count){throw 'Custom engine dependency inventory mismatch'}
    foreach($m in $policy){if(!$deps.ContainsKey($m.name) -or $deps[$m.name][0] -cne $m.version -or $deps[$m.name][1] -cne $m.sum){throw 'Custom dependency checksum mismatch'}}
    foreach($name in $spec.excluded){if($deps.ContainsKey($name)){throw 'Excluded module present in engine'}}
    $mainSum=$spec.sourceSum
    return [pscustomobject]@{Kind='nosmb';Architecture=$architecture;Version=$spec.sourceVersion;MainSum=$mainSum;Modules=$deps;Readme=(Join-Path $directory README.txt);Directory=$directory;Manifest=([IO.Path]::GetFullPath($EngineManifest));Collection=(Join-Path $project ('build/rclone-notices/'+$collection));PolicyFiles=$policyInputs;ProvenanceFiles=(@('ENGINE.json')+$required);SourceNotice=(Join-Path $project 'build/rclone-review/NOSMB-NOTICE.txt')}
}
