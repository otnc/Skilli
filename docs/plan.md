# Skilli 計画書

原案は 2026-11-17 の「Cache Sweeper 計画書」。以下は初版の仕様を詰めるための改訂案。

## 実装の状況 (2026-11-18)

できている機能は次のとおり。

- 固定ドライブの空き容量の表示と、15 分ごととスリープ復帰時の確認
- 空き不足の通知 (しきい値、6 時間の再通知、110% での解除) と、トレイのアイコンとツールチップでの状態表示
- 設定画面 (通知、しきい値、監視するドライブ、サインイン時の起動)
- キャッシュの計測と、uv・pip・Go ビルドキャッシュ・pnpm・npm・mise・Go モジュールキャッシュの公式コマンドによる掃除 (Poetry は計測のみ)。一時フォルダは Windows のストレージ設定を開く
- 掃除の履歴と、CLI の `status`・`scan`・`clean`・`history`
- インストール時の利用規約への同意

計画から変えた点は次のとおり。

- 設定は `config.toml` ではなく `%APPDATA%\Skilli\config.json` に置く。
- 履歴は SQLite ではなく `%LOCALAPPDATA%\Skilli\history.jsonl` に置く。90 日を過ぎた行は、ファイルが 1 MiB を超えたときに削除する。
- 掃除はファイル単位の削除を作らず、ツール公式のコマンドだけにした。
- 掃除の計画はプロセスのメモリ上にだけ置く。CLI は `plan` と `apply` に分けず、`clean` の中で対話して確認する。

まだないものは、`status` の結果を常駐側から受け取る名前付きパイプ、一時停止と再開、履歴画面、空き不足までの日数の目安、ごみ箱や VS Code など組み込みの掃除、MCP と Skill。

判定ルール (`internal/monitor`、`internal/config`、`internal/cleanup` の安全確認) は Windows API から分け、実ファイルを消さずにテストしている。

## 名前

表示名は Skilli（スキリ）。Sukkiri（スッキリ）と skill を掛けた名前。CLI 名は `skilli` とする。Windows のビルドは同じソースから CLI 用の `skilli.exe` と、コンソールを出さない起動用の `skilli-gui.exe` を作る。作業先は `D:\Codes\Tools\Skilli`。リポジトリは `otnc/Skilli`。


## 目的と対象

Windows で空き容量が減ったとき、増えた場所と片付けられる範囲を一画面で見つける。原案の調査では C ドライブの空きが約 13.9 GB まで減り、uv キャッシュやユーザーの一時フォルダが大きかった。最初の版では、その場でサイズを再測定して、本人が確認した対象を片付けられるところまでを作る。

初版の動作確認は Windows 11 x64 の標準ユーザー環境で行う。システムドライブとユーザーが選んだローカル固定ドライブを扱う。Windows 10 と Windows on Arm は、配布対象を増やすときに同じ受け入れテストを通す。GUI・トレイ・通知・CLI のヘルプは日本語。CLI のコマンド、JSON のキー、エラー識別子は英語で固定する。

## 初版で使える操作

| 画面 / 操作 | 仕様 |
| --- | --- |
| ホーム | ドライブごとの現在値、空き容量の推移、前回から増えたターゲット、最終計測日時を表示する。未計測・一部計測・不存在を別の状態として表示する。 |
| ターゲット詳細 | 測った場所、計測結果、除外条件、次の動作、最近の履歴を表示する。開ける場所はエクスプローラーで開く。 |
| 手動スキャン | 対象単位かまとめて開始する。進行中は重複起動をまとめ、件数と進行中のターゲットを表示する。ユーザーは途中で中止できる。 |
| フォルダ探索 | 選んだローカルフォルダを読み取り専用で調べる。見つけたフォルダは観測専用ターゲットとして登録できる。 |
| 掃除の確認 | 対象、方法、見込み容量、再ダウンロードの有無、スキップ理由を表示する。ごみ箱へ送る場合は、その時点で空きが増えないことも表示する。 |
| 履歴 | 実行元、対象、方法、成功・スキップ・失敗の件数、ドライブの空きの前後を表示する。中断したジョブは再起動時に見分けられる。 |
| トレイ | 開く、スキャン、掃除候補を確認、一時停止、再開、終了。状態は色とツールチップの文字で伝える。 |
| 設定 | ターゲットの有効化、しきい値、間隔、通知を設定する。ログイン時の常駐は設定で有効にする。 |

