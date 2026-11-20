#requires -Version 7.0
param([string]$OutputDirectory)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$project=Split-Path $PSScriptRoot -Parent
$activate=Join-Path $env:LOCALAPPDATA 'Programs/MultiPanDev/Activate.ps1'
if(Test-Path $activate){. $activate}
$policy=Join-Path $project 'build/engine-nosmb'
$spec=Get-Content (Join-Path $policy SPEC.json) -Raw|ConvertFrom-Json
if((go version) -cne $spec.goVersion){throw 'Pinned Go toolchain required'}
if((Get-FileHash (Join-Path $policy BACKEND-ALL.txt)).Hash -cne $spec.backendSHA256 -or (Get-FileHash (Join-Path $policy MODULES.json)).Hash -cne $spec.modulesSHA256){throw 'Engine policy files changed'}
if((Get-FileHash (Join-Path $policy MOUNT-RC.txt)).Hash -cne $spec.mountRCSHA256){throw 'Engine mount patch changed'}
if(!$OutputDirectory){$OutputDirectory=Join-Path $project 'tools/rclone-nosmb-v1.76.0-remount1-proxy2'}
$OutputDirectory=[IO.Path]::GetFullPath($OutputDirectory)
if(Test-Path -LiteralPath $OutputDirectory){throw 'Engine output already exists; choose a fresh directory'}
[IO.Directory]::CreateDirectory($OutputDirectory)|Out-Null
if(!$env:HTTPS_PROXY){$proxy=[Net.WebRequest]::GetSystemWebProxy().GetProxy([Uri]'https://proxy.golang.org');if($proxy.Host -eq '127.0.0.1' -and !$proxy.UserInfo){$env:HTTPS_PROXY=$proxy.AbsoluteUri;$env:HTTP_PROXY=$proxy.AbsoluteUri}}
$env:GOWORK='off';$env:CGO_ENABLED='0';$env:GOOS='windows';$env:GOARCH='amd64';$env:GOAMD64='v1';$env:GOFLAGS='';$env:GOEXPERIMENT='';$env:GOTOOLCHAIN='local'
$download=(& go -C $project mod download -json ($spec.sourceModule+'@'+$spec.sourceVersion)) -join "`n"
if($LASTEXITCODE -ne 0){throw 'Pinned source download failed'}
$source=$download|ConvertFrom-Json
if($source.Sum -cne $spec.sourceSum -or $source.Path -cne $spec.sourceModule -or $source.Version -cne $spec.sourceVersion -or (Get-FileHash $source.Zip).Hash -cne $spec.sourceZipSHA256){throw 'Pinned source checksum mismatch'}
$work=Join-Path $OutputDirectory source
Copy-Item -LiteralPath $source.Dir -Destination $work -Recurse
if((Get-FileHash (Join-Path $work 'backend/all/all.go')).Hash -cne $spec.originalBackendSHA256){throw 'Upstream registration source changed'}
if((Get-FileHash (Join-Path $work 'cmd/mountlib/rc.go')).Hash -cne $spec.originalMountRCSHA256){throw 'Upstream mount registry source changed'}
foreach($name in @('SPEC.json','MODULES.json','BACKEND-ALL.txt','MOUNT-RC.txt')){Copy-Item (Join-Path $policy $name) (Join-Path $OutputDirectory $name)}
$overlay=Join-Path $OutputDirectory overlay.json
function Replace-SourceText([string]$text,[string]$before,[string]$after) {
 $first=$text.IndexOf($before,[StringComparison]::Ordinal)
 if($first -lt 0 -or $text.IndexOf($before,$first+$before.Length,[StringComparison]::Ordinal) -ge 0){throw 'Engine patch anchor must match exactly once'}
 return $text.Replace($before,$after)
}
$httpPath=Join-Path $work 'fs/fshttp/http.go'
$httpText=[IO.File]::ReadAllText($httpPath).Replace("`r`n","`n")
$direct=@'
if ci.HTTPProxy == "direct" {
		t.Proxy = nil
	} else if ci.HTTPProxy != "" {
'@
$httpText=Replace-SourceText $httpText 'if ci.HTTPProxy != "" {' $direct
$exceptLocal=@'
t.Proxy = func(req *http.Request) (*url.URL, error) {
				host := req.URL.Hostname()
				if strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback() {
					return nil, nil
				}
				return proxyURL, nil
			}
'@
$httpText=Replace-SourceText $httpText 't.Proxy = http.ProxyURL(proxyURL)' $exceptLocal
$megaPath=Join-Path $work 'backend/mega/mega.go'
$megaText=[IO.File]::ReadAllText($megaPath).Replace("`r`n","`n")
$megaText=Replace-SourceText $megaText 'srv := megaCache[opt.User]' 'srv := megaCache[name]'
$megaText=Replace-SourceText $megaText 'megaCache[opt.User] = srv' 'megaCache[name] = srv'
$azurePath=Join-Path $work 'backend/azureblob/auth/auth.go'
$azureText=[IO.File]::ReadAllText($azurePath).Replace("`r`n","`n")
$azureText=Replace-SourceText $azureText 'RoundTripper: fshttp.NewTransport(ctx),' 'RoundTripper: fshttp.NewTransportCustom(ctx, nil),'
$azurePatch=Join-Path $OutputDirectory 'azure-auth-overlay.go'
[IO.File]::WriteAllText($azurePatch,$azureText)
$httpPatch=Join-Path $OutputDirectory 'http-overlay.go'
$megaPatch=Join-Path $OutputDirectory 'mega-overlay.go'
[IO.File]::WriteAllText($httpPatch,$httpText)
[IO.File]::WriteAllText($megaPatch,$megaText)
@{Replace=@{(Join-Path $work 'backend/all/all.go')=(Join-Path $OutputDirectory BACKEND-ALL.txt);(Join-Path $work 'cmd/mountlib/rc.go')=(Join-Path $OutputDirectory MOUNT-RC.txt);$httpPath=$httpPatch;$megaPath=$megaPatch;$azurePath=$azurePatch}}|ConvertTo-Json|Set-Content $overlay
$binary=Join-Path $OutputDirectory rclone.exe
$link='-X github.com/rclone/rclone/fs.VersionSuffix='+$spec.versionSuffix
& go -C $work build -mod=readonly -buildvcs=false -trimpath -tags cmount -ldflags $link -overlay $overlay -o $binary .
if($LASTEXITCODE -ne 0){throw 'Custom engine build failed'}
go version -m $binary|Set-Content (Join-Path $OutputDirectory BUILD-INFO.txt)
$runtime=@(& $binary version --config=)
if($LASTEXITCODE -ne 0 -or $runtime[0].Trim() -cne ('rclone '+$spec.sourceVersion+'-'+$spec.versionSuffix)){throw 'Built engine runtime label mismatch'}
Copy-Item -LiteralPath $PSCommandPath -Destination (Join-Path $OutputDirectory REBUILD.ps1)
$readme=@'
WebDAVMount custom rclone engine (not an official rclone binary)
Source: github.com/rclone/rclone v1.76.0, https://github.com/rclone/rclone
Modifications: exclude SMB backend registration; prevent a delayed old mount
Wait callback from deleting a replacement mount's registry entry. REBUILD.ps1
applies a direct HTTP routing override, isolates MEGA sessions per remote,
and gives each Azure Blob client a transport built from its own configuration.
Runtime suffix: webdavmount-nosmb-remount1-proxy2.
Go main build metadata: (devel). ENGINE.json, SPEC.json, MODULES.json,
BACKEND-ALL.txt and MOUNT-RC.txt record the exact source and modifications.
Source ZIP and the four reviewed MPL source modules accompany the trial separately.
Hash checks verify local integrity, not publisher authentication or legal clearance.

'@
$readme+=[IO.File]::ReadAllText((Join-Path $source.Dir COPYING))
[IO.File]::WriteAllText((Join-Path $OutputDirectory README.txt),$readme,[Text.UTF8Encoding]::new($false))
$manifest=[ordered]@{kind='nosmb';sourceVersion=$spec.sourceVersion;sourceSum=$spec.sourceSum;sourceZipSHA256=$spec.sourceZipSHA256;goVersion=(go version);goExecutableSha256=(Get-FileHash (Get-Command go -CommandType Application).Source).Hash.ToLowerInvariant();engineSha256=(Get-FileHash $binary).Hash.ToLowerInvariant();linkerFlags=$link;runtimeVersion=$runtime[0].Trim();files=@(foreach($name in @('SPEC.json','MODULES.json','BACKEND-ALL.txt','MOUNT-RC.txt','BUILD-INFO.txt','README.txt','REBUILD.ps1')){@{name=$name;sha256=(Get-FileHash (Join-Path $OutputDirectory $name)).Hash.ToLowerInvariant()}})}
$manifest|ConvertTo-Json -Depth 6|Set-Content (Join-Path $OutputDirectory ENGINE.json)
. (Join-Path $PSScriptRoot engine-identity.ps1)
$identity=Get-EngineIdentity $binary (Join-Path $OutputDirectory ENGINE.json)
Write-Output ('Custom engine verified: '+$identity.Version+'; '+$manifest.engineSha256+'; '+$OutputDirectory)
