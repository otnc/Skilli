$ErrorActionPreference = 'Stop'
$package = Join-Path $env:RUNNER_TEMP 'innosetup-6.7.4.exe'
$compilerDir = Join-Path $env:RUNNER_TEMP 'skilli-iscc'
$source = 'https://github.com/jrsoftware/issrc/releases/download/is-6_7_4/innosetup-6.7.4.exe'
$checksum = 'a549929319f9120716677c685535ca4671e4b937b5fdc87e2585f32d8eb30e8c'
Invoke-WebRequest -Uri $source -OutFile $package
if ((Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash -ne $checksum) {
  throw 'Inno Setup のハッシュが一致しません。'
}
$process = Start-Process -FilePath $package -ArgumentList @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/CURRENTUSER', ('/DIR="' + $compilerDir + '"')) -WindowStyle Hidden -Wait -PassThru
if ($process.ExitCode -ne 0 -or !(Test-Path -LiteralPath (Join-Path $compilerDir 'ISCC.exe'))) {
  throw "Inno Setup のコンパイラーを準備できませんでした: $($process.ExitCode)"
}
Add-Content -LiteralPath $env:GITHUB_PATH -Value $compilerDir -Encoding utf8