掃除操作はユーザーが手動で確定する。容量の違いが分かるように、画面では次の量を別々に扱う。

| 表示名 | 算出方法 |
| --- | --- |
| 現在のサイズ | 観測したファイルの論理サイズ。重複するターゲットの値は単純に合算しない。 |
| 掃除候補のサイズ | 許可されたファイルだけの合計。個別一覧を出せない専用コマンドは不明と表示する。 |
| ごみ箱へ移した量 | 実際に移せたファイルの論理サイズ。 |
| ドライブの空きの増減 | 掃除の前後で同じボリュームを測った差。別の処理も同時に容量を使うため、アプリだけの効果と断定しない。 |

## 監視と通知

- ドライブの空きは起動時・スリープ復帰時・常駐中の 15 分ごとに確認する。ディレクトリをたどってサイズを測るターゲットの計測は、既定で 6 時間ごと。空き不足になったドライブの計測は先に回す。
- ユーザーが変更できる初期値は、ターゲットが 1 GiB、ドライブの空きが 20 GiB。設定と JSON ではバイト数を基準にする。
- しきい値を初めて超えたときに通知する。同じ状態が続く間は既定で 6 時間に 1 回まで。空きがしきい値の 110% を超えたら、その不足状態を解消済みに戻す。
- 一時停止中は定期スキャンとしきい値判定を止める。起動中の手動操作の可否は設定画面に明示する。既定では手動スキャンを許可し、削除は引き続き個別確認を求める。
- 深い探索は手動で開始し、最大件数・実行時間・深さを指定できる。上限到達や権限エラーがあれば部分結果として表示し、その結果を掃除の計画に使わない。
- 次の版では 1 週間の推移をもとに空き不足までの目安を表示する。計測が少ない場合、増減が安定しない場合、空きが減っていない場合は日数を表示しない。

## 掃除と承認

掃除の判定と実行は共通のコアに集める。GUI、CLI、トレイは同じ計画を表示する。

- 掃除計画には対象 ID、ルールの版、ボリュームと対象の識別情報、作成時刻を含める。計画は 10 分で失効し、適用は 1 回だけ。再起動後は作り直す。
- CLI は `plan` で計画を作り、対話端末から `apply` を呼ぶと確認を求める。計画 ID を知っているだけで承認済みにはならない。トレイと通知は確認画面を開く。
- 実行時にパス、ファイルの同一性、変更日時、除外条件、対象アプリの状態を再確認する。条件が変わったファイルはスキップする。専用コマンドの対象ルートや実行ファイルが変わった場合は計画全体を作り直す。
- ルートはドライブと現在のユーザーに結び付ける。既知フォルダの実パスを解決し、ドライブ直下、ユーザープロファイル全体、Windows、Program Files、Documents、Desktop、Downloads、SSH や Git の管理領域などを削除対象から外す。設定で保護対象を解除できない。
- シンボリックリンク、ジャンクション、マウントポイント、クラウドのプレースホルダーなどの再解析ポイントはスキャン時も掃除時もたどらない。ファイルの入れ替わりを防ぐため、再確認と操作をどう結び付けられるかを Windows 実機で検証する。確認した対象と同じファイルを操作できる保証が取れない方式では掃除を有効にしない。
- フォルダ一括削除の代わりに、計画に入ったファイル単位で処理する。ロック中、削除できないファイル、計画後に書き換わったファイルはスキップし、理由を記録する。
- 組み込みの掃除は Windows のごみ箱へ移す。ごみ箱に入れられない場合はスキップする。完全削除やごみ箱の一括消去は後の版で個別に審査する。
- 専用コマンドは、同梱したアダプターの引数と使用バージョンを検証して実行する。timeout や失敗時は対象を未完了扱いにし、別の削除方法へ切り替えない。専用コマンドが削除するファイルを列挙できない場合は、実行内容と復元できない可能性を明記して個別に確認する。
- 同じユーザーの掃除ジョブは常駐・CLI を通じて同時に 1 件まで。停止を要求されたら新しい項目の処理を止め、済んだ項目の結果を記録する。

