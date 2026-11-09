# One-time, explicitly scoped exception for this project's local network tests.
# Does not disable Windows Firewall, change its defaults, or allow public remote addresses.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
if(-not ([Security.Principal.WindowsPrincipal]::new($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){
    throw 'Run this script once as administrator to save the test-only firewall exceptions.'
}
$testExe=Join-Path $root '.local/tests/node.test.exe'
if(-not(Test-Path -LiteralPath $testExe)){throw "Compile $testExe first"}
foreach($protocol in @('TCP','UDP')){
    $name="P2PMessenger-NodeTests-$protocol"
    if(-not(Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue)){
        $params=@{Name=$name;DisplayName="fuse local tests ($protocol)";Direction='Inbound';Action='Allow';Program=$testExe;Profile='Any';Protocol=$protocol;RemoteAddress='LocalSubnet'}
        if($protocol -eq 'UDP'){$params.LocalPort=5353}
        New-NetFirewallRule @params | Out-Null
    }
}
'Configured local-subnet exceptions for the stable node.test.exe path.' | Set-Content (Join-Path $root '.local/tests/firewall-configured.txt')
