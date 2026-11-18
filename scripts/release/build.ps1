param(
  [Parameter(Mandatory)]
  [ValidatePattern('^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[a-z]+\.(0|[1-9]\d*))?$')]
  [string]$Version,
  [string]$CompilerPath,
  [switch]$UpdateOnly
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
# 手元でのビルドでは、環境変数がなければ .env から鍵を読む。CI ではシークレットと変数から渡す。
$envFile = Join-Path $repo '.env'
if (Test-Path -LiteralPath $envFile) {
  foreach ($line in Get-Content -LiteralPath $envFile) {
    if ($line -match '^(MYGO_UPDATER_(?:PUBLIC|PRIVATE)_KEY)=(.+)$' -and !(Test-Path -LiteralPath "env:$($Matches[1])")) {
      Set-Item -LiteralPath "env:$($Matches[1])" -Value $Matches[2]
    }
  }
}
$publicKey = $env:MYGO_UPDATER_PUBLIC_KEY
if (!$publicKey -or !$env:MYGO_UPDATER_PRIVATE_KEY) {
  throw 'MYGO_UPDATER_PUBLIC_KEY と MYGO_UPDATER_PRIVATE_KEY を設定してください。'
}
$publicKey = $publicKey.Trim()
if ($publicKey -notmatch '^[A-Za-z0-9+/]{43}=$') {
  throw 'MYGO_UPDATER_PUBLIC_KEY の形式が正しくありません。'
}
if (!$UpdateOnly -and !$CompilerPath) {
  $CompilerPath = (Get-Command ISCC.exe -ErrorAction Stop).Source
}
$target = go env GOOS GOARCH
if ($LASTEXITCODE -ne 0 -or ($target -join '/') -ne 'windows/amd64') {
  throw 'リリースビルドには GOOS=windows、GOARCH=amd64 が必要です。'
}

$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')
$work = New-Item -ItemType Directory -Path (Join-Path $tempRoot ('skilli-release-' + [guid]::NewGuid()))
$previousLdflags = $env:MYGO_LDFLAGS
Push-Location -LiteralPath $work.FullName
try {
  foreach ($name in 'go.mod', 'go.sum', 'cmd', 'internal') {
    Copy-Item -LiteralPath (Join-Path $repo $name) -Destination $work.FullName -Recurse
  }

  New-Item -ItemType Directory -Path resources | Out-Null
  go build -trimpath -ldflags "-X main.version=$Version" -o resources/skilli.exe ./cmd/skilli
  if ($LASTEXITCODE -ne 0) { throw "CLI のビルドに失敗しました: $LASTEXITCODE" }

  $config = @{
    name = 'skilli-gui'
    slug = 'skilli'
    identifier = 'com.otnc.skilli'
    version = $Version
    publisher = 'otoneko.'
    main = './cmd/skilli'
    windows = @{ displayName = 'Skilli' }
    updates = @{ github = 'otnc/Skilli'; publicKey = $publicKey; deltas = 0 }
  } | ConvertTo-Json -Depth 5
  [IO.File]::WriteAllText((Join-Path $work.FullName 'mygo.json'), $config)
  $env:MYGO_LDFLAGS = "-X main.version=$Version"
  go run github.com/egoist/mygo/cmd/mygo@v0.9.2 build -platform windows/amd64 -skip-installer
  if ($LASTEXITCODE -ne 0) { throw "署名付き更新ファイルを作成できませんでした: $LASTEXITCODE" }

  $stage = Join-Path $work.FullName 'dist/windows-amd64'
  $out = Join-Path $repo 'dist/windows-amd64'
  New-Item -ItemType Directory -Path $out -Force | Out-Null
  $archive = "skilli-$Version-windows-amd64.tar.gz"
  foreach ($asset in 'skilli.exe', 'skilli-gui.exe', '.mygo-files', $archive, 'update-windows-amd64.json') {
    Copy-Item -LiteralPath (Join-Path $stage $asset) -Destination $out -Force
  }

  if (!$UpdateOnly) {
    & $CompilerPath "/DAppVersion=$Version" "/DSourceDir=$out" (Join-Path $repo 'packaging/windows/skilli.iss')
    if ($LASTEXITCODE -ne 0) { throw "インストーラーの作成に失敗しました: $LASTEXITCODE" }
  }
} finally {
  Pop-Location
  $env:MYGO_LDFLAGS = $previousLdflags
  $resolved = Get-Item -LiteralPath $work.FullName
  if ($resolved.Parent.FullName.TrimEnd('\') -eq $tempRoot -and
      $resolved.Name.StartsWith('skilli-release-') -and
      !($resolved.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    Remove-Item -LiteralPath $resolved.FullName -Recurse -Force
  }
}