## 対応するターゲット

原案にあるパスは候補として付録に残す。各プリセットに検出方法、対応バージョン、計測範囲、保護するファイル、掃除方法、失敗時の応答を記録する。

| 対象 | 初版の仕様案 |
| --- | --- |
| uv | `uv cache dir` で対象を確認し、`uv cache prune` を個別確認で実行できるようにする。環境設定で場所が変わる可能性を扱う。`uv cache clean` への自動切り替えとフォルダの直接削除は行わない。 |
| pnpm | `pnpm store path` でストアを計測する。`pnpm store prune` の対応バージョンと使用中の挙動を確認できれば手動掃除に追加する。 |
| Go ビルドキャッシュ | `go env GOCACHE` で場所を調べる。`go clean -cache` は計測した場所と実行時の環境が一致するときだけ候補に出す。Go モジュールキャッシュは計測のみにする。 |
| VS Code | 初版はファイルの種類を限定できる拡張のダウンロードキャッシュから試す。アプリが終了しているときだけ実行する。設定、ユーザーデータ、ワークスペースの状態を含めない。 |
| ユーザー Temp | 読み取り専用でサイズ順に表示する。既定のパス全体を日付だけで掃除できるようにはせず、用途を確認できたアプリのサブフォルダを個別に審査する。 |
| Windows ごみ箱 | ボリュームごとにサイズを表示し、Windows のごみ箱を開けるようにする。 |
| AI ツール / ブラウザ / Discord など | 原案のうち場所と範囲を再確認できたものを順次計測専用で追加する。バージョン別にキャッシュ専用の境界を確認できたものから手動掃除を追加する。 |
| Docker / WSL / 開発環境 | 初版はサイズ表示。prune、仮想ディスク圧縮、オフラインデータの消去など用途が異なる操作は、対応範囲を別に決める。 |

追加候補は Gradle のローカルビルドキャッシュ、Windows の配信の最適化、Windows Error Reporting、Hugging Face の Xet キャッシュ、Rider など JetBrains 製品別のキャッシュ、Python のツール別キャッシュ。外部コマンドや OS 設定を使うものは、対象バージョンと既定パスの根拠が見つかるまでは観測候補とする。

## CLI と連携

CLI 名は `skilli`。

| コマンド | 仕様 |
| --- | --- |
| `skilli` | トレイアプリを起動する。起動済みならホームを開く。 |
| `skilli status --json` | 最後に計測したドライブ、ターゲット、警告を返す。読み取り操作で再計測は開始しない。 |
| `skilli scan [target...] --json` | 計測を開始し、完了・一部完了・中止を区別して返す。 |
| `skilli plan <target...> --json` | 確認画面と同じ掃除計画を返す。 |
| `skilli apply <plan-id>` | 対話端末での確認を受けて計画を実行する。 |
| `skilli target list / show` | ターゲットの設定と実行できる操作を表示する。 |
| `skilli history --since 7d --json` | 操作履歴を返す。 |
| `skilli pause [1h]` / `skilli resume` | 監視を停止・再開する。 |

JSON には `schema_version` を含め、stdout は結果、stderr は診断に使う。終了コードの初期案は `0` 成功、`1` 実行エラー、`2` 入力不正、`3` 確認待ち、`4` 一部完了または保留。CLI の自動操作は初版では計測と表示を対象にする。

