# Builds the embedded Tailwind stylesheet and the single-binary executable.
$ErrorActionPreference = "Stop"
Push-Location $PSScriptRoot
try {
    $tailwind = Join-Path $PSScriptRoot "tools\tailwind.ps1"
    if (-not (Test-Path -LiteralPath $tailwind)) {
        throw "Tailwind helper not found: $tailwind"
    }

    & powershell -ExecutionPolicy Bypass -File $tailwind `
        -i ./web/src/input.css -o ./web/dist/styles.css --minify
    if ($LASTEXITCODE -ne 0) { throw "Tailwind build failed" }

    go build -trimpath -ldflags "-s -w -H windowsgui" -o GoFastRDP.exe .
    if ($LASTEXITCODE -ne 0) { throw "Go build failed" }

    Write-Host "Built $PSScriptRoot\GoFastRDP.exe"
}
finally {
    Pop-Location
}
