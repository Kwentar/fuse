$ErrorActionPreference='Stop'
$project=Split-Path -Parent $PSScriptRoot
Push-Location $project
try {
    & "$PSScriptRoot/build-gui.ps1"
    $release='dist/windows-0.4'
    Copy-Item -LiteralPath README.md -Destination "$release/README.md"
    Copy-Item -LiteralPath docs/gui-files.md -Destination "$release/FILES.md"
    # Top-level documentation only; docs/reports contains private local output.
    $stage=Join-Path $project ('.local/desktop-package-'+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force "$stage/docs","$stage/specs" | Out-Null
    Copy-Item -LiteralPath "$release/fuse.exe","$release/messenger.exe","$release/README.md","$release/FILES.md" -Destination $stage
    Copy-Item -Path docs/*.md -Destination "$stage/docs"
    Copy-Item -Path specs/*.md -Destination "$stage/specs"
    $lines=foreach($name in @('fuse.exe','messenger.exe')) {
        $hash=(Get-FileHash -Algorithm SHA256 -LiteralPath "$release/$name").Hash.ToLowerInvariant()
        "$hash  $name"
    }
    $lines | Set-Content "$release/SHA256SUMS.txt" -Encoding ascii
    Copy-Item -LiteralPath "$release/SHA256SUMS.txt" -Destination $stage
    Compress-Archive -Path "$stage/*" -DestinationPath dist/fuse-0.4.0-windows.zip -Force
    (Get-FileHash -Algorithm SHA256 dist/fuse-0.4.0-windows.zip).Hash.ToLowerInvariant() | Set-Content dist/SHA256-windows-0.4.txt -Encoding ascii
    Get-Item dist/fuse-0.4.0-windows.zip | Select-Object FullName,Length
} finally {Pop-Location}
