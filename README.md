# Skilli

Skilli（スキリ）は、Windows のタスクトレイから空き容量を確認するアプリです。

> [!WARNING]
> Skilli は開発途上のソフトウェアで、利用は自己責任です。利用によって生じたデータの消失や環境の破損などについて、作者は責任を負いません。キャッシュの掃除で削除されたファイルはごみ箱に入らず、元に戻せません。使う前に [利用規約](TERMS.txt) を読み、重要なデータはバックアップしてください。

## インストール

[Releases](https://github.com/otnc/Skilli/releases/latest) から `Skilli-Setup-<バージョン>-x64.exe` をダウンロードして実行します。対応しているのは Windows 11 x64 で、管理者権限は要りません。インストールの途中で利用規約への同意を求めます。

インストーラーと実行ファイルにはコード署名をしていないため、実行時に Windows SmartScreen の警告が出ることがあります。その場合は「詳細情報」を開き、「実行」を選びます。

新しいバージョンはアプリが確認し、通知から更新できます。アンインストールは Windows の「設定 > アプリ > インストールされているアプリ」から行います。設定と掃除の履歴は `%APPDATA%\Skilli` と `%LOCALAPPDATA%\Skilli` に残るので、不要なら手動で削除してください。

## 現在できること

### 空き容量の監視

タスクトレイから開くと、固定ドライブの空き容量と使用率が表示されます。常駐中は 15 分ごとと、スリープから復帰したときに空き容量を確認します。空き容量が設定した基準 (既定は 20 GiB) を下回ると Windows の通知を出し、トレイのアイコンの色が変わります。下回ったままの間は 6 時間おきに通知し、基準の 110% まで空きが戻ると解除します。

設定画面では、通知のオンとオフ、通知の基準、監視するドライブ、Windows へのサインイン時にトレイで起動するかを変更できます。設定は `%APPDATA%\Skilli\config.json` に保存されます。

### キャッシュの計測と掃除

開発ツールのキャッシュとユーザーの一時フォルダのサイズを計測し、エクスプローラーで開けます。

| ID | 対象 | 掃除の方法 |
| --- | --- | --- |
| `uv` | uv キャッシュ | `uv cache prune` (使われていないキャッシュだけ) |
| `pip` | pip キャッシュ | `python -m pip cache purge` (すべて削除) |
| `poetry` | Poetry キャッシュ | 計測のみ (既定でプロジェクトの仮想環境を含むため) |
| `conda` | conda パッケージキャッシュ | `conda clean --all --yes` (環境が使っていないものだけ) |
| `npm` | npm キャッシュ | `npm cache verify` (不要になったデータだけ) |
| `pnpm` | pnpm ストア | `pnpm store prune` (参照されていないパッケージだけ) |
| `yarn` | Yarn 1 のキャッシュ | `yarn cache clean` (すべて削除) |
| `yarn-berry` | Yarn 2 以降のグローバルキャッシュ | `yarn cache clean --all` (すべて削除) |
| `bun` | Bun キャッシュ | 計測のみ (掃除のコマンドがプロジェクトの中でしか動かないため) |
| `deno` | Deno キャッシュ | `deno clean` (すべて削除) |
| `playwright` | Playwright のブラウザ | 計測のみ |
| `go-build` | Go ビルドキャッシュ | `go clean -cache` |
| `go-mod` | Go モジュールキャッシュ | `go clean -modcache` (すべて削除) |
| `cargo` | Cargo のレジストリ | 計測のみ (Cargo が自動で削除するため) |
| `cargo-git` | Cargo の Git 依存 | 計測のみ (Cargo が自動で削除するため) |
| `rustup` | Rust のツールチェーン | 計測のみ |
| `nuget` | NuGet のグローバルパッケージ | `dotnet nuget locals global-packages --clear` (すべて削除) |
| `nuget-http` | NuGet の HTTP キャッシュ | `dotnet nuget locals http-cache --clear` |
| `gradle` | Gradle キャッシュ | 計測のみ (Gradle が自動で削除するため) |
| `maven` | Maven のローカルリポジトリ | 計測のみ |
| `composer` | Composer キャッシュ | `composer clear-cache` (すべて削除) |
| `dart-pub` | Dart / Flutter の pub キャッシュ | `dart pub cache clean --force` (すべて削除) |
| `mise` | mise キャッシュ | `mise cache prune` (最近使われていないものだけ) |
| `huggingface` | Hugging Face のキャッシュ | 計測のみ (モデルやデータセットが入っているため) |
| `temp` | ユーザーの一時フォルダ | Windows の「設定 > システム > ストレージ」を開く |

「すべて削除」のコマンドを実行すると、必要なパッケージやモジュールを次のビルドやインストールで再ダウンロードすることになり、オフラインではビルドできなくなります。とくに `go clean -modcache` と `dart pub cache clean` は、使用中かどうかに関係なく削除します。一時フォルダには使用中のアプリのファイルが含まれるため、Skilli は削除せず、使用中のファイルを避けて削除できる Windows の機能に任せます。Rust のプロジェクトごとのビルド結果 (`target` フォルダ) は大きくなりやすいですが、プロジェクトの場所は Skilli には分からないため、各プロジェクトで `cargo clean` を実行してください。

Yarn は、PATH の `yarn` が Yarn 1 か Yarn 2 以降かで同じコマンドが別のキャッシュを削除するため、掃除の前に `yarn --version` を確かめ、対象に合わないときは実行しません。Corepack を使っていると、ホームフォルダで実行した `yarn` は Yarn 1 になることが多く、その場合 Yarn 2 以降のキャッシュは計測だけになります。Yarn 2 以降のプロジェクトで `yarn cache clean --all` を実行すれば、同じキャッシュを削除できます。

Flutter の `dart` (`bin\dart.bat`) は、実行のたびに Flutter のバージョンを確かめ、Dart SDK のダウンロードや Flutter のツールの再ビルドをすることがあります。Skilli は Flutter に同梱された Dart SDK (`bin\cache\dart-sdk\bin\dart.exe`) を直接実行し、同梱の SDK がまだないときは実行しません。

キャッシュの場所を調べるだけで、ツールがダウンロードやインストールを始めないよう、Skilli が実行するコマンドには次の環境変数を付けます。`MISE_AUTO_INSTALL=false` は mise の shims が足りないバージョンを自動でインストールするのを、`COREPACK_ENABLE_NETWORK=0` は Corepack が pnpm や Yarn をダウンロードするのを、`GOTOOLCHAIN=local` は Go が別のツールチェーンをダウンロードするのを止めます。

pnpm は、プロジェクトのあるドライブごとにストア (`D:\.pnpm-store` など) を作ります。Skilli はホームフォルダのあるドライブのストアを `pnpm`、ほかのドライブのストアを `pnpm-d` のように ID の末尾にドライブ名を付けて扱います。ほかのドライブは、既定の場所にストアがあるときだけ一覧に出します。

各ツールは Windows の環境変数 PATH から探し、コマンドはユーザーのホームフォルダで実行します。プロジェクトのフォルダで実行すると、そのプロジェクトの設定で結果が変わったり、Go がツールチェーンをダウンロードしたりするためです。シェルの起動時にだけ PATH を変える方法 (`fnm env` や `mise activate` など) で使えるようにしたツールは、サインイン時やスタートメニューから起動した Skilli からは見つかりません。mise を使うなら、shims のフォルダを PATH に入れておくと見つかります。Microsoft Store を開くだけの `python.exe` などの「アプリ実行エイリアス」は、インストールされていないものとして扱います。

Skilli はファイルを直接削除しません。掃除は各ツールの公式コマンドで行い、次の条件をすべて満たしたときだけ実行します。

- 実行する前に、対象フォルダ、サイズ、実行するコマンドを表示して確認を求めます。CLI は対話できるターミナルでのみ実行し、それ以外では終了コード `3` で止まります。
- 確認は 10 分間だけ有効で、1 回しか使えません。
- 実行の直前に、確認したときと同じ実行ファイルが PATH から見つかること、その実行ファイルが置き換わっていないこと、ツールが同じフォルダを返すことを確かめます。どれかが変わっていたら中止し、別の方法に切り替えません。
- ドライブ直下、ユーザープロファイル、Windows、Program Files、ドキュメントやデスクトップなどの既知フォルダと、それらを含むフォルダは対象にしません。シンボリックリンクやジャンクションを経由するフォルダも対象にしません。
- 同時に実行できる掃除は、GUI と CLI を合わせて 1 件だけです。結果は `%LOCALAPPDATA%\Skilli\history.jsonl` に記録します。履歴にはパスを含めません。

### CLI

```text
skilli status [--json]             固定ドライブの空き容量を確認する
skilli scan [ID...] [--json]       キャッシュの場所とサイズを計測する
skilli clean <ID>                  確認のうえ、ツール公式のコマンドで掃除する
skilli history [--days N] [--json] 掃除の履歴を表示する
skilli update                      更新を確認する画面を開く
```

`skilli` を引数なしで実行すると、`skilli-gui.exe` を起動して、コマンド自体はすぐに終了します。終了コードは `0` が成功、`1` が実行エラー、`2` が入力不正、`3` が確認が必要、`4` が一部だけ完了した場合です。`status` は実行のたびに計測します。

## 利用規約

インストール時に [利用規約](TERMS.txt) への同意を求めます。利用規約は GPL が認める権利を制限しません。

## ライセンス

Copyright (C) 2026 otoneko.

GNU General Public License v3.0 only (GPL-3.0-only) で公開しています。詳しくは [LICENSE](LICENSE) を見てください。
