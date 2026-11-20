# Gera os executáveis da pasta dist\ (Windows x64). Requer Go instalado.
# Uso: powershell -ExecutionPolicy Bypass -File scripts\build-dist.ps1
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")
New-Item -ItemType Directory -Force dist | Out-Null
$env:CGO_ENABLED = "0"; $env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o dist\Scorch.exe .\cmd\studio
go build -trimpath -ldflags "-s -w" -o dist\Scorch-Setup.exe .\cmd\setup
Write-Host "Gerado: dist\Scorch.exe e dist\Scorch-Setup.exe"
