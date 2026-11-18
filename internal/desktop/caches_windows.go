package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/storage"
)

const scanTimeout = 2 * time.Minute

type cacheView struct {
	engine    *cleanup.Engine
	engineErr error
	scans     []cleanup.Scan
	scanning  bool
	scannedAt time.Time
	// cleaning is the ID of the target being cleaned, or "".
	cleaning string
}

func (d *dashboard) busy() bool {
	return d.caches.scanning || d.caches.cleaning != ""
}

func (d *dashboard) scanCaches() {
	if d.caches.engine == nil || d.busy() {
		return
	}
	d.caches.scanning = true
	d.caches.scans = nil
	d.invalidate()
	engine := d.caches.engine
	go func() {
		for _, target := range cleanup.Targets() {
			ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
			result := engine.Scan(ctx, target)
			cancel()
			mygo.RunOnMain(func() {
				d.caches.scans = append(d.caches.scans, result)
				d.invalidate()
			})
		}
		mygo.RunOnMain(func() {
			d.caches.scanning = false
			d.caches.scannedAt = time.Now()
			d.invalidate()
		})
	}()
}

func (d *dashboard) renderCaches(c *ui.Context) {
	ui.Text(c, "キャッシュ").FontSize(20).Bold()
	if d.caches.engineErr != nil {
		ui.Text(c, "キャッシュを計測できません: "+d.caches.engineErr.Error()).Wrap().TextColor(warningColor)
		return
	}
	ui.Text(c, "Skilli はファイルを直接削除しません。掃除は各ツールの公式コマンドで行い、実行前に必ず確認します。").Wrap()
	label := "キャッシュを計測"
	if d.caches.scanning {
		label = "計測中…"
	}
	// A Column stretches its children to full width; a Row keeps the
	// button at its own width like the buttons at the top.
	ui.Row(c).Children(func() {
		if ui.Button(c, label).Disabled(d.busy()).Clicked() {
			d.scanCaches()
		}
	})
	for _, scan := range d.caches.scans {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, scan.Name).Bold()
			switch scan.Status {
			case cleanup.Missing:
				ui.Text(c, "見つかりませんでした (未インストールか、フォルダがありません)。")
				return
			case cleanup.Failed:
				ui.Text(c, "計測できませんでした: "+scan.Error).Wrap()
				return
			}
			size := storage.Size(scan.Size.Bytes)
			if scan.Size.Partial {
				size += " 以上 (一部のみ計測)"
			}
			ui.Text(c, size+"  "+scan.Dir).Wrap()
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "フォルダを開く").Clicked() {
					_ = mygo.Shell.OpenPath(scan.Dir)
				}
				target, _ := cleanup.FindTarget(scan.ID)
				if target.SettingsURI != "" {
					if ui.Button(c, "Windows の設定で削除…").Tooltip(target.Note).Clicked() {
						_ = mygo.Shell.OpenExternal(target.SettingsURI)
					}
				}
				if !target.Cleanable() {
					return
				}
				label := "掃除…"
				if d.caches.cleaning == scan.ID {
					label = "掃除中…"
				}
				if ui.Button(c, label).Disabled(d.busy()).Tooltip(target.Note).Clicked() {
					d.cleanTarget(target)
				}
			})
		})
	}
	if !d.caches.scannedAt.IsZero() {
		ui.Text(c, "計測 "+d.caches.scannedAt.Format("2006/01/02 15:04:05"))
	}
}

// cleanTarget measures the target again, asks the user to confirm the
// exact command and folder, and only then runs the cleanup.
func (d *dashboard) cleanTarget(target cleanup.Target) {
	if d.busy() {
		return
	}
	d.caches.cleaning = target.ID
	d.invalidate()
	engine := d.caches.engine
	go func() {
		defer mygo.RunOnMain(func() {
			d.caches.cleaning = ""
			d.invalidate()
			d.scanCaches()
			d.refresh()
		})

		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		plan, err := engine.NewPlan(ctx, target)
		cancel()
		if err != nil {
			cleanupMessage("掃除の準備ができませんでした。", err.Error())
			return
		}
		size := storage.Size(plan.Size.Bytes)
		if plan.Size.Partial {
			size += " 以上 (一部のみ計測)"
		}
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{
			Type:    mygo.MessageWarning,
			Title:   "Skilli の掃除",
			Message: target.Name + " を掃除しますか?",
			Detail: fmt.Sprintf("フォルダ: %s\n現在のサイズ: %s\n実行するコマンド: %s\n\n%s\n\n削除されたファイルはごみ箱に入らず、元に戻せません。この確認は %d 分間有効です。",
				plan.Dir, size, plan.Command(), target.Note, int(cleanup.PlanLifetime.Minutes())),
			Buttons:       []string{"キャンセル", "掃除する"},
			CancelButton:  0,
			DefaultButton: 0,
		})
		if err != nil || answer.Button != 1 {
			return
		}
		result, err := engine.Apply(context.Background(), plan, "gui")
		if err != nil {
			cleanupMessage(target.Name+" を掃除できませんでした。", describeApplyError(err))
			return
		}
		detail := fmt.Sprintf("サイズ: %s → %s", storage.Size(result.SizeBefore.Bytes), storage.Size(result.SizeAfter.Bytes))
		if result.FreeAfter > result.FreeBefore {
			detail += fmt.Sprintf("\nドライブの空き: %s 増加 (ほかのアプリの読み書きも含みます)", storage.Size(result.FreeAfter-result.FreeBefore))
		}
		cleanupMessage(target.Name+" を掃除しました。", detail)
	}()
}

func describeApplyError(err error) string {
	switch {
	case errors.Is(err, cleanup.ErrBusy):
		return "ほかの掃除が実行中です (CLI を含む)。終わってからやり直してください。"
	case errors.Is(err, cleanup.ErrPlanExpired):
		return "確認から時間がたちすぎたため中止しました。もう一度やり直してください。"
	case errors.Is(err, cleanup.ErrPlanChanged):
		return "確認したあとにツールかフォルダが変わったため中止しました。\n" + err.Error()
	default:
		return err.Error()
	}
}

func cleanupMessage(message, detail string) {
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Title:   "Skilli の掃除",
		Message: message,
		Detail:  detail,
	})
}
