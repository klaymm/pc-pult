$ErrorActionPreference = 'Stop'

$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) { throw 'Run this script as administrator' }

$s = & "$PSScriptRoot\settings.ps1"
$root = Split-Path $PSScriptRoot -Parent
$project = Join-Path $root 'pc\PcPult\PcPult.csproj'
$publish = Join-Path $root 'pc\publish'
$installDir = Join-Path $env:ProgramFiles 'PcPult'
$dataDir = Join-Path $env:ProgramData 'PcPult'
$name = 'PcPult'

# Publish
if (Test-Path $publish) { Remove-Item $publish -Recurse -Force }
dotnet publish $project -c Release -o $publish -r win-x64 --self-contained false -p:PublishReadyToRun=true --nologo -v q
if ($LASTEXITCODE -ne 0) { throw 'dotnet publish failed' }

# Stop
$svc = Get-Service $name -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne 'Stopped') {
    Stop-Service $name -Force
    $svc.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}
Get-Process PcPult -ErrorAction SilentlyContinue | Stop-Process -Force

# Files
New-Item -ItemType Directory -Force $installDir | Out-Null
Copy-Item (Join-Path $publish '*') $installDir -Recurse -Force

# Config
New-Item -ItemType Directory -Force $dataDir | Out-Null
$cfgPath = Join-Path $dataDir 'config.json'
if (-not (Test-Path $cfgPath)) {
    $cfg = [ordered]@{
        pc_name    = $s.pc_name
        router_url = "http://$($s.router_host):8765"
        key        = $s.pc_key
    }
    [IO.File]::WriteAllText($cfgPath, ($cfg | ConvertTo-Json), (New-Object Text.UTF8Encoding($false)))
    Write-Host "Config created: $cfgPath"
}
& icacls $dataDir /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null

# Service
$exe = Join-Path $installDir 'PcPult.exe'
if (-not (Get-Service $name -ErrorAction SilentlyContinue)) {
    & sc.exe create $name binPath= "`"$exe`"" start= auto DisplayName= 'PC Pult' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'sc create failed' }
}
else {
    & sc.exe config $name binPath= "`"$exe`"" start= auto | Out-Null
}
& sc.exe description $name 'Remote control of this PC from a Telegram bot running on the router' | Out-Null
& sc.exe failure $name reset= 86400 actions= restart/1000/restart/2000/restart/5000 | Out-Null
& sc.exe failureflag $name 1 | Out-Null
Start-Service $name
Start-Sleep -Seconds 3
Get-Service $name | Format-Table Name, Status, StartType -AutoSize
Get-Content (Join-Path $dataDir 'logs\service.log') -Tail 10 -ErrorAction SilentlyContinue
