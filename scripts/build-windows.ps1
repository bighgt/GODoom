# Build the Windows engine as bin\windows_engine.exe (native, on Windows).
#
# Needs cgo + a C toolchain on PATH: this project uses MinGW-w64 UCRT,
# installed via winget (BrechtSanders.WinLibs.POSIX.UCRT). cgo is pulled in
# by go-gl/glfw, goki/vulkan, and audio/fluidsynth_windows.go (which
# LoadLibrary's libfluidsynth at runtime - nothing is linked at build time).
#
# Music: the .exe uses FluidSynth with a bundled soundfont when the
# FluidSynth release DLLs (libfluidsynth-3.dll + deps) and a GM .sf2 are
# placed next to it (or in soundfonts\, or via $env:TPF_SOUNDFONT), and
# renders it into the same software mixer that plays the sound effects
# (audio\mixer.go). Without those it falls back to the Windows GS Wavetable
# synth (winmm), which needs nothing installed.
#
# Cross-compiling the same .exe from Linux: use scripts/build-windows.sh.

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

$mingw = 'C:\Users\BigH\AppData\Local\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin'
if (Test-Path $mingw) { $env:PATH = "$mingw;$env:PATH" }
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    Write-Error "gcc not found on PATH - install MinGW-w64 UCRT (winget install BrechtSanders.WinLibs.POSIX.UCRT)"
}

$env:CGO_ENABLED = '1'
$out = if ($args.Count -ge 1) { $args[0] } else { 'bin\windows_engine.exe' }
New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null

go build -trimpath -ldflags '-s -w' -o $out .\cmd\engine
if ($LASTEXITCODE -ne 0) { Write-Error "go build failed ($LASTEXITCODE)" }
Write-Output "built $out"
Get-Item $out | Format-Table Name, Length, LastWriteTime
