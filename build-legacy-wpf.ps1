param(
    [ValidateSet("x64", "x86")][string]$Architecture = "x64",
    [switch]$FrameworkDependent
)
$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$out = Join-Path $root "build\win"
if (Test-Path $out) { Remove-Item $out -Recurse -Force }
New-Item -ItemType Directory -Force $out | Out-Null
$selfContained = if ($FrameworkDependent) { "false" } else { "true" }
& dotnet publish (Join-Path $root "desktop\NorthLink.Desktop.csproj") -c Release -r "win-$Architecture" --self-contained $selfContained -o $out -p:PublishSingleFile=false
if ($LASTEXITCODE -ne 0) { throw "Desktop build failed" }
$oldOS=$env:GOOS; $oldArch=$env:GOARCH; $oldCGO=$env:CGO_ENABLED
try {
    $env:GOOS="windows"; $env:GOARCH=if($Architecture -eq "x64"){"amd64"}else{"386"}; $env:CGO_ENABLED="0"
    Push-Location (Join-Path $root "core")
    & go build -trimpath -ldflags "-s -w -H=windowsgui" -o (Join-Path $out "northlink-core.exe") ./cmd/northlink-core
    if ($LASTEXITCODE -ne 0) { throw "Network core build failed" }
} finally {
    Pop-Location
    $env:GOOS=$oldOS; $env:GOARCH=$oldArch; $env:CGO_ENABLED=$oldCGO
}
Copy-Item (Join-Path $out "NorthLink.exe") (Join-Path $out "NorthLink.Provider.exe") -Force
Copy-Item (Join-Path $out "NorthLink.exe") (Join-Path $out "NorthLink.Client.exe") -Force
Remove-Item (Join-Path $out "NorthLink.exe") -Force
Copy-Item (Join-Path $root "desktop\northlink.ico") $out -Force
Copy-Item (Join-Path $root "README.md") $out -Force
Copy-Item (Join-Path $root "LICENSE") $out -Force
Copy-Item (Join-Path $root "docs\NorthLink-Guide.html") $out -Force
Copy-Item (Join-Path $root "docs\THIRD-PARTY-NOTICES.txt") $out -Force
New-Item -ItemType Directory -Force (Join-Path $out "docs") | Out-Null
foreach($doc in @("ARCHITECTURE.md", "VALIDATION.md", "test-results.txt")) {
    Copy-Item (Join-Path $root "docs\$doc") (Join-Path $out "docs\$doc") -Force
}
$zip=Join-Path $root "build\NorthLink-Windows-$Architecture.zip"
if(Test-Path $zip){Remove-Item $zip -Force}
Compress-Archive -Path (Join-Path $out "*") -DestinationPath $zip -CompressionLevel Optimal
Write-Host "Built: $zip"
