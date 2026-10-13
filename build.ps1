param([ValidateSet("x64","x86")][string]$Architecture="x64",[switch]$BuiltinRedis)
& "$PSScriptRoot\build-native.ps1" -Architecture $Architecture -BuiltinRedis:$BuiltinRedis
