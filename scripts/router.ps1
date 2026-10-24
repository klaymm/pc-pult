$ErrorActionPreference = 'Stop'
$script:s = & "$PSScriptRoot\settings.ps1"

function Get-SshArgs {
    @('-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10', '-i', $s.ssh_key, '-p', "$($s.router_port)", "$($s.router_user)@$($s.router_host)")
}

function Invoke-Router([string]$Command, [switch]$AllowFail) {
    $out = & ssh @(Get-SshArgs) $Command
    if ($LASTEXITCODE -ne 0 -and -not $AllowFail) { throw "ssh failed ($LASTEXITCODE): $Command" }
    $out
}

function Send-RouterFile([string]$Local, [string]$Remote) {
    & scp -O -q -o BatchMode=yes -o ConnectTimeout=10 -i $s.ssh_key -P "$($s.router_port)" $Local "$($s.router_user)@$($s.router_host):$Remote"
    if ($LASTEXITCODE -ne 0) { throw "upload failed: $Local -> $Remote" }
}

function Get-GoExe {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $fallback = 'C:\Program Files\Go\bin\go.exe'
    if (Test-Path $fallback) { return $fallback }
    throw 'Go is not installed'
}