MCP と Skill は CLI の仕様が固まった後に追加する。MCP は読み取りを初期値とし、書き込みは提案を作るところまでにする。エージェントによる掃除の提案はアプリの確認画面で本人が承認する。プリセットの共有ファイルにはスキーマの版とルールの差分を表示し、生のコマンドやスクリプトは取り込まない。

## 常駐と保存

MyGo v0.9.2 のネイティブ UI を候補とする。タグの `go.mod` は Go 1.27.3 を指定している。`OnSecondInstance` が受け取るのは引数と作業ディレクトリなので、別の起動からウィンドウを開く用途に使う。CLI が結果を受け取る通信は、ユーザーとセッションを制限したローカルの名前付きパイプを候補とする。接続先が起動中なら CLI はそこへ依頼し、起動していない場合はローカルで実行する。どちらにも同じ排他と掃除ルールを適用する。

- `%APPDATA%\{AppId}\config.toml` に設定を置く。GUI は一時ファイルからの置換で書き込み、読み込みが失敗した場合は直前の有効な設定を使ってエラーを表示する。
- `%LOCALAPPDATA%\{AppId}\history.sqlite` に計測と実行結果を保存する。既定の保持期間は 90 日。大量のファイル名を履歴に残す代わりに集計とエラー理由を記録し、必要に応じて詳細を一時的に表示する。
- 容量レポートはローカルで生成する。共有用に書き出す場合は、ユーザー名と絶対パスを省いた版を既定にする。
- スキャン、掃除、履歴、IPC、GUI を Go のパッケージで分ける。Windows API を使う I/O と、ファイルシステムから独立したルールを分け、ルールは CI で実ファイルを消さずにテストできるようにする。

## Windows への配布

Skilli の Windows 用インストーラーとアンインストーラーには Inno Setup を使う。

配布版は常駐中に GitHub Releases の安定版を定期確認する。更新が見つかったら、同じ起動中にバージョンごとに一度だけ Windows の通知を出す。通知から更新の確認画面を開けるようにする。

公開は GitHub Actions の `release` を `workflow_dispatch` で実行する。Inno Setup のインストーラーと署名付き更新ファイルを GitHub Releases に公開する。インストール済みの Skilli は、GUI、トレイ、または `skilli update` から更新を確認できる。ユーザーが承認すると MyGo の更新機能が署名を検証して実行ファイルを差し替え、Skilli が再起動する。

## 開発順と受け入れ条件

| 段階 | 確認すること |
| --- | --- |
| M0 / Windows 実機の確認 | ウィンドウなしでログイン時に常駐できる。通知から確認画面を開ける。標準ユーザーで動き、再起動しても多重起動しない。PowerShell と cmd で CLI の出力と終了コードを確認する。GUI 用の実行ファイルでコンソールが開かないことを確認する。 |
| M1 / 読み取りと計画 | 容量、エラー、未検出を区別できる。複数ドライブと重複ターゲットを扱える。権限不足、長いパス、再解析ポイント、オフラインのドライブでも表示を続けられる。 |
| M2 / 初版の手動掃除 | 実行前に許可した範囲だけを掃除する。計画後に対象ファイルが変わった場合や、停止が必要なアプリが起動した場合は保留になる。ごみ箱から復元できる例、同一ボリュームで空きが増えない例、途中中断、設定破損からの復旧を実機で確認する。 |
| M3 / 対象と自動化の拡張 | 検証済みのプリセットだけ追加する。自動掃除は初版を使った本人がターゲットごとに許可した場合に限り、実機で最低 2 週間の確認を経て有効化候補にする。 |
| M4 / 配布と連携 | Inno Setup で新規インストール、上書きインストール、アンインストールを試し、上書き時に設定を引き継げることを確認する。公開するなら署名・アップデーター・依存ライセンスを確認する。MCP と Skill は読み取りから公開する。 |

M0 では利用中のプロセス確認ができない場合の表示、長時間常駐させたときのメモリ、キーボードだけでの操作、日本語表示、Windows の高コントラストも確認する。実測値を取ってから、数値目標を決める。

