$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Output = Join-Path $Root "dist\java-game-launcher-windows-amd64.exe"
New-Item -ItemType Directory -Force (Split-Path -Parent $Output) | Out-Null
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o $Output $Root
Write-Host "Built $Output"
