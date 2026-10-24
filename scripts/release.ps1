param(
    [Parameter(Mandatory)][ValidatePattern('^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$')][string]$Version,
    [string]$Title,
    [string]$NotesFile,
    [switch]$Publish
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$root = Split-Path $PSScriptRoot -Parent
$dist = Join-Path $root 'dist'
$number = $Version.TrimStart('v')

function Get-GoExe {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $fallback = 'C:\Program Files\Go\bin\go.exe'
    if (Test-Path $fallback) { return $fallback }
    throw 'Go is not installed'
}

function Write-Utf8([string]$Path, [string]$Text) {
    [IO.File]::WriteAllText($Path, $Text, (New-Object Text.UTF8Encoding($false)))
}

# Checks

if ($Publish) {
    if (git -C $root status --porcelain) { throw 'Working tree is not clean' }
    $local = git -C $root rev-parse HEAD
    $remote = git -C $root rev-parse '@{u}' 2>$null
    if ($local -ne $remote) { throw 'HEAD is not pushed' }
    if (-not $NotesFile -or -not (Test-Path $NotesFile)) { throw 'NotesFile is required to publish: a hand-written description of the release' }
}
if (-not $Title) { $Title = $Version }

# Router

if (Test-Path $dist) { Remove-Item $dist -Recurse -Force }
New-Item -ItemType Directory -Force $dist | Out-Null
$targets = @(
    @{ name = 'arm64'; goarch = 'arm64' },
    @{ name = 'mipsle'; goarch = 'mipsle' },
    @{ name = 'mips'; goarch = 'mips' },
    @{ name = 'arm'; goarch = 'arm' }
)
$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED; GOMIPS = $env:GOMIPS; GOARM = $env:GOARM }
try {
    Push-Location (Join-Path $root 'router')
    foreach ($t in $targets) {
        $env:GOOS = 'linux'
        $env:GOARCH = $t.goarch
        $env:CGO_ENABLED = '0'
        $env:GOMIPS = 'softfloat'
        $env:GOARM = '5'
        $out = Join-Path $dist "pc-pult-router-linux-$($t.name)"
        & (Get-GoExe) build -trimpath -ldflags "-s -w -X main.version=$Version" -o $out .
        if ($LASTEXITCODE -ne 0) { throw "go build failed for $($t.name)" }
        Write-Host "Built router $($t.name) ($([math]::Round((Get-Item $out).Length / 1MB, 1)) MB)"
    }
} finally {
    Pop-Location
    foreach ($k in $saved.Keys) {
        if ($saved[$k]) { Set-Item "Env:$k" $saved[$k] } else { Remove-Item "Env:$k" -ErrorAction SilentlyContinue }
    }
}
Copy-Item (Join-Path $root 'router\deploy\S99pc-pult') (Join-Path $dist 'S99pc-pult')

# PC

$publishDir = Join-Path $dist 'pc-win-x64'
dotnet publish (Join-Path $root 'pc\PcPult\PcPult.csproj') -c Release -o $publishDir -r win-x64 --self-contained true -p:PublishReadyToRun=true "-p:Version=$number" --nologo -v q
if ($LASTEXITCODE -ne 0) { throw 'dotnet publish failed' }
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = Join-Path $dist 'pc-pult-win-x64.zip'
[IO.Compression.ZipFile]::CreateFromDirectory($publishDir, $zip, [IO.Compression.CompressionLevel]::Optimal, $false)
Remove-Item $publishDir -Recurse -Force
Write-Host "Built PC archive ($([math]::Round((Get-Item $zip).Length / 1MB, 1)) MB)"

# Checksums

$lines = Get-ChildItem $dist -File | Where-Object { $_.Name -ne 'SHA256SUMS' } | Sort-Object Name | ForEach-Object {
    "$((Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower())  $($_.Name)"
}
Write-Utf8 (Join-Path $dist 'SHA256SUMS') (($lines -join "`n") + "`n")
Write-Host "Assets are in $dist"
if (-not $Publish) { return }

# Notes

$remoteUrl = (git -C $root remote get-url origin).Trim()
if ($remoteUrl -match '^git@github\.com:(.+?)(\.git)?$') { $repoUrl = "https://github.com/$($Matches[1])" }
elseif ($remoteUrl -match '^https://github\.com/(.+?)(\.git)?$') { $repoUrl = "https://github.com/$($Matches[1])" }
else { throw "Unknown remote: $remoteUrl" }
$slug = $repoUrl -replace '^https://github\.com/', ''
git -C $root fetch --tags --quiet origin
$prev = git -C $root tag --list 'v*' --merged HEAD --sort=-v:refname | Select-Object -First 1
$text =[IO.File]::ReadAllText((Resolve-Path $NotesFile), [Text.Encoding]::UTF8).TrimEnd()
if ($prev) { $text += "`n`nПолный список изменений: $repoUrl/compare/$prev...$Version" }
$notesFile = Join-Path $dist 'notes.md'
Write-Utf8 $notesFile ($text + "`n")

# Publish

$assets = Get-ChildItem $dist -File | Where-Object { $_.Name -ne 'notes.md' } | ForEach-Object { $_.FullName }
$flags = @()
if ($Version -match '-') { $flags += '--prerelease' }
& gh release create $Version @assets --repo $slug --title $Title --notes-file $notesFile --target (git -C $root rev-parse HEAD) @flags
if ($LASTEXITCODE -ne 0) { throw 'gh release create failed' }
Write-Host "Released $Version"
