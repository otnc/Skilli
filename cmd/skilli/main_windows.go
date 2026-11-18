package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"encoding/json/v2"

	"golang.org/x/sys/windows"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/desktop"
	"github.com/otnc/Skilli/internal/storage"
)

var version = "0.1.0-dev"

// Exit codes shared by every command.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitNeedConfirm = 3
	exitPartial     = 4
)

const guiExecutable = "skilli-gui.exe"

var desktopFlags = []string{"--open", "--background", "--check-updates"}

func main() {
	if len(os.Args) == 1 || len(os.Args) == 2 && slices.Contains(desktopFlags, os.Args[1]) {
		os.Exit(startDesktop(os.Args[1:]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(newCLI().run(ctx, os.Args[1:]))
}

// startDesktop hands the console build's desktop commands to skilli-gui.exe,
// so that the terminal returns at once instead of hosting the tray app.
// Under go run there is no skilli-gui.exe and the app runs in this process.
func startDesktop(args []string) int {
	self, err := os.Executable()
	if err == nil && !strings.EqualFold(filepath.Base(self), guiExecutable) {
		gui := filepath.Join(filepath.Dir(self), guiExecutable)
		if _, statErr := os.Stat(gui); statErr == nil {
			if err := startDetached(gui, args...); err != nil {
				fmt.Fprintf(os.Stderr, "Skilli を起動できませんでした: %v\n", err)
				return exitError
			}
			return exitOK
		}
	}
	background := slices.Contains(args, "--background")
	checkUpdates := slices.Contains(args, "--check-updates")
	if err := desktop.Run(version, background, checkUpdates); err != nil {
		fmt.Fprintf(os.Stderr, "Skilli を起動できませんでした: %v\n", err)
		return exitError
	}
	return exitOK
}

func startDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

type cli struct {
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	interactive bool
	readDrives  func() (storage.Snapshot, error)
	newEngine   func() (*cleanup.Engine, error)
	openUpdater func() error
}

func newCLI() *cli {
	return &cli{
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		interactive: isConsole(os.Stdin),
		readDrives:  storage.Read,
		newEngine:   cleanup.New,
		openUpdater: openUpdater,
	}
}

func isConsole(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

const helpText = `Skilli（スキリ）

  skilli                       ウィンドウを開く
  skilli --background          トレイに常駐する
  skilli status [--json]       固定ドライブの空き容量を確認する
  skilli scan [ID...] [--json] キャッシュの場所とサイズを計測する
  skilli clean <ID>            確認のうえ、ツール公式のコマンドでキャッシュを掃除する
  skilli history [--days N] [--json]
                               掃除の履歴を表示する
  skilli update                更新を確認する画面を開く
  skilli --version             バージョンを表示する

終了コード: 0 成功、1 実行エラー、2 入力不正、3 確認が必要、4 一部だけ完了`

func (c *cli) run(ctx context.Context, args []string) int {
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(c.stdout, helpText)
		return exitOK
	case "--version", "version":
		if len(args) == 1 {
			fmt.Fprintln(c.stdout, "Skilli", version)
			return exitOK
		}
	case "status":
		return c.status(args[1:])
	case "scan":
		return c.scan(ctx, args[1:])
	case "clean":
		return c.clean(ctx, args[1:])
	case "history":
		return c.history(args[1:])
	case "update":
		if len(args) == 1 {
			if err := c.openUpdater(); err != nil {
				fmt.Fprintf(c.stderr, "更新画面を開けませんでした: %v\n", err)
				return exitError
			}
			fmt.Fprintln(c.stdout, "Skilli の更新画面を開きます。")
			return exitOK
		}
	}
	fmt.Fprintln(c.stderr, "指定したコマンドを認識できません。skilli --help を参照してください。")
	return exitUsage
}

func openUpdater() error {
	current, err := os.Executable()
	if err != nil {
		return err
	}
	return startDetached(filepath.Join(filepath.Dir(current), guiExecutable), "--check-updates")
}

// parseFlags parses args with a JSON switch. It returns ok = false after
// printing usage, with the exit code to use.
func (c *cli) parseFlags(name, usage string, args []string, define func(*flag.FlagSet)) (flags *flag.FlagSet, asJSON bool, code int, ok bool) {
	flags = flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonFlag := flags.Bool("json", false, "")
	if define != nil {
		define(flags)
	}
	if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		fmt.Fprintln(c.stdout, "使い方: "+usage)
		return nil, false, exitOK, false
	}
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(c.stderr, "使い方: "+usage)
		return nil, false, exitUsage, false
	}
	return flags, *jsonFlag, 0, true
}

func (c *cli) writeJSON(value any) int {
	data, err := json.Marshal(value)
	if err == nil {
		_, err = fmt.Fprintln(c.stdout, string(data))
	}
	if err != nil {
		fmt.Fprintf(c.stderr, "結果を JSON で書き込めませんでした: %v\n", err)
		return exitError
	}
	return exitOK
}

func (c *cli) status(args []string) int {
	const usage = "skilli status [--json]"
	flags, asJSON, code, ok := c.parseFlags("status", usage, args, nil)
	if !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(c.stderr, "使い方: "+usage)
		return exitUsage
	}
	snapshot, err := c.readDrives()
	if err != nil {
		fmt.Fprintf(c.stderr, "ドライブを確認できませんでした: %v\n", err)
		return exitError
	}

	switch {
	case asJSON:
		if code := c.writeJSON(snapshot); code != exitOK {
			return code
		}
	case len(snapshot.Drives) == 0:
		fmt.Fprintln(c.stdout, "固定ドライブが見つかりませんでした。")
	default:
		for _, drive := range snapshot.Drives {
			if !drive.Measured() {
				fmt.Fprintf(c.stdout, "%s  計測できませんでした: %s\n", drive.DisplayPath(), drive.Error)
				continue
			}
			fmt.Fprintf(c.stdout, "%s  空き %s / %s\n", drive.DisplayPath(), storage.GiB(drive.FreeBytes), storage.GiB(drive.CapacityBytes))
		}
	}
	for _, drive := range snapshot.Drives {
		if !drive.Measured() {
			return exitPartial
		}
	}
	return exitOK
}

