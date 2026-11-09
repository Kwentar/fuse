param([switch]$Test)
. "$PSScriptRoot/go-env.ps1"
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    if ((go env GOVERSION) -ne 'go1.27.3') { throw 'Build requires Go 1.27.3' }
    if ($Test) { go test ./... -count=1; if ($LASTEXITCODE) { throw 'Tests failed' } }
    New-Item -ItemType Directory -Force dist | Out-Null
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
    go build -trimpath -ldflags '-s -w' -o dist/messenger.exe ./cmd/client
    if ($LASTEXITCODE) { throw 'Windows build failed' }
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'
    go build -trimpath -ldflags '-s -w' -o dist/bootstrap-node ./cmd/bootstrap
    if ($LASTEXITCODE) { throw 'Linux build failed' }
} finally { Remove-Item Env:GOOS -ErrorAction SilentlyContinue; Remove-Item Env:GOARCH -ErrorAction SilentlyContinue; Pop-Location }
