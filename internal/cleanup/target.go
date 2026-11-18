// Package cleanup measures cache folders and cleans them up only through
// the official command of the tool that owns them.
//
// Skilli never deletes files itself. A cleanup is a Plan that the user
// confirms, and Engine.Apply checks again right before running it that the
// tool, its executable and its folder are still the ones the user saw.
package cleanup

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/otnc/Skilli/internal/config"
)

// Command is an executable name looked up in PATH and its arguments.
type Command struct {
	Name string
	Args []string
}

// Target is a folder Skilli can measure and possibly clean.
//
// The folder comes from one of three places. Dir names it outright, as
// for a target the user added. With Locate set, the tool prints it.
// Otherwise it is the environment variable EnvDir, or, when that is unset,
// DefaultDir under the folder in the environment variable DefaultBase (the
// user's home when DefaultBase is empty). SubDir is then joined to it.
type Target struct {
	ID   string
	Name string
	// Custom marks a target the user added in the settings.
	Custom bool
	// Dir is the folder itself, when no tool or variable tells it.
	Dir string
	// Locate prints the folder.
	Locate Command
	// Parse extracts the folder from Locate's output; nil takes the whole
	// output as the path.
	Parse func(output string) (string, error)
	// MissingIfLocateFails treats a failing Locate as the tool being absent,
	// for commands such as python -m pip that fail when pip is not installed.
	MissingIfLocateFails bool
	EnvDir               string
	DefaultBase          string
	DefaultDir           string
	SubDir               string
	// Clean is the tool's own cleanup command. With neither Clean nor
	// Delete, the target is only measured.
	Clean *Command
	// Delete lets Skilli remove the folder's contents itself, for a cache
	// whose tool offers no cleanup command. With both set, the one that
	// PreferDelete chooses is tried first, and the other only when the
	// first cannot be used: its tool is missing or of the wrong version.
	Delete       *Deletion
	PreferDelete bool
	// AcceptMajor, when set, runs the clean tool with --version before a
	// cleanup and refuses it unless the major version passes. The same
	// yarn command cleans Yarn 1's cache or Yarn 2+'s depending on which
	// Yarn PATH resolves to.
	AcceptMajor func(major int) bool
	// Executable maps the executable PATH resolves to the one to run, or
	// refuses it, as for the dart.bat wrapper of Flutter.
	Executable func(found string) (string, error)
	// Note tells the user what the cleanup removes and what it costs.
	Note string
	// PerDrive marks a tool that keeps one cache per drive and acts on the
	// one of the drive it runs on, as pnpm does to hard-link packages.
	// TargetsFor adds a copy of such a target for each other drive.
	PerDrive bool
	// DriveStore is the folder at a drive root where a per-drive tool
	// keeps that drive's cache by default.
	DriveStore string
	// WorkDir is where the target's commands run; empty means the user's
	// home. A per-drive copy runs at the root of its drive.
	WorkDir string
	// RequireDir must exist before any command runs; otherwise the target
	// is missing. A per-drive copy of pnpm requires X:\.pnpm-store, because
	// pnpm store path writes a test file to the drive root, which on a
	// cloud drive such as Google Drive fails or syncs a stray file.
	RequireDir string
	// Busy names executables, such as uv.exe or cargo.exe, that hold the
	// cache while they run. A cleanup is refused while one of them runs,
	// rather than left to wait for a lock: a server started with uv run can
	// hold uv's cache for hours, and uv cache prune waits for it.
	Busy []string
}

// Deletion describes what Skilli removes itself.
type Deletion struct {
	// Paths are folders inside the target folder whose contents are
	// removed; empty means the contents of the target folder itself. They
	// let a deletion skip what is not cache, such as Hugging Face's token.
	Paths []string
	// OlderThan keeps what was modified more recently, as files an app in
	// the temporary folder may still use.
	OlderThan time.Duration
	// KeepProtected keeps read-only, hidden and system files.
	KeepProtected bool
	// LinksOnly removes only the links directly in the folder.
	LinksOnly bool
	// KeepPathEntries keeps any entry that PATH names, in the registry or
	// in Skilli's own environment, as the fnm link a user's PATH may hold.
	KeepPathEntries bool
}

