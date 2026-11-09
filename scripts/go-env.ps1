$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$env:GOPATH = Join-Path $projectRoot '.tools/gopath'
$env:GOCACHE = Join-Path $projectRoot '.tools/gocache'
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
$portableGo = Join-Path $projectRoot '.tools/go/bin'
if (Test-Path -LiteralPath $portableGo) { $env:PATH = "$portableGo;$env:PATH" }
