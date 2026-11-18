param([string]$OutputPath='dist/fuse-source.zip')
$ErrorActionPreference='Stop'
$project=Split-Path -Parent $PSScriptRoot
Push-Location $project
try {
    $network=Get-Content -LiteralPath internal/config/default-network.json -Raw | ConvertFrom-Json
    if (@($network.bootstrap_nodes).Count -or @($network.relay_nodes).Count) {
        throw 'Public source must have empty bootstrap_nodes and relay_nodes. Keep private settings under .local.'
    }

    # Allow source files by location and type. Never copy arbitrary JSON, logs or reports.
    $files=@(Get-Item -LiteralPath README.md,go.mod,go.sum,.gitignore)
    $rules=@{
        'cmd'=@('.go'); 'internal'=@('.go'); 'tests'=@('.go')
        'gui'=@('.cs'); 'android'=@('.java','.xml')
        'macos'=@('.swift','.py','.plist','.html')
        'scripts'=@('.py','.ps1','.sh')
    }
    foreach($dir in $rules.Keys) {
        $files+=@(Get-ChildItem -LiteralPath $dir -Recurse -File | Where-Object {
            $relative=[IO.Path]::GetRelativePath($project,$_.FullName)
            $_.Extension -in $rules[$dir] -and
            $relative -notmatch '(^|[\\/])(?:__pycache__|\.local|profiles)[\\/]' -and
            $_.Name -notlike '*.local.*'
        })
    }
    $files+=@(Get-ChildItem configs/*.example.json,deploy/*.service,docs/*.md,specs/*.md -File)
    $files+=@(Get-Item internal/config/default-network.json,scripts/requirements-tools.txt)
    foreach($optional in @('AGENTS.md','REQUESTS.md','SPEC.md','BACKLOG.md','CHANGELOG.md','RUNLOG.md','LICENSE','NOTICE','.gitattributes','.env.example')) {
        if(Test-Path -LiteralPath $optional -PathType Leaf){$files+=Get-Item -LiteralPath $optional}
    }
    $files=@($files | Sort-Object FullName -Unique)
    foreach($file in $files) {
        if($file.Attributes -band [IO.FileAttributes]::ReparsePoint){throw "Symlink not allowed in source archive: $($file.Name)"}
        $entry=$file.Directory
        while($entry.FullName -ne $project) {
            if($entry.Attributes -band [IO.FileAttributes]::ReparsePoint){throw "Symlink not allowed in source archive: $($file.Name)"}
            $entry=$entry.Parent
            if($null -eq $entry){throw 'Source file is outside the project'}
        }
    }

    $output=[IO.Path]::GetFullPath((Join-Path $project $OutputPath))
    New-Item -ItemType Directory -Force (Split-Path -Parent $output) | Out-Null
    # ZipArchive includes .gitignore on every platform and uses portable / separators.
    $stream=[IO.File]::Open($output,[IO.FileMode]::Create)
    try {
        $zip=[IO.Compression.ZipArchive]::new($stream,[IO.Compression.ZipArchiveMode]::Create)
        try {
            foreach($file in $files) {
                $relative=[IO.Path]::GetRelativePath($project,$file.FullName).Replace('\','/')
                [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,$file.FullName,$relative,[IO.Compression.CompressionLevel]::Optimal) | Out-Null
            }
        } finally {$zip.Dispose()}
    } finally {$stream.Dispose()}
    Write-Output "Public source: $OutputPath ($($files.Count) files)"
} finally {Pop-Location}
