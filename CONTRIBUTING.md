# 貢献について

## 開発

Windows 11 x64、Go 1.27.3 以上が必要です。GUI には MyGo のネイティブ UI を使います。

```powershell
go run ./cmd/skilli
go run ./cmd/skilli status --json
go test ./...
go vet ./...
```

## ビルドと配布

```powershell
./scripts/build.ps1
./dist/skilli.exe status --json
```

`dist/skilli.exe` はターミナルから結果を取得する CLI 用です。`dist/skilli-gui.exe` はコンソールを出さずにウィンドウを開きます。両方とも同じソースを使います。`skilli-gui.exe --background` でウィンドウを出さずトレイに常駐します。

Inno Setup 6.3 以上を用意し、次のコマンドで Windows 用インストーラーを作れます。必要なら `-CompilerPath` に `ISCC.exe` のパスを渡します。

```powershell
./scripts/build.ps1 -Installer
```

インストーラーは [TERMS.txt](TERMS.txt) を利用規約の同意画面に表示し、`LICENSE` と一緒にインストール先へ置きます。利用規約を変えたときは、制定日の行を更新日に書き換え、変更点をリリースノートに書いてください。

管理者権限なしで `%LOCALAPPDATA%\Programs\Skilli` にインストールします。スタートメニューからアプリを開けます。CLI を使うときはインストール先の `skilli.exe` を指定します。アンインストールは Windows の「設定 → アプリ → インストールされているアプリ」から行います。

## アプリの更新

配布版は起動の約 30 秒後と、その後 6 時間おきに GitHub Releases の安定版を確認します。新しい更新が見つかると、アプリを起動している間はそのバージョンにつき一度だけ Windows の通知を出します。通知をクリックすると更新の確認画面が開きます。

GitHub Releases からインストールした配布版では、画面またはトレイの「アプリの更新を確認」を選べます。CLI の `skilli update` からも同じ確認画面が開きます。更新を承認すると、Skilli が署名を検証して実行ファイルを差し替え、再起動します。更新確認では安定版を参照します。インストーラーの画面が開くのは新規インストールとアンインストールのときです。

## GitHub Releases への公開

`main` の Actions で `release` を実行します。`version` に `patch`、`minor`、`major`、`prerelease`、または `0.1.0` のようなバージョンを入力してください。プレリリースを始めるなら `version=minor` と `preid=rc` のように入力します。`0.1.0-rc` と入力すると、同じ系列の次の番号を選びます。

ワークフローは Windows 上でテストし、Inno Setup でインストーラーを作り、署名付き更新ファイルと一緒に GitHub Releases に公開します。失敗してタグだけ作成された場合は、最新タグを `version` にそのまま指定して再実行してください。同じタグのコミットを使って再開します。

### 初回の設定

次のコマンドで更新署名用の鍵を生成します。作成した秘密鍵は今後のリリースでも必要なので、安全な場所に保管してください。

```powershell
go run github.com/egoist/mygo/cmd/mygo@v0.9.2 keygen
```

GitHub リポジトリの **Settings → Secrets and variables → Actions** で、`mygo-update.pub` の内容を変数 `MYGO_UPDATER_PUBLIC_KEY` に、`mygo-update.key` の内容をシークレット `MYGO_UPDATER_PRIVATE_KEY` に登録します。配布版は公開された `otnc/Skilli` の GitHub Releases を更新先として使います。
