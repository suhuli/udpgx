param([ValidateSet("x64","x86")][string]$Architecture="x64", [switch]$BuiltinRedis)
$ErrorActionPreference="Stop"
$root=$PSScriptRoot
$tags=""
if($BuiltinRedis){
  $private=Join-Path $root ".private\redis-default.json"
  if(!(Test-Path $private)){throw "Private Redis config is required; credentials are not included in the public source."}
  $json=Get-Content $private -Raw | ConvertFrom-Json | ConvertTo-Json -Compress
  $encoded=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
  $source=@"
//go:build builtinredis

package config
import("encoding/base64";"encoding/json")
func builtinRedis()(Redis,bool){b,e:=base64.StdEncoding.DecodeString("$encoded");if e!=nil{return Redis{},false};var r Redis;if json.Unmarshal(b,&r)!=nil{return Redis{},false};return r,r.Host!=""}
"@
  [IO.File]::WriteAllText((Join-Path $root "core\internal\config\builtin_private.go"),$source,(New-Object Text.UTF8Encoding($false)))
  $tags="builtinredis"
}
$output=Join-Path $root "build\native-$Architecture"
New-Item -ItemType Directory -Force $output | Out-Null
$oldOS=$env:GOOS; $oldArch=$env:GOARCH; $oldCGO=$env:CGO_ENABLED
try {
  $env:GOOS="windows"; $env:GOARCH=if($Architecture -eq "x64"){"amd64"}else{"386"}; $env:CGO_ENABLED="0"
  Push-Location (Join-Path $root "core")
  foreach($role in @("provider","client")) {
    $name=if($role -eq "provider"){"Server"}else{"Client"}
    $argsList=@("build","-trimpath","-ldflags","-s -w -H=windowsgui -X main.defaultRole=$role","-o",(Join-Path $output "NorthLink-$name-v0.3.exe"))
    if($BuiltinRedis){$argsList+=@("-tags","builtinredis")}
    $argsList+="./cmd/northlink-native"
    & go @argsList
    if($LASTEXITCODE -ne 0){throw "$role build failed"}
  }
} finally { Pop-Location; $env:GOOS=$oldOS; $env:GOARCH=$oldArch; $env:CGO_ENABLED=$oldCGO }
Write-Host "Native single-file EXEs: $output"
Write-Host "No .NET, additional DLLs or installer required."
