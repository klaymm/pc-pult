param([switch]$NoStart)

. "$PSScriptRoot\router.ps1"
$root = Split-Path $PSScriptRoot -Parent
$src = Join-Path $root 'router'
$build = Join-Path $src 'build'
New-Item -ItemType Directory -Force $build | Out-Null

# Build
$version = (git -C $root describe --always --dirty 2>$null)
if (-not $version) { $version = 'dev' }
$bin = Join-Path $build 'pc-pult-router'
$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED; GOMIPS = $env:GOMIPS }
try {
    $env:GOOS = 'linux'
    $env:GOARCH = $s.router_arch
    $env:CGO_ENABLED = '0'
    if ($s.router_arch -like 'mips*') { $env:GOMIPS = 'softfloat' }
    Push-Location $src
    & (Get-GoExe) build -trimpath -ldflags "-s -w -X main.version=$version" -o $bin .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
} finally {
    Pop-Location
    foreach ($k in $saved.Keys) { Set-Item "Env:$k" $saved[$k] -ErrorAction SilentlyContinue; if (-not $saved[$k]) { Remove-Item "Env:$k" -ErrorAction SilentlyContinue } }
}
Write-Host "Built $version ($([math]::Round((Get-Item $bin).Length / 1MB, 1)) MB)"

# Upload
Invoke-Router 'mkdir -p /opt/etc/pc-pult /opt/var/log /opt/sbin /opt/etc/init.d' | Out-Null
Send-RouterFile $bin '/opt/sbin/pc-pult-router.new'
Send-RouterFile (Join-Path $src 'deploy\S99pc-pult') '/opt/etc/init.d/S99pc-pult'
Invoke-Router 'chmod +x /opt/etc/init.d/S99pc-pult' | Out-Null

# Config
$hasConfig = Invoke-Router 'test -f /opt/etc/pc-pult/config.json && echo yes || echo no'
if ($hasConfig -ne 'yes') {
    $cfg = [ordered]@{
        token     = ''
        owner_id  = [int64]$s.owner_id
        pc_name   = $s.pc_name
        pc_mac    = $s.pc_mac
        pc_ip     = ''
        broadcast = $s.broadcast
        wake_cmd  = @('ndmc', '-c', "ip hotspot wake $($s.pc_mac.ToLower())")
        listen    = $s.listen
        pc_key    = $s.pc_key
        data_dir  = '/opt/etc/pc-pult'
        log_file  = '/opt/var/log/pc-pult.log'
        timezone  = $s.timezone
        tg_api    = "$($s.tg_api)"
        tg_proxy  = ''
    }
    $tmp = Join-Path $build 'config.json'
    [IO.File]::WriteAllText($tmp, ($cfg | ConvertTo-Json), (New-Object Text.UTF8Encoding($false)))
    Send-RouterFile $tmp '/opt/etc/pc-pult/config.json'
    Remove-Item $tmp
    Invoke-Router 'chmod 600 /opt/etc/pc-pult/config.json' | Out-Null
    Write-Host 'Config created on the router'
}

# Restart
Invoke-Router '/opt/etc/init.d/S99pc-pult stop >/dev/null 2>&1; sleep 1; mv /opt/sbin/pc-pult-router.new /opt/sbin/pc-pult-router && chmod +x /opt/sbin/pc-pult-router' | Out-Null
Invoke-Router '/opt/sbin/pc-pult-router -version'
$check = (Invoke-Router '/opt/sbin/pc-pult-router -check -config /opt/etc/pc-pult/config.json; echo "exit=$?"' -AllowFail) -join "`n"
$check
if ($check -match 'exit=2') {
    Write-Host 'Token is empty: run the set-token command, then deploy again'
    return
}
if ($check -notmatch 'exit=0') { throw 'config check failed' }
if (-not $NoStart) {
    Invoke-Router '/opt/etc/init.d/S99pc-pult start' | Out-Null
    Start-Sleep -Seconds 2
    Invoke-Router 'tail -n 20 /opt/var/log/pc-pult.log' -AllowFail
}
