# Ensures the pinned standalone Tailwind CLI is present and runs it.
# The binary is downloaded on first use, verified by SHA-256 and kept in
# tools\tailwindcss.exe, which is not committed to the repository.
# Usage: tools\tailwind.ps1 -i ./web/src/input.css -o ./web/dist/styles.css --minify
param(
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$version = "3.4.19"
$expected = "A15158C4C5E0E7A75F7229BFE4986FE7710D2EDC468B6F96C8981F78AB211347"
$url = "https://github.com/tailwindlabs/tailwindcss/releases/download/v$version/tailwindcss-windows-x64.exe"
$cli = Join-Path $PSScriptRoot "tailwindcss.exe"

function Get-Sha256([string]$Path) {
    (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
}

function Install-Cli {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $tmp = Join-Path $PSScriptRoot ("tailwindcss-" + [guid]::NewGuid().ToString("N") + ".tmp")
    try {
        Write-Host "Downloading Tailwind CLI v$version..."
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $tmp
        $actual = Get-Sha256 $tmp
        if ($actual -ne $expected) {
            throw "SHA-256 mismatch: $actual (expected $expected)"
        }
        Move-Item -LiteralPath $tmp -Destination $cli -Force
    }
    finally {
        if (Test-Path -LiteralPath $tmp) {
            Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        }
    }
}

if ($Force -or -not (Test-Path -LiteralPath $cli)) {
    Install-Cli
} elseif ((Get-Sha256 $cli) -ne $expected) {
    Install-Cli
}

# The bundled browserslist data is older than the CLI expects; the warning is
# not actionable for this project.
$env:BROWSERSLIST_IGNORE_OLD_DATA = "1"

& $cli @args
exit $LASTEXITCODE
