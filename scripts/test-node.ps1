param([string]$Run='.',[string]$Timeout='20m')
$ErrorActionPreference='Stop'
. "$PSScriptRoot/go-env.ps1"
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    New-Item -ItemType Directory -Force .local/tests | Out-Null
    go test -c -o .local/tests/node.test.exe ./internal/node
    if($LASTEXITCODE){throw 'Test compilation failed'}
    & .local/tests/node.test.exe '-test.v' "-test.run=$Run" "-test.timeout=$Timeout"
    if($LASTEXITCODE){throw 'Tests failed'}
} finally {Pop-Location}
