param(
  [ValidatePattern('^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$')]
  [string]$Version = '0.2.0-dev',
  [switch]$Installer,
  [string]$CompilerPath
)

$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
  New-Item -ItemType Directory -Path dist -Force | Out-Null
  $versionFlag = "-X main.version=$Version"
  go build -trimpath -ldflags $versionFlag -o dist/skilli.exe ./cmd/skilli
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  go build -trimpath -ldflags "-H=windowsgui $versionFlag" -o dist/skilli-gui.exe ./cmd/skilli
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

  if ($Installer) {
    $target = go env GOOS GOARCH
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    if (($target -join '/') -ne 'windows/amd64') {
      throw 'このインストーラーは GOOS=windows、GOARCH=amd64 のビルドに対応しています。'
    }
    if (!$CompilerPath) {
      $CompilerPath = (Get-Command ISCC.exe -ErrorAction SilentlyContinue).Source
    }
    if (!$CompilerPath) {
      $programFiles = [Environment]::GetFolderPath('ProgramFilesX86')
      $CompilerPath = Join-Path $programFiles 'Inno Setup 6\ISCC.exe'
    }
    if (!(Test-Path -LiteralPath $CompilerPath -PathType Leaf)) {
      throw 'Inno Setup の ISCC.exe が見つかりません。-CompilerPath で指定してください。'
    }
    & $CompilerPath "/DAppVersion=$Version" 'packaging/windows/skilli.iss'
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  }
} finally {
  Pop-Location
}
