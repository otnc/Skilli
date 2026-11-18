$ErrorActionPreference = 'Stop'
$unformatted = gofmt -l cmd internal scripts
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
if ($unformatted) {
  Write-Error ("gofmt が必要なファイル: " + ($unformatted -join ', '))
}
