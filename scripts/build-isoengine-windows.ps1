# Build the isometric viewer as bin\iso_engine.exe (native, on Windows).
#
# Same toolchain requirement as build-windows.ps1 (cgo + MinGW-w64 UCRT —
# see that script's own comment for why): cmd/isoengine pulls in the same
# go-gl/glfw + goki/vulkan cgo dependencies via render/vulkan and window.
#
# This is Phase 1 of the isometric renderer plan: a standalone WAD/map
# viewer (no monsters/physics/combat), so unlike build-windows.ps1 there's
# no audio/soundfont story here at all.

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

$mingw = 'C:\Users\BigH\AppData\Local\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin'
if (Test-Path $mingw) { $env:PATH = "$mingw;$env:PATH" }
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    Write-Error "gcc not found on PATH - install MinGW-w64 UCRT (winget install BrechtSanders.WinLibs.POSIX.UCRT)"
}

$env:CGO_ENABLED = '1'
$out = if ($args.Count -ge 1) { $args[0] } else { 'bin\iso_engine.exe' }
New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null

go build -trimpath -ldflags '-s -w' -o $out .\cmd\isoengine
if ($LASTEXITCODE -ne 0) { Write-Error "go build failed ($LASTEXITCODE)" }
Write-Output "built $out"
Get-Item $out | Format-Table Name, Length, LastWriteTime
