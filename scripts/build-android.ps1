param([string]$JavaHome='', [string]$SdkRoot='', [switch]$Emulator)
$ErrorActionPreference='Stop'
. "$PSScriptRoot/go-env.ps1"
$project=Split-Path -Parent $PSScriptRoot
Push-Location $project
try {
    if(-not $JavaHome){$JavaHome=(Get-ChildItem .tools/android/jdk -Directory | Select-Object -First 1).FullName}
    if(-not $SdkRoot){$SdkRoot=Join-Path $project '.tools/android/sdk'}
    $env:JAVA_HOME=$JavaHome
    $env:PATH="$JavaHome/bin;$env:PATH"
    $buildTools=Join-Path $SdkRoot 'build-tools/35.0.0'
    $androidJar=Join-Path $SdkRoot 'platforms/android-35/android.jar'
    if(-not (Test-Path $androidJar)){throw 'Install Android SDK platform 35 and build-tools 35.0.0 first'}
    $build=Join-Path $project ('.local/android-build-'+[guid]::NewGuid().ToString('N'))
    $abi='arm64-v8a';$suffix='arm64'
    $env:GOOS='android';$env:GOARCH='arm64';$env:CGO_ENABLED='0'
    if($Emulator){
        $abi='x86_64';$suffix='emulator-x86_64';$env:GOARCH='amd64';$env:CGO_ENABLED='1'
        $env:CC=Join-Path $SdkRoot 'ndk/27.2.12479018/toolchains/llvm/prebuilt/windows-x86_64/bin/x86_64-linux-android26-clang.cmd'
    }
    New-Item -ItemType Directory -Force "$build/classes", "$build/dex", "$build/package/lib/$abi", "$build/package/assets", 'dist/android' | Out-Null
    Copy-Item internal/config/default-network.json "$build/package/assets/default-network.json"
    # Required by pinned anet v0.0.5 for Go >=1.23, per its upstream documentation.
    go build -trimpath -ldflags '-s -w -checklinkname=0' -o "$build/package/lib/$abi/libmessenger.so" ./cmd/client
    if($LASTEXITCODE){throw 'Android engine build failed'}
    $sources=@(Get-ChildItem android/src -Filter *.java -Recurse | ForEach-Object FullName)
    if($Emulator){$sources+=@(Get-ChildItem android/test -Filter *.java -Recurse | ForEach-Object FullName)}
    & "$JavaHome/bin/javac.exe" -encoding UTF-8 --release 8 -classpath $androidJar -d "$build/classes" @sources
    if($LASTEXITCODE){throw 'Android Java compilation failed'}
    & "$JavaHome/bin/jar.exe" cf "$build/classes.jar" -C "$build/classes" .
    & "$buildTools/d8.bat" --lib $androidJar --min-api 26 --output "$build/dex" "$build/classes.jar"
    if($LASTEXITCODE){throw 'DEX compilation failed'}
    $manifest=Join-Path $project 'android/AndroidManifest.xml'
    if($Emulator){$content=[IO.File]::ReadAllText($manifest).Replace('<application ','<application android:debuggable="true" ').Replace('</application>','<provider android:name=".TestFileProvider" android:authorities="org.p2pmessenger.app.test" android:exported="false" /></application>').Replace('</manifest>','<instrumentation android:name=".SmokeTest" android:targetPackage="org.p2pmessenger.app" /></manifest>');$manifest=Join-Path $build 'AndroidManifest.xml';[IO.File]::WriteAllText($manifest,$content)}
    & "$buildTools/aapt2.exe" link -I $androidJar --manifest $manifest -o "$build/unsigned.apk"
    if($LASTEXITCODE){throw 'Manifest compilation failed'}
    Copy-Item "$build/dex/classes.dex" "$build/package/classes.dex"
    & "$JavaHome/bin/jar.exe" uf "$build/unsigned.apk" -C "$build/package" classes.dex -C "$build/package" lib -C "$build/package" assets
    if($LASTEXITCODE){throw 'APK assembly failed'}
    & "$buildTools/zipalign.exe" -P 16 -f 4 "$build/unsigned.apk" "$build/aligned.apk"
    if($LASTEXITCODE){throw 'APK alignment failed'}
    $key=Join-Path $project '.local/android-preview.keystore'
    if(-not (Test-Path $key)){
        & "$JavaHome/bin/keytool.exe" -genkeypair -keystore $key -storepass android -keypass android -alias preview -keyalg RSA -keysize 2048 -validity 10000 -dname 'CN=fuse Preview'
        if($LASTEXITCODE){throw 'Preview signing key creation failed'}
    }
    $apk=Join-Path $project "dist/android/fuse-0.4.2-preview-$suffix.apk"
    & "$buildTools/apksigner.bat" sign --ks $key --ks-pass pass:android --key-pass pass:android --out $apk "$build/aligned.apk"
    if($LASTEXITCODE){throw 'APK signing failed'}
    & "$buildTools/apksigner.bat" verify --verbose $apk
    if($LASTEXITCODE){throw 'APK signature verification failed'}
    & "$buildTools/zipalign.exe" -c -P 16 4 $apk
    if($LASTEXITCODE){throw 'APK alignment verification failed'}
    (Get-FileHash -Algorithm SHA256 $apk).Hash.ToLowerInvariant() | Set-Content "dist/android/SHA256-$suffix.txt" -Encoding ascii
    Get-Item $apk | Select-Object FullName,Length
} finally {Remove-Item Env:GOOS,Env:GOARCH,Env:CC -ErrorAction SilentlyContinue;Pop-Location}