// Cleanable reports whether Skilli can clean the target, by the tool's
// command or by deleting it.
func (t Target) Cleanable() bool { return t.Clean != nil || t.Delete != nil }

func (t Target) locatedByCommand() bool { return t.Locate.Name != "" && t.Dir == "" }

// methods lists the cleanup methods to try, in order.
func (t Target) methods() []string {
	var methods []string
	if t.Clean != nil {
		methods = append(methods, MethodCommand)
	}
	if t.Delete != nil {
		methods = append(methods, MethodDelete)
	}
	if t.PreferDelete {
		slices.Reverse(methods)
	}
	return methods
}

// Targets returns the built-in targets in display order.
//
// The tool's own cleanup command is preferred whenever one exists and runs
// without a project. Skilli deletes a cache itself only when the tool
// offers no such command, and then only the folders that hold cache: never
// Poetry's virtual environments or Hugging Face's token. Rust toolchains
// are only measured, since rustup toolchain uninstall removes them. A
// folder shared by many apps, such as %TEMP%, is left to Windows' own
// cleanup, which skips files in use.
func Targets() []Target {
	return []Target{
		// Python
		{
			ID:     "uv",
			Name:   "uv キャッシュ",
			Locate: Command{Name: "uv", Args: []string{"cache", "dir"}},
			Clean:  &Command{Name: "uv", Args: []string{"cache", "prune"}},
			Busy:   []string{"uv.exe", "uvx.exe"},
			Note:   "uv cache prune で、どの環境からも使われていないキャッシュだけを削除します。必要になれば再ダウンロードされます。",
		},
		{
			ID:                   "pip",
			Name:                 "pip キャッシュ",
			Locate:               Command{Name: "python", Args: []string{"-m", "pip", "cache", "dir"}},
			MissingIfLocateFails: true,
			Clean:                &Command{Name: "python", Args: []string{"-m", "pip", "cache", "purge"}},
			Note:                 "pip cache purge で、PATH の python が使う pip のキャッシュをすべて削除します。必要になれば再ダウンロードされます。",
		},
		// Poetry's cache-dir also holds the projects' virtual environments
		// (virtualenvs) by default, so each target names a subfolder.
		{
			ID:     "poetry",
			Name:   "Poetry のリポジトリキャッシュ",
			Locate: Command{Name: "poetry", Args: []string{"config", "cache-dir"}},
			SubDir: `cache\repositories`,
			// The confirmation's default is yes, which --no-interaction takes.
			Clean: &Command{Name: "poetry", Args: []string{"cache", "clear", "--all", "--no-interaction"}},
			Note:  "poetry cache clear --all で、パッケージの情報などのリポジトリのキャッシュを削除します。プロジェクトの仮想環境は削除しません。",
		},
		{
			ID:     "poetry-artifacts",
			Name:   "Poetry のダウンロード済みパッケージ",
			Locate: Command{Name: "poetry", Args: []string{"config", "cache-dir"}},
			SubDir: "artifacts",
			Delete: &Deletion{},
			Busy:   []string{"poetry.exe"},
			Note:   "Poetry にはこのフォルダを削除するコマンドがないため、Skilli が中身を直接削除します。次のインストールで再ダウンロードされます。プロジェクトの仮想環境は削除しません。",
		},
		{
			ID:     "conda",
			Name:   "conda パッケージキャッシュ",
			Locate: Command{Name: "conda", Args: []string{"info", "--json"}},
			Parse:  firstOfJSONList("pkgs_dirs"),
			Clean:  &Command{Name: "conda", Args: []string{"clean", "--all", "--yes"}},
			Note:   "conda clean --all で、インデックスのキャッシュ、どの環境からも使われていないパッケージ、tarball、一時ファイル、ログを削除します。環境が使っているパッケージは残ります。",
		},
		// JavaScript
		{
			ID:     "npm",
			Name:   "npm キャッシュ",
			Locate: Command{Name: "npm", Args: []string{"config", "get", "cache"}},
			Clean:  &Command{Name: "npm", Args: []string{"cache", "verify"}},
			Note:   "npm cache verify で、キャッシュの整合性を確かめ、不要になったデータだけを削除します。空く容量は少ないことがあります。",
		},
		{
			ID:     "pnpm",
			Name:   "pnpm ストア",
			Locate: Command{Name: "pnpm", Args: []string{"store", "path"}},
			Clean:  &Command{Name: "pnpm", Args: []string{"store", "prune"}},
			Note:   "pnpm store prune で、どのプロジェクトからも参照されていないパッケージを削除します。pnpm install の実行中は使わないでください。",
			// pnpm keeps a store on every drive that holds a project
			// (such as D:\.pnpm-store) and acts on the current drive's.
			PerDrive:   true,
			DriveStore: ".pnpm-store",
		},
		{
			ID:     "yarn",
			Name:   "Yarn キャッシュ",
			Locate: Command{Name: "yarn", Args: []string{"cache", "dir"}},
			// Yarn 2 and later have no cache dir command, so a failing
			// yarn means PATH has no Yarn 1.
			MissingIfLocateFails: true,
			Clean:                &Command{Name: "yarn", Args: []string{"cache", "clean"}},
			AcceptMajor:          func(major int) bool { return major == 1 },
			Note:                 "yarn cache clean で Yarn 1 のグローバルキャッシュをすべて削除します。次のインストールで再ダウンロードされます。",
		},
		{
			ID:          "yarn-berry",
			Name:        "Yarn 2 以降のグローバルキャッシュ",
			EnvDir:      "YARN_GLOBAL_FOLDER",
			DefaultBase: "LOCALAPPDATA",
			DefaultDir:  `Yarn\Berry`,
			SubDir:      "cache",
			// --all, not --mirror: with Yarn 4's default global cache the
			// cache folder and the mirror are the same folder, so Yarn sets
			// no mirror and --mirror would remove nothing.
			Clean:       &Command{Name: "yarn", Args: []string{"cache", "clean", "--all"}},
			AcceptMajor: func(major int) bool { return major >= 2 },
			// With Corepack, yarn in the home folder is usually Yarn 1,
			// which must not run this command; the folder is plain cache.
			Delete: &Deletion{},
			Note:   "ホームフォルダで実行した yarn が Yarn 2 以降なら yarn cache clean --all で、そうでなければ Skilli が中身を直接削除して、プロジェクト間で共有するキャッシュを削除します。次のインストールで再ダウンロードされます。.yarnrc.yml の globalFolder で場所を変えている場合は、既定の場所だけが対象です。",
		},
		// Node version managers keep every installed Node, which is not
		// cache; only fnm's per-shell links are cleaned.
		{
			ID:          "fnm",
			Name:        "fnm でインストールした Node",
			EnvDir:      "FNM_DIR",
			DefaultBase: "APPDATA",
			DefaultDir:  "fnm",
			SubDir:      "node-versions",
			Note:        "計測のみ。使わないバージョンは fnm uninstall <バージョン> で削除します。",
		},
		{
			ID:          "fnm-multishells",
			Name:        "fnm のシェルごとのリンク",
			DefaultBase: "LOCALAPPDATA",
			DefaultDir:  "fnm_multishells",
			Delete:      &Deletion{LinksOnly: true, OlderThan: 7 * 24 * time.Hour, KeepPathEntries: true},
			Note:        "fnm はシェルを起動するたびにここへリンクを 1 つ作り、削除しません。Skilli は 7 日以上前のリンクのうち、PATH に入っていないものだけを削除します。リンク先の Node は削除しません。7 日以上開いたままのシェルでは、node が見つからなくなることがあります。",
		},
		{
			ID:          "nvm",
			Name:        "nvm-windows でインストールした Node",
			EnvDir:      "NVM_HOME",
			DefaultBase: "APPDATA",
			DefaultDir:  "nvm",
			Note:        "計測のみ。使わないバージョンは nvm uninstall <バージョン> で削除します。",
		},
		{
			ID:         "bun",
			Name:       "Bun キャッシュ",
			EnvDir:     "BUN_INSTALL_CACHE_DIR",
			DefaultDir: `.bun\install\cache`,
			Delete:     &Deletion{},
			Busy:       []string{"bun.exe"},
			Note:       "bun pm cache rm は package.json のあるフォルダでしか実行できないため、Skilli が中身を直接削除します。次のインストールで再ダウンロードされます。",
		},
		{
			ID:     "deno",
			Name:   "Deno キャッシュ",
			Locate: Command{Name: "deno", Args: []string{"info", "--json"}},
			Parse:  stringInJSON("denoDir"),
			Clean:  &Command{Name: "deno", Args: []string{"clean"}},
			Note:   "deno clean で Deno のグローバルなモジュールキャッシュ (DENO_DIR) をすべて削除します。次の実行で再ダウンロードされます。",
		},
		{
			ID:          "playwright",
			Name:        "Playwright のブラウザ",
			EnvDir:      "PLAYWRIGHT_BROWSERS_PATH",
			DefaultBase: "LOCALAPPDATA",
			DefaultDir:  "ms-playwright",
			Delete:      &Deletion{},
			Note:        "Playwright がテスト用にダウンロードしたブラウザを、Skilli が直接削除します。使うときは npx playwright install でダウンロードし直します。",
		},
		// Go
		{
			ID:     "go-build",
			Name:   "Go ビルドキャッシュ",
			Locate: Command{Name: "go", Args: []string{"env", "GOCACHE"}},
			Clean:  &Command{Name: "go", Args: []string{"clean", "-cache"}},
			Busy:   []string{"go.exe", "gopls.exe"},
			Note:   "go clean -cache でビルドキャッシュを削除します。次回のビルドとテストは時間がかかります。",
		},
		{
			ID:     "go-mod",
			Name:   "Go モジュールキャッシュ",
			Locate: Command{Name: "go", Args: []string{"env", "GOMODCACHE"}},
			Clean:  &Command{Name: "go", Args: []string{"clean", "-modcache"}},
			Busy:   []string{"go.exe", "gopls.exe"},
			Note:   "注意: go clean -modcache は、使用中かどうかに関係なくモジュールキャッシュをすべて削除します。すべてのプロジェクトが次のビルドで依存を再ダウンロードし、オフラインではビルドできなくなります。",
		},
		// Rust
		{
			ID:         "cargo",
			Name:       "Cargo のレジストリ",
			EnvDir:     "CARGO_HOME",
			DefaultDir: ".cargo",
			SubDir:     "registry",
			Delete:     &Deletion{},
			Busy:       []string{"cargo.exe", "rustc.exe"},
			Note:       "Cargo には全体を削除するコマンドがないため、Skilli が中身を直接削除します。Cargo の公式ドキュメントでは、このキャッシュはどの部分を削除しても再取得されるとされています。Rust 1.88 以降の Cargo は、3 か月使われていないファイルを自動でも削除します。プロジェクトごとのビルド結果 (target フォルダ) は対象外です。",
		},
		{
			ID:         "cargo-git",
			Name:       "Cargo の Git 依存",
			EnvDir:     "CARGO_HOME",
			DefaultDir: ".cargo",
			SubDir:     "git",
			Delete:     &Deletion{},
			Busy:       []string{"cargo.exe", "rustc.exe"},
			Note:       "Git リポジトリから取得した依存を、Skilli が直接削除します。次のビルドで取得し直されます。",
		},
		{
			ID:         "rustup",
			Name:       "Rust のツールチェーン",
			EnvDir:     "RUSTUP_HOME",
			DefaultDir: ".rustup",
			SubDir:     "toolchains",
			Note:       "計測のみ。インストールした Rust のツールチェーンです。使わないものは rustup toolchain uninstall で削除します。",
		},
		// .NET
		{
			ID:     "nuget",
			Name:   "NuGet のグローバルパッケージ",
			Locate: Command{Name: "dotnet", Args: []string{"nuget", "locals", "global-packages", "--list", "--force-english-output"}},
			Parse:  valueAfter("global-packages:"),
			Clean:  &Command{Name: "dotnet", Args: []string{"nuget", "locals", "global-packages", "--clear"}},
			Note:   "dotnet nuget locals global-packages --clear で、復元したパッケージをすべて削除します。次の dotnet restore で再ダウンロードされ、オフラインでは復元できなくなります。ビルド中は実行しないでください。",
		},
		{
			ID:     "nuget-http",
			Name:   "NuGet の HTTP キャッシュ",
			Locate: Command{Name: "dotnet", Args: []string{"nuget", "locals", "http-cache", "--list", "--force-english-output"}},
			Parse:  valueAfter("http-cache:"),
			Clean:  &Command{Name: "dotnet", Args: []string{"nuget", "locals", "http-cache", "--clear"}},
			Note:   "dotnet nuget locals http-cache --clear で、パッケージのダウンロード時に保存した HTTP のキャッシュを削除します。",
		},
		// Java
		{
			ID:         "gradle",
			Name:       "Gradle キャッシュ",
			EnvDir:     "GRADLE_USER_HOME",
			DefaultDir: ".gradle",
			SubDir:     "caches",
			Delete:     &Deletion{},
			Busy:       []string{"gradle.exe"},
			Note:       "Gradle には全体を削除するコマンドがないため、Skilli が中身を直接削除します。Gradle のデーモンが使っているファイルは削除できずに残るので、先に gradle --stop を実行してください。Gradle は古いキャッシュを自動でも削除します。",
		},
		{
			ID:         "maven",
			Name:       "Maven のローカルリポジトリ",
			DefaultDir: `.m2\repository`,
			Delete:     &Deletion{},
			Busy:       []string{"mvn.exe"},
			Note:       "注意: Maven には全体を削除するコマンドがないため、Skilli が中身を直接削除します。mvn install で入れた自分の成果物も削除され、作り直すまで参照できなくなります。settings.xml で場所を変えている場合は、既定の場所だけが対象です。",
		},
		// PHP, Dart
		{
			ID:     "composer",
			Name:   "Composer キャッシュ",
			Locate: Command{Name: "composer", Args: []string{"config", "--global", "--absolute", "cache-dir"}},
			Clean:  &Command{Name: "composer", Args: []string{"clear-cache"}},
			Note:   "composer clear-cache で Composer のキャッシュをすべて削除します。次のインストールで再ダウンロードされます。",
		},
		{
			ID:          "dart-pub",
			Name:        "Dart / Flutter の pub キャッシュ",
			EnvDir:      "PUB_CACHE",
			DefaultBase: "LOCALAPPDATA",
			DefaultDir:  `Pub\Cache`,
			Clean:       &Command{Name: "dart", Args: []string{"pub", "cache", "clean", "--force"}},
			Executable:  flutterBundledDart,
			Note:        "注意: dart pub cache clean は pub のキャッシュをすべて削除します。各プロジェクトで dart pub get か flutter pub get を実行し直すまで、ビルドできなくなります。Flutter の dart は、Flutter に同梱された Dart SDK を直接実行します。",
		},
		// Other
		{
			ID:     "mise",
			Name:   "mise キャッシュ",
			Locate: Command{Name: "mise", Args: []string{"cache", "path"}},
			Clean:  &Command{Name: "mise", Args: []string{"cache", "prune"}},
			Note:   "mise cache prune で、最近使われていないキャッシュ (バージョンの一覧やリリースの情報など) だけを削除します。インストール済みのツールは削除しません。",
		},
		{
			ID:         "huggingface",
			Name:       "Hugging Face のキャッシュ",
			EnvDir:     "HF_HOME",
			DefaultDir: `.cache\huggingface`,
			// HF_HOME also holds the login token, which must survive.
			Delete: &Deletion{Paths: []string{"hub", "xet", "assets"}},
			Note:   "注意: ダウンロードしたモデルやデータセット (hub、xet、assets) を、Skilli が直接削除します。使うときは再ダウンロードになり、大きなモデルは時間がかかります。ログイン用のトークンは削除しません。",
		},
		{
			ID:     "temp",
			Name:   "ユーザーの一時フォルダ",
			EnvDir: "TEMP",
			// The same rule as Windows' Disk Cleanup, which leaves files
			// changed within a week and read-only, hidden or system files.
			Delete: &Deletion{OlderThan: 7 * 24 * time.Hour, KeepProtected: true},
			Note:   "Windows のディスククリーンアップと同じく、7 日以上更新されていないファイルだけを Skilli が直接削除します。読み取り専用、隠し、システムのファイルと、使用中のファイルは残します。インストールの途中などで後から使うファイルが消えることがあるため、アプリを終了してから実行してください。",
		},
	}
}

