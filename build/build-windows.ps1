# Builds PowerDistributionSystem.exe on Windows (requires Go 1.24+ from https://go.dev/dl/).
# Usage:  powershell -ExecutionPolicy Bypass -File build\build-windows.ps1 [-Version 1.0.0]
param([string]$Version = "1.0.0")
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")
New-Item -ItemType Directory -Force -Path dist | Out-Null
$env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=$Version" -o dist\PowerDistributionSystem.exe .\cmd\pds
Write-Host "Built dist\PowerDistributionSystem.exe ($Version)"