## 決めておきたいこと

- 公開配布は GitHub Releases で行うと決めた。対応は Windows 11 x64、ライセンスは GPL-3.0-only、インストール時に `TERMS.txt` の利用規約への同意を求める。実行ファイルのコード署名は未定。
- 初版の掃除はツール公式のコマンドに絞った (uv、Go ビルドキャッシュ、pnpm、npm、Go モジュールキャッシュ)。Go モジュールキャッシュは全削除になるため、確認画面で影響を明記する。持ち主のツールがない一時フォルダは Windows のストレージ設定に任せる。組み込みターゲットの自前削除は、ファイルの同一性を確認したうえで操作する方式を Windows 実機で検証してから決める。
- 外部コマンドをタイムアウトで止めるとき、`pnpm.cmd` などが起動した子プロセスは残ることがある。ジョブオブジェクトでまとめて止めるかを決める。
- 原案で参照している削除範囲の調査表は今回の Markdown に含まれていない。手元に別資料があれば、対応プリセットを確定するときに照合する。

## 参照資料

- [MyGo v0.9.2 の go.mod](https://github.com/egoist/mygo/blob/v0.9.2/go.mod)
- [MyGo のアプリ、スレッド、多重起動](https://github.com/egoist/mygo/blob/main/docs/app.md)
- [Windows の再解析ポイント](https://learn.microsoft.com/windows/win32/fileio/reparse-points-and-file-operations)
- [Windows のごみ箱への移動に使うフラグ](https://learn.microsoft.com/ja-jp/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperation-setoperationflags)
- [Windows の名前付きパイプのアクセス制御](https://learn.microsoft.com/windows/win32/ipc/named-pipe-security-and-access-rights)
- [uv のキャッシュの扱い](https://docs.astral.sh/uv/concepts/cache/)
- [pnpm store コマンド](https://pnpm.io/cli/store)

## 付録 / 原案の計測・調査候補

パスと容量は原案の記載。容量は 2026-11-17 の個人環境での記録で、空欄相当の行を含む。候補ディレクトリにはキャッシュ以外が混在する場合があるため、表のパスは掃除の許可範囲には使わない。

### Windows・システム

| 対象 | 原案の場所 | 原案の計測 |
| --- | --- | --- |
| ユーザー Temp | `%TEMP%` | VS の残骸だけで 4.7GB（掃除前） |
| システム Temp | `C:\Windows\Temp` | — |
| アプリ更新の残り | `%LOCALAPPDATA%\*-updater` | 約 1.2GB（8 アプリ） |
| Squirrel 更新の残り | `%LOCALAPPDATA%\SquirrelTemp` | 1MB |
| クラッシュダンプ | `%LOCALAPPDATA%\CrashDumps` | 207MB |
| DirectX シェーダー | `%LOCALAPPDATA%\D3DSCache` | 1MB |
| ごみ箱 | `C:\$Recycle.Bin` | — |
| Windows Update の残り | — | — |

### AI エージェント

| 対象 | 原案の場所 | 原案の計測 |
| --- | --- | --- |
| Claude Code 一時フォルダ | `%TEMP%\claude` | 2.2GB（掃除前） |
| Codex 一時ファイル | `%TEMP%\codex-*` | — |
| Codex パッケージ | `~\.codex\packages` | 923MB |
| Codex サンドボックス | `~\.codex\.sandbox-bin` | 360MB |
| Copilot CLI ログ | `~\.copilot\logs` | 54MB |
| Hugging Face モデル | `~\.cache\huggingface` | — |

### 言語・パッケージマネージャー

| 対象 | 原案の場所 | 原案の計測 |
| --- | --- | --- |
| uv | `%LOCALAPPDATA%\uv\cache` | 6GB 以上 |
| pip | `%LOCALAPPDATA%\pip\Cache` | 78MB |
| Poetry | `%LOCALAPPDATA%\pypoetry\Cache` | — |
| conda | `~\.conda`, `pkgs` | — |
| npm | `%LOCALAPPDATA%\npm-cache` | 未測定（大きい） |
| pnpm | `%LOCALAPPDATA%\pnpm\store`, `pnpm-cache` | 1.2GB 以上 |
| Yarn | `%LOCALAPPDATA%\Yarn\Cache` | 824MB |
| Bun | `~\.bun\install\cache` | — |
| Deno | `%LOCALAPPDATA%\deno` | 5MB |
| node-gyp | `%LOCALAPPDATA%\node-gyp` | — |
| Electron ダウンロード | `%LOCALAPPDATA%\electron`, `electron-builder` | 350MB |
| Playwright ブラウザ | `%LOCALAPPDATA%\ms-playwright` | 706MB |
| Puppeteer ブラウザ | `~\.cache\puppeteer` | — |
| Go ビルド | `%LOCALAPPDATA%\go-build` | — |
| Go モジュール | `~\go\pkg\mod` | 未測定（大きい） |
| Rust crate | `~\.cargo\registry` | — |
| sccache | `%LOCALAPPDATA%\Mozilla\sccache` | — |
| Gradle | `~\.gradle\caches`, `wrapper` | 1GB 以上（掃除前） |
| Maven | `~\.m2\repository` | — |
| NuGet | `~\.nuget\packages` | — |
| Dart / Flutter | `%LOCALAPPDATA%\Pub\Cache` | 未測定 |
| RubyGems | gem のインストール先 | — |
| Arduino | `%LOCALAPPDATA%\Arduino15\staging` | Arduino15 全体で 387MB |
| Docker | Docker Desktop | — |
| WSL 仮想ディスク | `ext4.vhdx` | — |
| ユーザーキャッシュ | `~\.cache`（上のものと codex-runtimes 以外） | 1GB 以上（掃除前） |

### エディタ・ IDE

| 対象 | 原案の場所 | 原案の計測 |
| --- | --- | --- |
| VS Code 拡張のダウンロード | `%APPDATA%\Code\CachedExtensionVSIXs` | 643MB |
| VS Code 古い版のデータ | `%APPDATA%\Code\CachedData` | 365MB |
| VS Code キャッシュ | `%APPDATA%\Code\Cache`, `Code Cache`, `GPUCache` | 210MB |
| VS Code ログ | `%APPDATA%\Code\logs` | 6MB |
| VS Code 系のエディタ | `%APPDATA%\Cursor`, `Antigravity`, `Windsurf` など | Cursor 217MB, Antigravity 517MB |
| JetBrains | `%LOCALAPPDATA%\JetBrains` | — |

### アプリ・ゲーム

| 対象 | 原案の場所 | 原案の計測 |
| --- | --- | --- |
| Chrome / Brave / Edge | `User Data\*\Cache`, `Code Cache`, `GPUCache` | — |
| Discord | `%APPDATA%\discord\Cache`, `Code Cache`, `GPUCache` | 237MB |
| Slack | `%APPDATA%\Slack\Cache` など | 106MB |
| Teams（新） | `%LOCALAPPDATA%\Packages\MSTeams_8wekyb3d8bbwe` のキャッシュ | — |
| Spotify | `%LOCALAPPDATA%\Spotify\Data` | 809MB |
| Zoom | `%APPDATA%\Zoom` のキャッシュとログ | Zoom 全体で 810MB |
| Figma | `%APPDATA%\Figma\DesktopProfile`, `Desktop` | 788MB |
| Steam シェーダー | `steamapps\shadercache` | — |
| NVIDIA シェーダー | `%LOCALAPPDATA%\NVIDIA\DXCache`, `GLCache` | — |
| Intel シェーダー | `%USERPROFILE%\AppData\LocalLow\Intel\ShaderCache` | LocalLow\\Intel 全体で 165MB |
| Minecraft ログ | PrismLauncher / CurseForge の `logs` | — |
