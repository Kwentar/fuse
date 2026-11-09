param([string]$NetworkConfig='internal/config/default-network.json')
$ErrorActionPreference='Stop'
$project=Split-Path -Parent $PSScriptRoot
Push-Location $project
$processes=@()
try {
    $network=Get-Content -LiteralPath $NetworkConfig -Raw | ConvertFrom-Json
    if(-not @($network.relay_nodes).Count){throw 'Configure your own relay before running this test; use -NetworkConfig PATH and build the APK with the same network.'}
    $adb=Join-Path $project '.tools/android/sdk/platform-tools/adb.exe'
    $exe=Join-Path $project 'dist/gui/messenger.exe'
    $folder=Join-Path $project ('.local/android-integration-'+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory $folder | Out-Null
    $data=Join-Path $folder 'windows'
    function Client($arguments){$out=& $exe --data-dir $data --json @arguments;if($LASTEXITCODE){throw "Client failed: $out"};@($out|ForEach-Object{$_|ConvertFrom-Json})}
    $winID=(Client @('init')).peer_id
    $configPath=Join-Path $data 'config.json'
    $config=Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $config.bootstrap_nodes=@($network.bootstrap_nodes)
    $config.relay_nodes=@($network.relay_nodes)
    [IO.File]::WriteAllText($configPath,($config|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
    $processes+=Start-Process $exe -ArgumentList "--data-dir `"$data`" run" -PassThru -WindowStyle Hidden -RedirectStandardOutput "$folder/windows.log" -RedirectStandardError "$folder/windows.err"
    & $adb -s emulator-5554 install -r dist/android/fuse-0.4.2-preview-emulator-x86_64.apk
    if($LASTEXITCODE){throw 'APK install failed'}
    & $adb -s emulator-5554 shell pm grant org.p2pmessenger.app android.permission.POST_NOTIFICATIONS
    $test=Start-Process $adb -ArgumentList "-s emulator-5554 shell am instrument -w -e peer $winID org.p2pmessenger.app/.SmokeTest" -PassThru -WindowStyle Hidden -RedirectStandardOutput "$folder/test.log" -RedirectStandardError "$folder/test.err"
    $processes+=$test
    $end=(Get-Date).AddSeconds(120);$androidID=''
    do {
        Start-Sleep -Seconds 1
        $log=Get-Content "$folder/test.log" -Raw -ErrorAction SilentlyContinue
        if($log -match 'peer_id=(12D3KooW\w+)'){$androidID=$Matches[1];break}
        if($test.HasExited){throw "Instrumentation exited: $log"}
    }while((Get-Date)-lt $end)
    if(-not $androidID){throw 'No Android ID'}
    Client @('add-contact',$androidID,'Android test') | Out-Null
    do {
        Start-Sleep -Seconds 1
        $log=Get-Content "$folder/test.log" -Raw
        if($log -match 'network=online'){break}
        if($test.HasExited){throw "Instrumentation exited: $log"}
    }while((Get-Date)-lt $end)
    if($log -notmatch 'network=online'){throw "Android did not become online: $log"}
    $found=$false
    $findUntil=(Get-Date).AddSeconds(45)
    do {
        try {$discovered=Client @('find',$androidID);if($discovered.addresses.Count -gt 0){$found=$true;break}}catch{}
        Start-Sleep -Seconds 1
    }while((Get-Date)-lt $findUntil)
    if(-not $found){throw 'Android contact was not published to the new relay DHT'}
    $reply=Client @('send',$androidID,'Hello from Windows')
    if($reply.status -ne 'delivered'){throw 'Windows text not delivered'}
    $source=Join-Path $folder 'from-windows.bin';$f=[IO.File]::Create($source);$f.SetLength(20MB);$f.Dispose()
    $sent=Client @('send-file',$androidID,$source)
    $end=(Get-Date).AddMinutes(5)
    while(-not $test.HasExited -and (Get-Date)-lt $end){Start-Sleep -Seconds 2}
    $log=Get-Content "$folder/test.log" -Raw
    if($log -notmatch 'result=PASS'){throw "Android test failed: $log"}
    $rows=Client @('files',$androidID)
    $incoming=$rows|Where-Object { $_.direction -eq 'in' -and $_.name -eq 'from-android.bin' }
    $outgoing=$rows|Where-Object file_id -eq $sent.file_id
    if($incoming.status -ne 'stored' -or $outgoing.status -ne 'delivered'){throw 'Windows file states invalid'}
    if((Get-FileHash $incoming.path).Hash.ToLowerInvariant() -ne $incoming.sha256){throw 'Windows inbound hash mismatch'}
    $results=@{android='PASS';text='both directions';file_bytes_each=20MB;sha256='verified at both receivers';outgoing_route=$outgoing.route;incoming_route=$incoming.route;folder=$folder}
    $results|ConvertTo-Json|Set-Content "$folder/results.json"
    $results|ConvertTo-Json
} finally {foreach($p in $processes){if(-not $p.HasExited){Stop-Process -Id $p.Id}};Pop-Location}