// scanTimeout bounds each target's measurement so that a huge folder does
// not hang the command; the result is then marked partial.
const scanTimeout = 2 * time.Minute

type scanReport struct {
	SchemaVersion int            `json:"schema_version"`
	MeasuredAt    time.Time      `json:"measured_at"`
	Targets       []cleanup.Scan `json:"targets"`
}

func (c *cli) scan(ctx context.Context, args []string) int {
	const usage = "skilli scan [ID...] [--json]"
	flags, asJSON, code, ok := c.parseFlags("scan", usage, args, nil)
	if !ok {
		return code
	}
	targets, ok := c.selectTargets(flags.Args())
	if !ok {
		return exitUsage
	}
	engine, err := c.newEngine()
	if err != nil {
		fmt.Fprintf(c.stderr, "計測を準備できませんでした: %v\n", err)
		return exitError
	}
	report := scanReport{SchemaVersion: 1, Targets: []cleanup.Scan{}}
	partial := false
	for _, target := range targets {
		targetCtx, cancel := context.WithTimeout(ctx, scanTimeout)
		result := engine.Scan(targetCtx, target)
		cancel()
		if result.Status == cleanup.Failed || result.Size != nil && result.Size.Partial {
			partial = true
		}
		report.Targets = append(report.Targets, result)
		if ctx.Err() != nil {
			partial = true
			break
		}
	}
	report.MeasuredAt = time.Now()

	if asJSON {
		if code := c.writeJSON(report); code != exitOK {
			return code
		}
	} else {
		for _, result := range report.Targets {
			fmt.Fprintf(c.stdout, "%-9s %s  %s\n", result.ID, result.Name, describeScan(result))
		}
	}
	if partial {
		return exitPartial
	}
	return exitOK
}

func describeScan(result cleanup.Scan) string {
	switch result.Status {
	case cleanup.Missing:
		return "見つかりません"
	case cleanup.Failed:
		return "計測できませんでした: " + result.Error
	}
	text := storage.Size(result.Size.Bytes) + "  " + result.Dir
	if result.Size.Partial {
		text += "  (一部のみ計測)"
	}
	return text
}

func (c *cli) selectTargets(ids []string) ([]cleanup.Target, bool) {
	if len(ids) == 0 {
		return cleanup.Targets(), true
	}
	var targets []cleanup.Target
	for _, id := range ids {
		target, ok := cleanup.FindTarget(id)
		if !ok {
			fmt.Fprintf(c.stderr, "ID %q のターゲットはありません。skilli scan で一覧を確認してください。\n", id)
			return nil, false
		}
		targets = append(targets, target)
	}
	return targets, true
}

