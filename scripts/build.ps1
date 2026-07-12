param(
    [string]$Output = "strata-pvr.exe"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $count = (& git rev-list --count HEAD).Trim()
    $commit = (& git rev-parse --short=12 HEAD).Trim()
    $version = "0.1.0-dev.$count+$commit"
    & git diff --quiet
    if ($LASTEXITCODE -ne 0) {
        $version += ".dirty"
    }

    $date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    $ldflags = "-X strata-pvr/internal/version.Number=$version -X strata-pvr/internal/version.Commit=$commit -X strata-pvr/internal/version.Date=$date"
    & go build -ldflags $ldflags -o $Output ./cmd/strata-pvr
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed"
    }
    Write-Host "Built Strata PVR $version -> $Output"
}
finally {
    Pop-Location
}