// FromCustom turns a target the user added into a Target. Its folder goes
// through the same checks as Skilli's own: it must not be or hold a
// protected folder, nor be reached through a link.
func FromCustom(c config.CustomTarget) Target {
	t := Target{ID: c.ID, Name: c.Name, Custom: true, Dir: c.Dir}
	if c.UsesCommand() && len(c.Command) > 0 {
		t.Clean = &Command{Name: c.Command[0], Args: c.Command[1:]}
	}
	if c.UsesDelete() {
		t.Delete = &Deletion{}
	}
	switch c.Method {
	case config.MethodCommand:
		t.Note = "自分で追加した対象です。設定したコマンドで掃除します。"
	case config.MethodDelete:
		t.Note = "自分で追加した対象です。Skilli がフォルダの中身を直接削除します。"
	case config.MethodCommandThenDelete:
		t.Note = "自分で追加した対象です。設定したコマンドで掃除し、コマンドが見つからないときは Skilli が中身を直接削除します。"
	default:
		t.Note = "自分で追加した対象です。Skilli が中身を直接削除します。"
	}
	return t
}

// Find returns the target with the given ID among targets, or the copy of
// a per-drive built-in target for a drive letter, as pnpm-d.
func Find(targets []Target, id string) (Target, bool) {
	if i := slices.IndexFunc(targets, func(t Target) bool { return t.ID == id }); i >= 0 {
		return targets[i], true
	}
	return FindTarget(id)
}

