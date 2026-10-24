param(
    [string]$Router,
    [string]$Code,
    [string]$Version,
    [switch]$Remove,
    [switch]$Elevated
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo = 'klaymm/pc-pult'
$name = 'PcPult'
$installDir = Join-Path $env:ProgramFiles 'PcPult'
$dataDir = Join-Path $env:ProgramData 'PcPult'
$cacheDir = Join-Path $env:TEMP 'pc-pult-install'

function Write-Step([string]$Text) { Write-Host "[pc-pult] $Text" }

function Stop-WithError([string]$Text) {
    Write-Host ''
    Write-Host "Ошибка: $Text" -ForegroundColor Red
    if ($Elevated) { Read-Host 'Нажмите Enter, чтобы закрыть окно' | Out-Null }
    exit 1
}

# Elevation

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    $script = $MyInvocation.MyCommand.ScriptBlock.ToString()
    $tmp = Join-Path $env:TEMP 'pc-pult-install.ps1'
    [IO.File]::WriteAllText($tmp, $script, (New-Object Text.UTF8Encoding($true)))
    $argList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$tmp`"", '-Elevated')
    if ($Router) { $argList += @('-Router', $Router) }
    if ($Code) { $argList += @('-Code', $Code) }
    if ($Version) { $argList += @('-Version', $Version) }
    if ($Remove) { $argList += '-Remove' }
    Write-Step 'Нужны права администратора, подтвердите запрос Windows'
    try {
        $p = Start-Process powershell -Verb RunAs -ArgumentList $argList -Wait -PassThru
    } catch {
        Stop-WithError 'Права администратора не получены'
    }
    Remove-Item $tmp -Force -ErrorAction SilentlyContinue
    exit $p.ExitCode
}

# Remove

if ($Remove) {
    Write-Step 'Удаляю PC Pult'
    $svc = Get-Service $name -ErrorAction SilentlyContinue
    if ($svc) {
        if ($svc.Status -ne 'Stopped') { Stop-Service $name -Force; $svc.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30)) }
        & sc.exe delete $name | Out-Null
    }
    Get-Process $name -ErrorAction SilentlyContinue | Stop-Process -Force
    if (Test-Path $installDir) { Remove-Item $installDir -Recurse -Force }
    $answer = Read-Host "Удалить и настройки ($dataDir)? [y/N]"
    if ($answer -match '^(y|д)') { Remove-Item $dataDir -Recurse -Force -ErrorAction SilentlyContinue }
    Write-Step 'Готово'
    if ($Elevated) { Read-Host 'Нажмите Enter, чтобы закрыть окно' | Out-Null }
    exit 0
}

# Checks

$cfgPath = Join-Path $dataDir 'config.json'
$paired = Test-Path $cfgPath
if (-not $paired -and (-not $Router -or -not $Code)) {
    Stop-WithError 'Нужны адрес роутера и код. Команду целиком показывает мастер на роутере (pc-pult-router -pair).'
}
if ([Environment]::OSVersion.Version.Build -lt 19041) { Stop-WithError 'Нужна Windows 10 версии 2004 или новее' }
if (-not [Environment]::Is64BitOperatingSystem) { Stop-WithError 'Нужна 64-разрядная Windows' }

# Download

if ($Version) { $base = "https://github.com/$repo/releases/download/$Version" } else { $base = "https://github.com/$repo/releases/latest/download" }
New-Item -ItemType Directory -Force $cacheDir | Out-Null
$zip = Join-Path $cacheDir 'pc-pult-win-x64.zip'
$sums = Join-Path $cacheDir 'SHA256SUMS'
try {
    Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS" -OutFile $sums
} catch {
    Stop-WithError "Не удалось скачать $base/SHA256SUMS. Проверьте интернет и что релиз существует."
}
$want = (Get-Content $sums | ForEach-Object { if ($_ -match '^([0-9a-fA-F]{64})\s+\*?pc-pult-win-x64\.zip\s*$') { $Matches[1] } } | Select-Object -First 1)
if (-not $want) { Stop-WithError 'В SHA256SUMS нет записи для pc-pult-win-x64.zip' }
if (-not ((Test-Path $zip) -and ((Get-FileHash $zip -Algorithm SHA256).Hash -eq $want))) {
    Write-Step 'Скачиваю сборку (около 70 МБ)'
    try {
        Invoke-WebRequest -UseBasicParsing "$base/pc-pult-win-x64.zip" -OutFile $zip
    } catch {
        Stop-WithError "Не удалось скачать $base/pc-pult-win-x64.zip"
    }
    if ((Get-FileHash $zip -Algorithm SHA256).Hash -ne $want) {
        Remove-Item $zip -Force
        Stop-WithError 'Контрольная сумма архива не совпала, установка остановлена'
    }
}

# Network

$key = $null
$pcName = $null
if ($Code) {
    $routerUrl = "http://$Router"
    $routerHost = ($Router -split ':')[0]
    $route = Find-NetRoute -RemoteIPAddress $routerHost -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceIndex } | Select-Object -First 1
    $adapter = $null
    if ($route) { $adapter = Get-NetAdapter -InterfaceIndex $route.InterfaceIndex -ErrorAction SilentlyContinue }
    if (-not $adapter) { Stop-WithError "Компьютер не видит роутер $routerHost в локальной сети" }
    $mac = $adapter.MacAddress
    if ($adapter.PhysicalMediaType -like '*802.11*') {
        Write-Host 'Внимание: компьютер подключён по Wi-Fi. Включение по сети (Wake-on-LAN) работает только по кабелю, остальное будет работать.' -ForegroundColor Yellow
    }

    Write-Step "Знакомлюсь с роутером $Router"
    $body = [Text.Encoding]::UTF8.GetBytes((@{ code = $Code; mac = $mac; host = $env:COMPUTERNAME } | ConvertTo-Json -Compress))
    try {
        $reply = Invoke-RestMethod -Method Post -Uri "$routerUrl/pair" -ContentType 'application/json' -Body $body -TimeoutSec 15
    } catch {
        $status = 0
        if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
        switch ($status) {
            403 { Stop-WithError 'Роутер не принял код. Проверьте его или запустите на роутере pc-pult-router -pair и возьмите новую команду.' }
            429 { Stop-WithError 'Слишком много неверных кодов, роутер закрыл подключение. Запустите на нём pc-pult-router -pair.' }
            400 { Stop-WithError 'Роутер не принял сетевую карту этого компьютера.' }
            default { Stop-WithError "Роутер $Router не отвечает. Мастер на роутере должен ждать подключения, а компьютер быть в той же сети." }
        }
    }
    if (-not $reply.ok -or -not $reply.key) { Stop-WithError 'Роутер ответил неожиданно' }
    $key = $reply.key
    $pcName = $reply.pc_name
}

# Install

Write-Step 'Устанавливаю'
$svc = Get-Service $name -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne 'Stopped') {
    Stop-Service $name -Force
    $svc.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}
Get-Process $name -ErrorAction SilentlyContinue | Stop-Process -Force
New-Item -ItemType Directory -Force $installDir | Out-Null
Expand-Archive -Path $zip -DestinationPath $installDir -Force

New-Item -ItemType Directory -Force $dataDir | Out-Null
if ($paired) { $cfg = Get-Content $cfgPath -Raw -Encoding UTF8 | ConvertFrom-Json } else { $cfg = [pscustomobject]@{} }
if ($Code) {
    $cfg | Add-Member -NotePropertyName pc_name -NotePropertyValue $pcName -Force
    $cfg | Add-Member -NotePropertyName router_url -NotePropertyValue $routerUrl -Force
    $cfg | Add-Member -NotePropertyName key -NotePropertyValue $key -Force
}
[IO.File]::WriteAllText($cfgPath, ($cfg | ConvertTo-Json -Depth 6), (New-Object Text.UTF8Encoding($false)))
& icacls $dataDir /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null

$exe = Join-Path $installDir 'PcPult.exe'
if (-not (Get-Service $name -ErrorAction SilentlyContinue)) {
    & sc.exe create $name binPath= "`"$exe`"" start= auto DisplayName= 'PC Pult' | Out-Null
    if ($LASTEXITCODE -ne 0) { Stop-WithError 'Не удалось создать службу Windows' }
} else {
    & sc.exe config $name binPath= "`"$exe`"" start= auto | Out-Null
}
& sc.exe description $name 'Remote control of this PC from a Telegram bot running on the router' | Out-Null
& sc.exe failure $name reset= 86400 actions= restart/1000/restart/2000/restart/5000 | Out-Null
& sc.exe failureflag $name 1 | Out-Null

# Wake-on-LAN

if ($adapter) {
    try {
        Set-NetAdapterPowerManagement -Name $adapter.Name -WakeOnMagicPacket Enabled -ErrorAction Stop
        Write-Step "Включение по сети разрешено на карте $($adapter.Name)"
    } catch {
        Write-Host "Не удалось разрешить включение по сети на карте $($adapter.Name). Проверьте в BIOS пункт Wake on LAN." -ForegroundColor Yellow
    }
}

# Start

$logPath = Join-Path $dataDir 'logs\service.log'
$offset = 0
if (Test-Path $logPath) { $offset = (Get-Item $logPath).Length }
Start-Service $name
Write-Step 'Служба запущена, жду связи с роутером'
$connected = $false
for ($i = 0; $i -lt 40 -and -not $connected; $i++) {
    Start-Sleep -Seconds 1
    if (Test-Path $logPath) {
        $fs = [IO.File]::Open($logPath, 'Open', 'Read', 'ReadWrite')
        try {
            if ($fs.Length -gt $offset) {
                $fs.Seek($offset, 'Begin') | Out-Null
                $text = (New-Object IO.StreamReader($fs, [Text.Encoding]::UTF8)).ReadToEnd()
                if ($text -match 'connected to router') { $connected = $true }
            }
        } finally { $fs.Dispose() }
    }
}

Write-Host ''
if ($connected) {
    Write-Step 'Готово: компьютер связан с роутером. Откройте бота в Telegram и нажмите /start'
} else {
    Write-Host 'Служба установлена, но связи с роутером пока нет. Она будет пробовать сама. Журнал:' -ForegroundColor Yellow
    Write-Host "  $logPath"
}
if ($Elevated) { Read-Host 'Нажмите Enter, чтобы закрыть окно' | Out-Null }
exit 0
