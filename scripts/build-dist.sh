#!/usr/bin/env sh
# Gera os executáveis da pasta dist/ (Windows x64). Requer Go instalado.
# Uso: sh scripts/build-dist.sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p dist
export CGO_ENABLED=0 GOOS=windows GOARCH=amd64
go build -trimpath -ldflags "-s -w" -o dist/Scorch.exe ./cmd/studio
go build -trimpath -ldflags "-s -w" -o dist/Scorch-Setup.exe ./cmd/setup
echo "Gerado: dist/Scorch.exe e dist/Scorch-Setup.exe"