// TargetsFor returns Targets plus, for each per-drive target, a copy for
// every drive in roots (such as `D:\`) other than the drive of home. The
// copy's ID ends in the drive letter, as pnpm-d.
//
//	targets := cleanup.TargetsFor([]string{`C:\`, `D:\`}, `C:\Users\me`)
func TargetsFor(roots []string, home string) []Target {
	homeVolume := filepath.VolumeName(home)
	var targets []Target
	for _, t := range Targets() {
		targets = append(targets, t)
		if !t.PerDrive {
			continue
		}
		for _, root := range roots {
			if volume := filepath.VolumeName(root); volume != "" && !strings.EqualFold(volume, homeVolume) {
				targets = append(targets, t.onDrive(volume))
			}
		}
	}
	return targets
}

// IsDriveCopy reports whether t is the copy of a per-drive target made by
// TargetsFor, which lists show only when the drive has the cache.
func (t Target) IsDriveCopy() bool { return t.WorkDir != "" }

func (t Target) onDrive(volume string) Target {
	letter := strings.ToLower(strings.TrimSuffix(volume, ":"))
	t.ID += "-" + letter
	t.Name += " (" + strings.ToUpper(volume) + ")"
	t.WorkDir = strings.ToUpper(volume) + `\`
	t.RequireDir = t.WorkDir + t.DriveStore
	t.PerDrive = false
	return t
}

// FindTarget returns the built-in target with the given ID, including the
// copy of a per-drive target for a drive letter, as pnpm-d.
func FindTarget(id string) (Target, bool) {
	targets := Targets()
	if i := slices.IndexFunc(targets, func(t Target) bool { return t.ID == id }); i >= 0 {
		return targets[i], true
	}
	base, letter, ok := strings.CutLast(id, "-")
	if !ok || len(letter) != 1 || letter[0] < 'a' || letter[0] > 'z' {
		return Target{}, false
	}
	i := slices.IndexFunc(targets, func(t Target) bool { return t.ID == base && t.PerDrive })
	if i < 0 {
		return Target{}, false
	}
	return targets[i].onDrive(letter + ":"), true
}

// valueAfter finds the line that starts with label, as in
// "global-packages: C:\Users\me\.nuget\packages\", and returns the rest of
// it without surrounding quotes. Other lines, such as a first-run banner,
// are ignored.
func valueAfter(label string) func(string) (string, error) {
	return func(output string) (string, error) {
		for line := range strings.Lines(output) {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), label); ok {
				return strings.Trim(strings.TrimSpace(rest), `"`), nil
			}
		}
		return "", fmt.Errorf("no line starting with %q", label)
	}
}

