# Compiles every .vert / .frag in this directory to <name>.<stage>.spv with
# glslc (Vulkan SDK). The Go build does NOT run this — it go:embed's the
# committed .spv — so run this by hand after editing a shader, then commit
# the regenerated .spv alongside the source.
#
#   pwsh render/vulkan/shaders/compile.ps1
#
# glslc is found on PATH, else under $env:VULKAN_SDK\Bin, else the newest
# C:\VulkanSDK\*\Bin.

$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path

function Find-Glslc {
    $c = Get-Command glslc -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    if ($env:VULKAN_SDK -and (Test-Path "$env:VULKAN_SDK\Bin\glslc.exe")) {
        return "$env:VULKAN_SDK\Bin\glslc.exe"
    }
    $sdk = Get-ChildItem 'C:\VulkanSDK' -Directory -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | Select-Object -First 1
    if ($sdk -and (Test-Path "$($sdk.FullName)\Bin\glslc.exe")) {
        return "$($sdk.FullName)\Bin\glslc.exe"
    }
    throw "glslc not found (install KhronosGroup.VulkanSDK or put glslc on PATH)"
}

$glslc = Find-Glslc
Write-Host "glslc: $glslc"

Get-ChildItem $here -File | Where-Object { $_.Extension -in '.vert', '.frag', '.comp' } | ForEach-Object {
    $stage = $_.Extension.TrimStart('.')
    $out = Join-Path $here ($_.BaseName + "." + $stage + ".spv")
    Write-Host ("  {0} -> {1}" -f $_.Name, (Split-Path -Leaf $out))
    & $glslc -O --target-env=vulkan1.0 $_.FullName -o $out
}

Write-Host "done."