func (c *cli) clean(ctx context.Context, args []string) int {
	const usage = "skilli clean <ID>"
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(c.stdout, "使い方: "+usage)
		return exitOK
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(c.stderr, "使い方: "+usage)
		return exitUsage
	}
	targets, ok := c.selectTargets(args)
	if !ok {
		return exitUsage
	}
	target := targets[0]
	if !target.Cleanable() {
		fmt.Fprintf(c.stderr, "%s は計測のみの対象で、Skilli からは掃除できません。\n%s\n", target.Name, target.Note)
		return exitUsage
	}
	engine, err := c.newEngine()
	if err != nil {
		fmt.Fprintf(c.stderr, "掃除を準備できませんでした: %v\n", err)
		return exitError
	}
	measureCtx, cancel := context.WithTimeout(ctx, scanTimeout)
	plan, err := engine.NewPlan(measureCtx, target)
	cancel()
	if err != nil {
		fmt.Fprintf(c.stderr, "掃除の計画を作れませんでした: %v\n", err)
		return exitError
	}

	fmt.Fprintf(c.stdout, "対象      %s\nフォルダ  %s\n現在      %s", target.Name, plan.Dir, storage.Size(plan.Size.Bytes))
	if plan.Size.Partial {
		fmt.Fprint(c.stdout, " 以上 (一部のみ計測)")
	}
	fmt.Fprintf(c.stdout, "\n実行      %s\n\n%s\n削除したファイルはごみ箱に入らず、元に戻せません。\n", plan.Command(), target.Note)

	if !c.interactive {
		fmt.Fprintln(c.stderr, "掃除には確認が必要です。対話できるターミナルから実行してください。")
		return exitNeedConfirm
	}
	fmt.Fprint(c.stdout, "実行しますか? [y/N]: ")
	answer, _ := bufio.NewReader(c.stdin).ReadString('\n')
	if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
		fmt.Fprintln(c.stdout, "中止しました。")
		return exitOK
	}

	result, err := engine.Apply(ctx, plan, "cli")
	if err != nil {
		fmt.Fprintf(c.stderr, "掃除できませんでした: %v\n", describeApplyError(err))
		return exitError
	}
	fmt.Fprintf(c.stdout, "完了しました。%s → %s\n", storage.Size(result.SizeBefore.Bytes), storage.Size(result.SizeAfter.Bytes))
	if result.FreeAfter > result.FreeBefore {
		fmt.Fprintf(c.stdout, "ドライブの空きは %s 増えました (ほかのアプリの読み書きも含みます)。\n", storage.Size(result.FreeAfter-result.FreeBefore))
	}
	return exitOK
}

func describeApplyError(err error) string {
	switch {
	case errors.Is(err, cleanup.ErrBusy):
		return "ほかの掃除が実行中です。終わってからやり直してください。"
	case errors.Is(err, cleanup.ErrPlanExpired):
		return "確認から時間がたちすぎました。もう一度実行してください。"
	case errors.Is(err, cleanup.ErrPlanChanged):
		return "確認したあとにツールかフォルダが変わったため中止しました。もう一度実行してください。(" + err.Error() + ")"
	default:
		return err.Error()
	}
}

func (c *cli) history(args []string) int {
	const usage = "skilli history [--days N] [--json]"
	var days *int
	flags, asJSON, code, ok := c.parseFlags("history", usage, args, func(f *flag.FlagSet) {
		days = f.Int("days", 30, "")
	})
	if !ok {
		return code
	}
	if flags.NArg() != 0 || *days < 1 {
		fmt.Fprintln(c.stderr, "使い方: "+usage)
		return exitUsage
	}
	engine, err := c.newEngine()
	if err != nil {
		fmt.Fprintf(c.stderr, "履歴を開けませんでした: %v\n", err)
		return exitError
	}
	entries, err := engine.History.Read(time.Now().AddDate(0, 0, -*days))
	if err != nil {
		fmt.Fprintf(c.stderr, "履歴を読めませんでした: %v\n", err)
		return exitError
	}
	if asJSON {
		return c.writeJSON(struct {
			SchemaVersion int             `json:"schema_version"`
			Entries       []cleanup.Entry `json:"entries"`
		}{1, append([]cleanup.Entry{}, entries...)})
	}
	if len(entries) == 0 {
		fmt.Fprintln(c.stdout, "履歴はありません。")
	}
	for _, e := range entries {
		fmt.Fprintf(c.stdout, "%s  %-9s %-9s %s → %s  %s\n", e.Time.Local().Format("2006/01/02 15:04"), e.Target, e.Status,
			storage.Size(e.SizeBefore), storage.Size(e.SizeAfter), e.Error)
	}
	return exitOK
}