// flutterBundledDart replaces Flutter's bin\dart.bat with the Dart SDK
// that Flutter keeps in bin\cache\dart-sdk. The wrapper checks the Flutter
// checkout with git on every run and may download a Dart SDK or rebuild
// the flutter tool, which a cache cleanup must not set off. If Flutter has
// not downloaded its Dart SDK yet, the wrapper would, so dart is refused.
func flutterBundledDart(found string) (string, error) {
	dir := filepath.Dir(found)
	if !strings.EqualFold(filepath.Base(found), "dart.bat") {
		return found, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "flutter.bat")); err != nil {
		return found, nil
	}
	bundled := filepath.Join(dir, "cache", "dart-sdk", "bin", "dart.exe")
	if _, err := os.Stat(bundled); err != nil {
		return "", fmt.Errorf("Flutter has not downloaded its Dart SDK to %s yet; run flutter once first", bundled)
	}
	return bundled, nil
}

// majorVersion reads the major version from output such as "4.5.1" or
// "1.22.22", the first line of yarn --version.
func majorVersion(output string) (int, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	major, _, _ := strings.Cut(strings.TrimSpace(line), ".")
	return strconv.Atoi(major)
}

// stringInJSON reads a JSON object and returns the string under key, such
// as denoDir in the output of deno info --json.
func stringInJSON(key string) func(string) (string, error) {
	return func(output string) (string, error) {
		var fields map[string]any
		if err := json.Unmarshal([]byte(output), &fields); err != nil {
			return "", err
		}
		value, ok := fields[key].(string)
		if !ok {
			return "", fmt.Errorf("no string %s in the output", key)
		}
		return value, nil
	}
}

// firstOfJSONList reads a JSON object and returns the first string in the
// list under key, such as the first of conda's pkgs_dirs, which is where
// conda puts new packages.
func firstOfJSONList(key string) func(string) (string, error) {
	return func(output string) (string, error) {
		var fields map[string]any
		if err := json.Unmarshal([]byte(output), &fields); err != nil {
			return "", err
		}
		list, _ := fields[key].([]any)
		if len(list) == 0 {
			return "", fmt.Errorf("no %s in the output", key)
		}
		first, ok := list[0].(string)
		if !ok {
			return "", fmt.Errorf("%s[0] is not a string", key)
		}
		return first, nil
	}
}
