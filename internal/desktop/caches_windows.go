package desktop

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/storage"
)

const scanTimeout = 2 * time.Minute

type cacheView struct {
	engine    *cleanup.Engine
	engineErr error
	scans     []cleanup.Scan
	scanning  bool
	// scanned and total count the targets of the scan in progress.
	scanned, total int
	scannedAt      time.Time
	// cleaning is the ID of the target being cleaned, or "".
	cleaning string
	// showMissing and showFailed open the folded lists of targets that
	// were not found or could not be measured.
	showMissing, showFailed bool
}

// found returns the measured targets, largest first, which is the order a
// user looking for space wants.
func (v *cacheView) found() []cleanup.Scan {
	var found []cleanup.Scan
	for _, scan := range v.scans {
		if scan.Status == cleanup.Found {
			found = append(found, scan)
		}
	}
	slices.SortStableFunc(found, func(a, b cleanup.Scan) int { return cmp.Compare(b.Size.Bytes, a.Size.Bytes) })
	return found
}

func (v *cacheView) withStatus(status string) []cleanup.Scan {
	var scans []cleanup.Scan
	for _, scan := range v.scans {
		if scan.Status == status {
			scans = append(scans, scan)
		}
	}
	return scans
}

func (d *dashboard) busy() bool {
	return d.caches.scanning || d.caches.cleaning != ""
}

func (d *dashboard) scanCaches() {
	if d.caches.engine == nil || d.busy() {
		return
	}
	var roots []string
	for _, drive := range d.snapshot.Drives {
		roots = append(roots, drive.Root)
	}
	home, _ := os.UserHomeDir()
	targets := cleanup.TargetsFor(roots, home)
	d.caches.scanning = true
	d.caches.scans = nil
	d.caches.scanned, d.caches.total = 0, len(targets)
	d.invalidate()
	engine := d.caches.engine
	go func() {
		for _, target := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
			result := engine.Scan(ctx, target)
			cancel()
			skip := target.IsDriveCopy() && result.Status == cleanup.Missing
			mygo.RunOnMain(func() {
				d.caches.scanned++
				if !skip {
					d.caches.scans = append(d.caches.scans, result)
				}
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

// cleanTarget measures the target again, asks the user to confirm the
// exact command or folders, and only then cleans up.
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
			cleanupMessage(target.Name+" を掃除できません", cleanup.Describe(err))
			return
		}
		size := storage.Size(plan.Size.Bytes)
		if plan.Size.Partial {
			size += " 以上"
		}
		how := "実行するコマンド: " + plan.Command()
		if plan.Deletes() {
			how = "Skilli が中身を直接削除するフォルダ:\n  " + joinLines(plan.Scope)
		}
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{
			Type:    mygo.MessageWarning,
			Title:   "Skilli の掃除",
			Message: fmt.Sprintf("%s (%s) を削除しますか?", target.Name, size),
			Detail: fmt.Sprintf("%s\n\n%s\n\n削除したファイルはごみ箱に入らず、元に戻せません。この確認は %d 分間有効です。",
				how, target.Note, int(cleanup.PlanLifetime.Minutes())),
			Buttons:       []string{"キャンセル", "削除する"},
			CancelButton:  0,
			DefaultButton: 0,
		})
		if err != nil || answer.Button != 1 {
			return
		}
		result, err := engine.Apply(context.Background(), plan, "gui")
		if err != nil {
			cleanupMessage(target.Name+" を掃除できませんでした", cleanup.Describe(err))
			return
		}
		detail := fmt.Sprintf("サイズ: %s → %s", storage.Size(result.SizeBefore.Bytes), storage.Size(result.SizeAfter.Bytes))
		if result.FreeAfter > result.FreeBefore {
			detail += fmt.Sprintf("\nドライブの空き: %s 増加 (ほかのアプリの読み書きも含みます)", storage.Size(result.FreeAfter-result.FreeBefore))
		}
		if result.Skipped > 0 {
			detail += fmt.Sprintf("\n使用中などで削除できなかった %d 件と、リンクはそのまま残しました。", result.Skipped)
		}
		cleanupMessage(target.Name+" を掃除しました", detail)
	}()
}

func joinLines(lines []string) string { return strings.Join(lines, "\n  ") }

func cleanupMessage(message, detail string) {
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Title:   "Skilli の掃除",
		Message: message,
		Detail:  detail,
	})
}
