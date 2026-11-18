package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/config"
)

// customForm holds the fields of the target being added or edited.
type customForm struct {
	// id is the target being edited, or "" for a new one.
	id                 string
	name, dir, command string
	method             string
	err                string
}

// The choices of the form's method list, and what each stores.
var methodChoices = []struct{ label, method string }{
	{"コマンドを優先し、使えないときは直接削除", config.MethodCommandThenDelete},
	{"コマンドで掃除する", config.MethodCommand},
	{"Skilli が中身を直接削除する", config.MethodDelete},
}

func methodChoiceLabels() []string {
	labels := make([]string, len(methodChoices))
	for i, choice := range methodChoices {
		labels[i] = choice.label
	}
	return labels
}

func methodOfLabel(label string) string {
	for _, choice := range methodChoices {
		if choice.label == label {
			return choice.method
		}
	}
	return config.MethodDelete
}

func labelOfMethod(method string) string {
	for _, choice := range methodChoices {
		if choice.method == method {
			return choice.label
		}
	}
	return methodChoices[0].label
}

func (d *dashboard) renderCustomTargets(c *ui.Context) {
	t := c.Theme()
	sectionTitle(c, "自分で追加した対象")
	card(c, func() {
		if len(d.cfg.CustomTargets) == 0 && d.form == nil {
			muted(c, "一覧にないキャッシュのフォルダを追加できます。追加した対象も、削除の前に保護フォルダやリンクの確認をします。").Wrap()
		}
		for _, custom := range d.cfg.CustomTargets {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, custom.Name).Bold()
					muted(c, custom.Dir).SingleLine().TruncateMiddle().Tooltip(custom.Dir)
					detail := labelOfMethod(custom.Method)
					if len(custom.Command) > 0 && custom.UsesCommand() {
						detail += "  ・  " + strings.Join(custom.Command, " ")
					}
					muted(c, detail+"  ・  ID "+custom.ID).SingleLine().Ellipsis("…")
				})
				if ui.Button(c, "編集").Disabled(d.form != nil).Clicked() {
					d.form = &customForm{id: custom.ID, name: custom.Name, dir: custom.Dir,
						command: joinCommand(custom.Command), method: labelOfMethod(custom.Method)}
				}
				if ui.Button(c, "外す").Disabled(d.form != nil).Tooltip("一覧から外します。フォルダの中身は削除しません").Clicked() {
					d.cfg.CustomTargets = slices.DeleteFunc(slices.Clone(d.cfg.CustomTargets), func(other config.CustomTarget) bool {
						return other.ID == custom.ID
					})
					d.saveConfig()
				}
			})
		}
		if d.form != nil {
			if len(d.cfg.CustomTargets) > 0 {
				ui.Divider(c)
			}
			d.renderCustomForm(c)
			return
		}
		ui.Row(c).Children(func() {
			if ui.Button(c, "追加…").Clicked() {
				d.form = &customForm{method: methodChoices[0].label}
			}
		})
		if d.saveErr != nil {
			ui.Text(c, d.saveErr.Error()).Wrap().TextColor(t.Warning)
		}
	})
}

func (d *dashboard) renderCustomForm(c *ui.Context) {
	f := d.form
	ui.Column(c).Gap(8).PaddingY(4).Children(func() {
		muted(c, "名前")
		ui.TextInput(c, &f.name).Placeholder("例: 社内ツールのキャッシュ").Label("名前")
		muted(c, "フォルダ")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &f.dir).Grow(1).Placeholder(`例: C:\Users\me\.mytool\cache`).Label("フォルダ")
			if ui.Button(c, "選ぶ…").Clicked() {
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "キャッシュのフォルダを選ぶ", Directory: true})
					if err == nil && len(paths) == 1 {
						mygo.RunOnMain(func() {
							if d.form == f {
								f.dir = paths[0]
								d.invalidate()
							}
						})
					}
				}()
			}
		})
		muted(c, "掃除のコマンド (任意)")
		ui.TextInput(c, &f.command).Placeholder("例: mytool cache clean").Label("掃除のコマンド")
		muted(c, "掃除の方法")
		ui.Select(c, &f.method, methodChoiceLabels()).Label("掃除の方法")
		muted(c, "コマンドは PATH から探し、シェルを通さずにホームフォルダで実行します。空白を含む引数は \"\" で囲みます。").Wrap()
		if f.err != "" {
			ui.Text(c, f.err).Wrap().TextColor(c.Theme().Warning)
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Spacer(c)
			if ui.Button(c, "キャンセル").Clicked() {
				d.form = nil
			}
			if ui.PrimaryButton(c, "保存").Clicked() {
				if err := d.saveCustomForm(); err != nil {
					f.err = err.Error()
				} else {
					d.form = nil
				}
			}
		})
	})
}

// saveCustomForm checks the form and stores it in the settings. The folder
// must pass the same safety checks a cleanup makes, so that a target that
// would always be refused is not added.
func (d *dashboard) saveCustomForm() error {
	f := d.form
	command, err := splitCommand(f.command)
	if err != nil {
		return err
	}
	dir := filepath.Clean(strings.TrimSpace(f.dir))
	target := config.CustomTarget{
		ID:      f.id,
		Name:    strings.TrimSpace(f.name),
		Dir:     dir,
		Command: command,
		Method:  methodOfLabel(f.method),
	}
	if target.ID == "" {
		target.ID = d.cfg.NextCustomID()
	}
	switch {
	case target.Name == "":
		return errors.New("名前を入力してください。")
	case !filepath.IsAbs(dir):
		return errors.New(`フォルダは C:\ から始まる完全なパスで入力してください。`)
	case target.UsesCommand() && len(command) == 0:
		return errors.New("コマンドを入力するか、掃除の方法を「Skilli が中身を直接削除する」にしてください。")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return errors.New("フォルダが見つかりません。")
	}
	if d.caches.engine != nil && d.caches.engine.Protected != nil {
		if err := cleanup.CheckCleanable(dir, d.caches.engine.Protected()); err != nil {
			return errors.New("このフォルダは保護しているフォルダか、それを含むフォルダのため追加できません。\n" + err.Error())
		}
	}
	if err := cleanup.CheckOnDisk(dir); err != nil {
		return errors.New("このフォルダはリンクを経由しているため追加できません。\n" + err.Error())
	}

	targets := slices.Clone(d.cfg.CustomTargets)
	if i := slices.IndexFunc(targets, func(t config.CustomTarget) bool { return t.ID == target.ID }); i >= 0 {
		targets[i] = target
	} else {
		targets = append(targets, target)
	}
	next := d.cfg
	next.CustomTargets = targets
	if err := next.Validate(); err != nil {
		return err
	}
	d.cfg = next
	d.saveConfig()
	return d.saveErr
}

// splitCommand splits a command line at spaces, keeping text in double
// quotes together, as `tool clean "C:\My Cache"`.
func splitCommand(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	inQuotes, hasArg := false, false
	for _, r := range line {
		switch {
		case r == '"':
			inQuotes, hasArg = !inQuotes, true
		case (r == ' ' || r == '\t') && !inQuotes:
			if hasArg {
				args = append(args, current.String())
				current.Reset()
				hasArg = false
			}
		default:
			current.WriteRune(r)
			hasArg = true
		}
	}
	if inQuotes {
		return nil, errors.New("コマンドの \" が閉じていません。")
	}
	if hasArg {
		args = append(args, current.String())
	}
	return args, nil
}

func joinCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t") {
			arg = `"` + arg + `"`
		}
		quoted[i] = arg
	}
	return strings.Join(quoted, " ")
}
