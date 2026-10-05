# Build the real-3D WAD converter as bin\windows_wad3d.exe (native, on
# Windows). Same MinGW-w64 UCRT toolchain as scripts\build-windows.ps1.
# A console program: double-click it for a file picker (the window stays
# open to show the summary), or drag a .wad onto it.

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

$mingw = 'C:\Users\BigH\AppData\Local\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin'
if (Test-Path $mingw) { $env:PATH = "$mingw;$env:PATH" }
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    Write-Error "gcc not found on PATH - install MinGW-w64 UCRT (winget install BrechtSanders.WinLibs.POSIX.UCRT)"
}

$env:CGO_ENABLED = '1'
$out = if ($args.Count -ge 1) { $args[0] } else { 'bin\windows_wad3d.exe' }
New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null

go build -trimpath -ldflags '-s -w' -o $out .\cmd\wad3d
if ($LASTEXITCODE -ne 0) { Write-Error "go build failed ($LASTEXITCODE)" }
Write-Output "built $out"
Get-Item $out | Format-Table Name, Length, LastWriteTime
