param([string]$OutputDir='dist/windows-0.4')
. "$PSScriptRoot/go-env.ps1"
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    New-Item -ItemType Directory -Force $OutputDir | Out-Null
    go build -trimpath -ldflags '-s -w' -o "$OutputDir/messenger.exe" ./cmd/client
    if ($LASTEXITCODE) { throw 'Engine build failed' }
    $guiOut='/out:'+([IO.Path]::GetFullPath((Join-Path $OutputDir 'fuse.exe')))
    & 'C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe' /nologo /target:winexe /platform:x64 $guiOut /reference:System.Windows.Forms.dll /reference:System.Drawing.dll /reference:System.Web.Extensions.dll /reference:System.Core.dll '.\gui\Messenger.cs'
    if ($LASTEXITCODE) { throw 'GUI build failed' }
} finally { Pop-Location }
