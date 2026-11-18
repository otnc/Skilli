// Package cleanup measures cache folders and cleans them up only through
// the official command of the tool that owns them.
//
// Skilli never deletes files itself. A cleanup is a Plan that the user
// confirms, and Engine.Apply checks again right before running it that the
// tool, its executable and its folder are still the ones the user saw.
package cleanup

import "slices"

// Command is an executable name looked up in PATH and its arguments.
type Command struct {
	Name string
	Args []string
}

// Target is a folder Skilli can measure and possibly clean.
type Target struct {
	ID   string
	Name string
	// Locate prints the folder on stdout. Ignored when EnvDir is set.
	Locate Command
	// MissingIfLocateFails treats a failing Locate as the tool being absent,
	// for commands such as python -m pip that fail when pip is not installed.
	MissingIfLocateFails bool
	// EnvDir names an environment variable that holds the folder.
	EnvDir string
	// Clean is the tool's own cleanup command, or nil for a target that
	// Skilli does not clean itself.
	Clean *Command
	// SettingsURI is an ms-settings: page where Windows cleans the target,
	// for a folder that no single tool owns.
	SettingsURI string
	// Note tells the user what the cleanup removes and what it costs.
	Note string
}

// Cleanable reports whether the target has a cleanup command.
func (t Target) Cleanable() bool { return t.Clean != nil }

// Targets returns the built-in targets in display order.
//
// A cleanup command is added only when the tool documents it as removing
// cache data that it can download or rebuild again. A folder shared by many
// apps, such as %TEMP%, is left to Windows' own cleanup, which skips files
// in use.
func Targets() []Target {
	return []Target{
		{
			ID:     "uv",
			Name:   "uv キャッシュ",
			Locate: Command{Name: "uv", Args: []string{"cache", "dir"}},
			Clean:  &Command{Name: "uv", Args: []string{"cache", "prune"}},
			Note:   "uv cache prune で、どの環境からも使われていないキャッシュだけを削除します。必要になれば再ダウンロードされます。",
		},
		{
			ID:     "go-build",
			Name:   "Go ビルドキャッシュ",
			Locate: Command{Name: "go", Args: []string{"env", "GOCACHE"}},
			Clean:  &Command{Name: "go", Args: []string{"clean", "-cache"}},
			Note:   "go clean -cache でビルドキャッシュを削除します。次回のビルドとテストは時間がかかります。",
		},
		{
			ID:     "pnpm",
			Name:   "pnpm ストア",
			Locate: Command{Name: "pnpm", Args: []string{"store", "path"}},
			Clean:  &Command{Name: "pnpm", Args: []string{"store", "prune"}},
			Note:   "pnpm store prune で、どのプロジェクトからも参照されていないパッケージを削除します。pnpm install の実行中は使わないでください。",
		},
		{
			ID:     "npm",
			Name:   "npm キャッシュ",
			Locate: Command{Name: "npm", Args: []string{"config", "get", "cache"}},
			Clean:  &Command{Name: "npm", Args: []string{"cache", "verify"}},
			Note:   "npm cache verify で、キャッシュの整合性を確かめ、不要になったデータだけを削除します。空く容量は少ないことがあります。",
		},
		{
			ID:                   "pip",
			Name:                 "pip キャッシュ",
			Locate:               Command{Name: "python", Args: []string{"-m", "pip", "cache", "dir"}},
			MissingIfLocateFails: true,
			Clean:                &Command{Name: "python", Args: []string{"-m", "pip", "cache", "purge"}},
			Note:                 "pip cache purge で、PATH の python が使う pip のキャッシュをすべて削除します。必要になれば再ダウンロードされます。",
		},
		{
			ID:     "poetry",
			Name:   "Poetry キャッシュ",
			Locate: Command{Name: "poetry", Args: []string{"config", "cache-dir"}},
			Note:   "計測のみ。Poetry は既定でこのフォルダの中にプロジェクトの仮想環境 (virtualenvs) を作るため、Skilli からは掃除しません。",
		},
		{
			ID:     "mise",
			Name:   "mise キャッシュ",
			Locate: Command{Name: "mise", Args: []string{"cache", "path"}},
			Clean:  &Command{Name: "mise", Args: []string{"cache", "prune"}},
			Note:   "mise cache prune で、最近使われていないキャッシュ (バージョンの一覧やリリースの情報など) だけを削除します。インストール済みのツールは削除しません。",
		},
		{
			ID:     "go-mod",
			Name:   "Go モジュールキャッシュ",
			Locate: Command{Name: "go", Args: []string{"env", "GOMODCACHE"}},
			Clean:  &Command{Name: "go", Args: []string{"clean", "-modcache"}},
			Note:   "注意: go clean -modcache は、使用中かどうかに関係なくモジュールキャッシュをすべて削除します。すべてのプロジェクトが次のビルドで依存を再ダウンロードし、オフラインではビルドできなくなります。",
		},
		{
			ID:          "temp",
			Name:        "ユーザーの一時フォルダ",
			EnvDir:      "TEMP",
			SettingsURI: "ms-settings:storagesense",
			Note:        "使用中のアプリのファイルが含まれるため、Skilli は削除しません。Windows の「設定 > システム > ストレージ > 一時ファイル」で、使用中のファイルを避けて削除できます。",
		},
	}
}

// FindTarget returns the built-in target with the given ID.
func FindTarget(id string) (Target, bool) {
	targets := Targets()
	i := slices.IndexFunc(targets, func(t Target) bool { return t.ID == id })
	if i < 0 {
		return Target{}, false
	}
	return targets[i], true
}
