. "$PSScriptRoot/go-env.ps1"
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    & "$PSScriptRoot/package-source.ps1" -OutputPath 'dist/fuse-0.4.0-source.zip'
    & "$PSScriptRoot/build.ps1"
    & "$PSScriptRoot/package-desktop.ps1"
    $releaseDir=Join-Path (Get-Location) ('.local/package-'+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force $releaseDir | Out-Null
    Copy-Item -LiteralPath dist/messenger.exe,dist/bootstrap-node,README.md -Destination $releaseDir
    foreach($name in @('configs','deploy','docs','specs')) {
        New-Item -ItemType Directory -Force "$releaseDir/$name" | Out-Null
    }
    Copy-Item -Path configs/*.example.json -Destination "$releaseDir/configs"
    Copy-Item -Path deploy/*.service -Destination "$releaseDir/deploy"
    Copy-Item -Path docs/*.md -Destination "$releaseDir/docs"
    Copy-Item -Path specs/*.md -Destination "$releaseDir/specs"
    Compress-Archive -Path "$releaseDir/*" -DestinationPath dist/fuse-0.4.0.zip -Force
    $lines=foreach($name in @('messenger.exe','bootstrap-node','fuse-0.4.0.zip','fuse-0.4.0-source.zip','fuse-0.4.0-windows.zip')) {
        $digest=(Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path 'dist' $name)).Hash.ToLowerInvariant()
        "$digest  $name"
    }
    $lines | Set-Content -LiteralPath dist/SHA256SUMS.txt -Encoding ascii
    Get-Item dist/fuse-0.4.0.zip,dist/fuse-0.4.0-source.zip | Select-Object Name,Length
} finally {Pop-Location}
