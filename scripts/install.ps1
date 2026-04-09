# install.ps1 - Windows PowerShell helper for building and installing the
# axiom CLI from source. This is the PowerShell-native equivalent of the
# POSIX `make install` target: it runs `go install .\cmd\axiom`, reports
# where the resulting binary lands, and warns if that directory is not on
# the current session PATH.
#
# Usage (from the repo root):
#     powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1

$ErrorActionPreference = "Stop"

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "go is not on PATH. Install Go 1.25+ from https://go.dev/dl/ and reopen this terminal."
    exit 1
}

Write-Host "Running: go install .\cmd\axiom"
& go install .\cmd\axiom
if ($LASTEXITCODE -ne 0) {
    Write-Error "go install failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

$goBin = Join-Path (& go env GOPATH) "bin"
$target = Join-Path $goBin "axiom.exe"
Write-Host "Installed: $target"

$pathDirs = $env:Path -split ';' | Where-Object { $_ -ne '' }
if ($pathDirs -notcontains $goBin) {
    Write-Warning "$goBin is not on your current session PATH."
    Write-Host "To add it for this session, run:"
    Write-Host "    `$env:Path = `"$goBin;`$env:Path`""
    Write-Host "For a persistent fix, add $goBin to your user Path via System Properties > Environment Variables."
} else {
    Write-Host "$goBin is already on PATH. Run 'axiom version' to confirm."
}
