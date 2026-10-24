$ErrorActionPreference = 'Stop'
$path = Join-Path (Split-Path $PSScriptRoot -Parent) 'deploy.local.json'

if (-not (Test-Path $path)) {
    $nic = Get-NetIPConfiguration | Where-Object { $_.IPv4DefaultGateway -and $_.NetAdapter.Status -eq 'Up' -and $_.NetAdapter.HardwareInterface } | Select-Object -First 1
    if (-not $nic) { throw 'No active network adapter with a gateway' }
    $ip = $nic.IPv4Address[0].IPAddress
    $prefix = $nic.IPv4Address[0].PrefixLength
    $gw = $nic.IPv4DefaultGateway[0].NextHop
    $mac = ($nic.NetAdapter.MacAddress -replace '-', ':')

    $ipBytes = ([Net.IPAddress]::Parse($ip)).GetAddressBytes()
    $mask = [uint32]([math]::Pow(2, 32) - [math]::Pow(2, 32 - $prefix))
    $maskBytes = [BitConverter]::GetBytes($mask)
    [Array]::Reverse($maskBytes)
    $bc = for ($i = 0; $i -lt 4; $i++) { $ipBytes[$i] -bor (255 -bxor $maskBytes[$i]) }

    $bytes = New-Object byte[] 32
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $key = -join ($bytes | ForEach-Object { $_.ToString('x2') })

    $s = [ordered]@{
        router_host = $gw
        router_port = 222
        router_user = 'root'
        router_arch = 'arm64'
        ssh_key     = Join-Path $env:USERPROFILE '.ssh\Router\pc-pult_ed25519'
        owner_id    = [int64](Read-Host 'Telegram id of the owner (ask @userinfobot)')
        pc_name     = 'ПК'
        pc_ip       = $ip
        pc_mac      = $mac
        broadcast   = (($bc -join '.') + ':9')
        listen      = "${gw}:8765"
        pc_key      = $key
        timezone    = 'Europe/Saratov'
        tg_api      = ''
    }
    [IO.File]::WriteAllText($path, ($s | ConvertTo-Json), (New-Object Text.UTF8Encoding($false)))
}

Get-Content $path -Raw -Encoding UTF8 | ConvertFrom-Json
