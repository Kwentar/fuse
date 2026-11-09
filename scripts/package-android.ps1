$ErrorActionPreference='Stop'
$project=Split-Path -Parent $PSScriptRoot
Push-Location $project
try {
    & "$PSScriptRoot/package-source.ps1" -OutputPath 'dist/android/fuse-0.4.2-source.zip'
    & "$PSScriptRoot/build-android.ps1"
    Copy-Item -LiteralPath docs/android.md -Destination dist/android/README.md
    Copy-Item -LiteralPath dist/android/SHA256-arm64.txt -Destination dist/android/SHA256.txt
    Compress-Archive -Path dist/android/fuse-0.4.2-preview-arm64.apk,dist/android/README.md,dist/android/SHA256-arm64.txt -DestinationPath dist/android/fuse-0.4.2-android.zip -Force
    Get-Item dist/android/fuse-0.4.2-preview-arm64.apk,dist/android/fuse-0.4.2-source.zip | Select-Object Name,Length
} finally {Pop-Location}
