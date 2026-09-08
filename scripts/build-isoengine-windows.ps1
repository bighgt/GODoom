# Build the isometric engine as bin\windows_isoengine.exe (native, on
# Windows) — naming matches build-windows.ps1's <os>_<program> scheme
# (windows_engine.exe alongside windows_isoengine.exe).
#
# Same toolchain requirement as build-windows.ps1 (cgo + MinGW-w64 UCRT —
# see that script's own comment for why): cmd/isoengine pulls in the same
# go-gl/glfw + goki/vulkan + oto/FluidSynth cgo dependencies via
# render/vulkan, window, and audio as the first-person engine — it runs the
# same engine.Game simulation and the same pre-game launcher, just with a
# top-down camera and screen-relative controls instead of first-person.

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

$mingw = 'C:\Users\BigH\AppData\Local\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin'
if (Test-Path $mingw) { $env:PATH = "$mingw;$env:PATH" }
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    Write-Error "gcc not found on PATH - install MinGW-w64 UCRT (winget install BrechtSanders.WinLibs.POSIX.UCRT)"
}

$env:CGO_ENABLED = '1'
$out = if ($args.Count -ge 1) { $args[0] } else { 'bin\windows_isoengine.exe' }
New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null

go build -trimpath -ldflags '-s -w' -o $out .\cmd\isoengine
if ($LASTEXITCODE -ne 0) { Write-Error "go build failed ($LASTEXITCODE)" }
Write-Output "built $out"
Get-Item $out | Format-Table Name, Length, LastWriteTime
