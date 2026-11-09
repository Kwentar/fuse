$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$exe=Join-Path $root 'dist/gui/messenger.exe'
$folder=Join-Path $root ('.local/gui-integration-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $folder | Out-Null
function Invoke-Client($who, $arguments) {
    $output=& $exe --data-dir (Join-Path $folder $who) --json @arguments
    if($LASTEXITCODE){throw "Client failed: $who $($arguments[0]) $output"}
    @($output | ForEach-Object { $_ | ConvertFrom-Json })
}
function Wait-Ready($who) {
    for($i=0;$i -lt 90;$i++) {
        try {$s=Invoke-Client $who @('status');if($s.state -eq 'online'){return}}catch{}
        Start-Sleep -Seconds 1
    }
    throw 'Network readiness timeout'
}
$processes=@()
try {
    $a=(Invoke-Client 'a' @('init')).peer_id
    $b=(Invoke-Client 'b' @('init')).peer_id
    foreach($who in @('a','b')) {
        $dir=Join-Path $folder $who
        $config=Get-Content "$dir/config.json" -Raw | ConvertFrom-Json
        $config.smart_routing=$true
        $config.direct_listen_addrs=@('/ip4/127.0.0.1/tcp/0')
        $config | ConvertTo-Json -Depth 8 | Set-Content "$dir/config.json" -Encoding utf8NoBOM
        $processes+=Start-Process -FilePath $exe -ArgumentList "--data-dir `"$dir`" run" -PassThru -WindowStyle Hidden -RedirectStandardOutput "$folder/$who.log" -RedirectStandardError "$folder/$who.err"
    }
    Wait-Ready 'a'; Wait-Ready 'b'
    Invoke-Client 'a' @('add-contact',$b,'B') | Out-Null
    Invoke-Client 'b' @('add-contact',$a,'A') | Out-Null
    $message=Invoke-Client 'a' @('send',$b,'GUI relay test')
    if($message.status -ne 'delivered' -or $message.route -ne 'relay'){throw 'Text relay failed'}
    $path=Join-Path $folder '32MiB.bin'
    $file=[IO.File]::Create($path);$file.SetLength(32MB);$file.Dispose()
    $sent=Invoke-Client 'a' @('send-file',$b,$path)
    $end=(Get-Date).AddMinutes(8)
    do {
        Start-Sleep -Seconds 2
        $row=Invoke-Client 'a' @('files',$b) | Where-Object file_id -eq $sent.file_id
        if($row.status -eq 'paused'){throw "File paused: $($row.error)"}
    } while($row.status -ne 'delivered' -and (Get-Date) -lt $end)
    if($row.status -ne 'delivered' -or $row.route -ne 'relay'){throw 'File relay failed'}
    $received=Invoke-Client 'b' @('files',$a) | Where-Object file_id -eq $sent.file_id
    if((Get-FileHash $path).Hash -ne (Get-FileHash $received.path).Hash){throw 'SHA mismatch'}
    $result=@{text='delivered';file_bytes=$row.progress;route=$row.route;sha256='matched'}
    $result | ConvertTo-Json | Set-Content "$folder/results.json"
    $result | ConvertTo-Json
} finally {
    foreach($p in $processes){if(-not $p.HasExited){Stop-Process -Id $p.Id}}
}
