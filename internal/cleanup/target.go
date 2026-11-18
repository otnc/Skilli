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
	// EnvDir names an environment variable that holds the folder.
	EnvDir string
	// Clean is the tool's own cleanup command, or nil for a target that
	// is only measured.
	Clean *Command
	// Note tells the user what the cleanup removes and what it costs.
	Note string
}

// Cleanable reports whether the target has a cleanup command.
func (t Target) Cleanable() bool { return t.Clean != nil }

// Targets returns the built-in targets in display order.
//
// A cleanup command is added only when the tool documents it as removing
// cache data that it can download or rebuild again; everything else stays
// measure-only so that the user can open the folder and decide.
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
			ID:     "go-mod",
			Name:   "Go モジュールキャッシュ",
			Locate: Command{Name: "go", Args: []string{"env", "GOMODCACHE"}},
			Note:   "計測のみ。削除するとオフラインでビルドできなくなるため、Skilli からは掃除しません。",
		},
		{
			ID:     "npm",
			Name:   "npm キャッシュ",
			Locate: Command{Name: "npm", Args: []string{"config", "get", "cache"}},
			Note:   "計測のみ。npm のキャッシュは npm 自身が整合性を管理します。",
		},
		{
			ID:     "temp",
			Name:   "ユーザーの一時フォルダ",
			EnvDir: "TEMP",
			Note:   "計測のみ。使用中のアプリのファイルが含まれるため、中身を確認してから手動で削除してください。",
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
