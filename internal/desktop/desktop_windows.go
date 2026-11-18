// Package desktop runs Skilli's native Windows interface.
package desktop

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/config"
	"github.com/otnc/Skilli/internal/icon"
	"github.com/otnc/Skilli/internal/monitor"
	"github.com/otnc/Skilli/internal/storage"
)

// driveCheckInterval is how often the tray app reads drive capacity.
const driveCheckInterval = 15 * time.Minute

const lowSpaceNotificationID = "skilli-low-space"

var warningColor = ui.Hex("#b45309")

// dashboard is touched only on the main thread; goroutines hand their
// results back through mygo.RunOnMain.
type dashboard struct {
	window *mygo.Window
	tray   *mygo.Tray
	icons  map[bool][]byte

	snapshot storage.Snapshot
	readErr  error
	trayErr  error
	reading  bool

	updating   bool
	installing bool

	configPath   string
	cfg          config.Config
	configErr    error
	saveErr      error
	monitor      monitor.Monitor
	notify       bool
	thresholdGiB float64
	openAtLogin  bool
	loginErr     error

	caches cacheView
}

// Run starts the MyGo main loop. Call it from the main goroutine.
func Run(version string, background, checkUpdates bool) error {
	mygo.App.SetName("Skilli")
	mygo.App.SetVersion(version)
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}

	view := &dashboard{}
	view.loadConfig()
	view.caches.engine, view.caches.engineErr = cleanup.New()

	watchContext, stopWatching := context.WithCancel(context.Background())
	defer stopWatching()
	mygo.App.OnQuit(stopWatching)
	mygo.App.OnNotificationClick(func(id string) {
		switch id {
		case updateNotificationID:
			mygo.App.WhenReady(view.checkUpdates)
		case lowSpaceNotificationID:
			mygo.App.WhenReady(view.show)
		}
	})
	mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) {
		if view.installing || view.caches.cleaning != "" {
			event.PreventDefault()
		}
	})
	mygo.App.OnWindowAllClosed(func() {
		if view.tray == nil {
			mygo.App.Quit()
		}
	})
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		if slices.Contains(args, "--check-updates") {
			mygo.App.WhenReady(view.checkUpdates)
		} else if !slices.Contains(args, "--background") {
			mygo.App.WhenReady(view.show)
		}
	})
	mygo.Power.OnResume(func() { mygo.RunOnMain(view.refresh) })
	mygo.App.WhenReady(func() {
		view.openAtLogin = mygo.App.OpenAtLogin()
		view.trayErr = view.startTray()
		if !(background || mygo.App.WasOpenedAtLogin()) || view.trayErr != nil {
			view.show()
		} else {
			view.refresh()
		}
		if checkUpdates {
			view.checkUpdates()
		}
		go watchUpdates(watchContext)
		go view.watchDrives(watchContext)
	})
	return mygo.App.Run()
}

func (d *dashboard) watchDrives(ctx context.Context) {
	ticker := time.NewTicker(driveCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mygo.RunOnMain(d.refresh)
		}
	}
}

func (d *dashboard) loadConfig() {
	path, err := config.Path()
	if err != nil {
		d.cfg, d.configErr = config.Default(), err
	} else {
		d.configPath = path
		d.cfg, d.configErr = config.Load(path)
	}
	d.notify = d.cfg.Notify
	d.thresholdGiB = float64(d.cfg.LowSpaceBytes >> 30)
}

func (d *dashboard) saveConfig() {
	if d.configPath == "" {
		return
	}
	d.saveErr = config.Save(d.configPath, d.cfg)
	if d.saveErr == nil {
		d.configErr = nil
	}
	d.observe()
}

func (d *dashboard) startTray() error {
	d.icons = map[bool][]byte{}
	for _, low := range []bool{false, true} {
		png, err := icon.PNG(32, low)
		if err != nil {
			return err
		}
		d.icons[low] = png
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "skilli",
		Icon:    d.icons[false],
		ToolTip: "Skilli（スキリ）",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "開く", Click: func(*mygo.MenuItem, *mygo.Window) { d.show() }},
			{Label: "空き容量を更新", Click: func(*mygo.MenuItem, *mygo.Window) { d.refresh() }},
			{Label: "キャッシュを計測", Click: func(*mygo.MenuItem, *mygo.Window) { d.show(); d.scanCaches() }},
			{Label: "アプリの更新を確認", Click: func(*mygo.MenuItem, *mygo.Window) { d.checkUpdates() }},
			{Label: "終了", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		}),
	})
	if err != nil {
		return err
	}
	tray.OnClick(d.show)
	d.tray = tray
	return nil
}

func (d *dashboard) show() {
	if d.window != nil {
		d.window.Restore()
		d.window.Focus()
		return
	}
	window := mygo.NewWindow(mygo.WindowOptions{
		Title:   "Skilli（スキリ）",
		Width:   640,
		Height:  640,
		Content: ui.View(d.render),
	})
	d.window = window
	window.OnClosed(func() { d.window = nil })
	d.refresh()
}

func (d *dashboard) invalidate() {
	if d.window != nil {
		d.window.Invalidate()
	}
}

func (d *dashboard) refresh() {
	if d.reading {
		return
	}
	d.reading = true
	d.invalidate()
	go func() {
		snapshot, err := storage.Read()
		mygo.RunOnMain(func() {
			d.reading = false
			d.snapshot, d.readErr = snapshot, err
			if err == nil {
				d.observe()
			}
			d.invalidate()
		})
	}()
}

// observe runs the low-space rules on the current snapshot, shows the
// resulting notifications and updates the tray.
func (d *dashboard) observe() {
	for _, alert := range d.monitor.Observe(d.snapshot, d.cfg, time.Now()) {
		go notifyLowSpace(alert)
	}
	d.updateTray()
}

func notifyLowSpace(alert monitor.Alert) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	drive := strings.TrimSuffix(alert.Root, `\`)
	_ = mygo.NewNotification(mygo.NotificationOptions{
		ID:    lowSpaceNotificationID,
		Title: drive + " の空き容量が少なくなっています",
		Body:  fmt.Sprintf("空きは %s です (通知の基準は %s)。クリックすると Skilli を開きます。", storage.GiB(alert.FreeBytes), storage.GiB(alert.ThresholdBytes)),
	}).ShowContext(ctx)
}

func (d *dashboard) updateTray() {
	if d.tray == nil {
		return
	}
	low := d.monitor.AnyLow()
	_ = d.tray.SetIcon(d.icons[low], false)
	var parts []string
	for _, drive := range d.snapshot.Drives {
		if drive.Measured() && !d.cfg.Ignores(drive.Root) {
			parts = append(parts, drive.DisplayPath()+" "+storage.GiB(drive.FreeBytes))
		}
	}
	tip := "Skilli"
	if low {
		tip += " - 空き容量が少ないドライブがあります"
	}
	if len(parts) > 0 {
		tip += "\n空き " + strings.Join(parts, ", ")
	}
	// Windows truncates tray tooltips at 127 characters.
	if runes := []rune(tip); len(runes) > 127 {
		tip = string(runes[:126]) + "…"
	}
	d.tray.SetToolTip(tip)
}

func (d *dashboard) render(c *ui.Context) {
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Padding(24).Gap(14).Children(func() {
			ui.Text(c, "ドライブの空き容量").FontSize(26).Bold()
			if d.trayErr != nil {
				ui.Text(c, "タスクトレイにアイコンを表示できませんでした。").TextColor(warningColor)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, "空き容量を更新").Disabled(d.reading).Clicked() {
					d.refresh()
				}
				if ui.Button(c, "アプリの更新を確認").Disabled(d.updating).Clicked() {
					d.checkUpdates()
				}
			})
			if d.installing {
				ui.Text(c, "アプリを更新しています。")
			} else if d.updating {
				ui.Text(c, "アプリの更新を確認しています。")
			}
			d.renderDrives(c)
			ui.Divider(c)
			d.renderCaches(c)
			ui.Divider(c)
			d.renderSettings(c)
		})
	})
}

func (d *dashboard) renderDrives(c *ui.Context) {
	switch {
	case d.reading && len(d.snapshot.Drives) == 0:
		ui.Text(c, "ドライブを計測中…")
	case d.readErr != nil:
		ui.Text(c, "ドライブを確認できませんでした: "+d.readErr.Error()).Wrap().TextColor(warningColor)
	case len(d.snapshot.Drives) == 0:
		ui.Text(c, "固定ドライブが見つかりませんでした。")
	}
	for _, drive := range d.snapshot.Drives {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, drive.DisplayPath()).FontSize(17).Bold()
			if !drive.Measured() {
				ui.Text(c, "このドライブは計測できませんでした ("+drive.ErrorCode+")。")
				return
			}
			capacity := float64(drive.CapacityBytes)
			critical := max(capacity-float64(d.cfg.LowSpaceBytes), 0)
			ui.Meter(c, capacity-float64(drive.FreeBytes), 0, capacity,
				&ui.MeterLevels{Warning: max(capacity-float64(d.cfg.LowSpaceBytes)*1.5, 0), Critical: critical}).
				Label(drive.DisplayPath() + " の使用量")
			line := fmt.Sprintf("空き %s / %s (使用 %.0f%%)", storage.GiB(drive.FreeBytes), storage.GiB(drive.CapacityBytes), drive.UsedRatio()*100)
			text := ui.Text(c, line)
			switch {
			case d.cfg.Ignores(drive.Root):
				text.Tooltip("監視の対象外です")
			case d.monitor.Low(drive.Root):
				ui.Text(c, "空き容量が通知の基準を下回っています。").TextColor(warningColor)
			}
		})
	}
	if !d.snapshot.MeasuredAt.IsZero() {
		ui.Text(c, "最終計測 "+d.snapshot.MeasuredAt.Format("2006/01/02 15:04:05")+" (常駐中は 15 分ごとに更新)")
	}
}

func (d *dashboard) renderSettings(c *ui.Context) {
	ui.Text(c, "設定").FontSize(20).Bold()
	if d.configErr != nil {
		ui.Text(c, "設定ファイルを読み込めなかったため既定値を使っています。変更すると上書きします: "+d.configErr.Error()).Wrap().TextColor(warningColor)
	}
	if ui.Checkbox(c, &d.notify, "空き容量が少なくなったら通知する").Changed() {
		d.cfg.Notify = d.notify
		d.saveConfig()
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "通知する空き容量の基準 (GiB)")
		if ui.NumberInput(c, &d.thresholdGiB, 1, 1024, 1).Width(120).Label("通知する空き容量の基準 (GiB)").Changed() {
			d.cfg.LowSpaceBytes = uint64(d.thresholdGiB) << 30
			d.saveConfig()
		}
	})
	for _, drive := range d.snapshot.Drives {
		watched := !d.cfg.Ignores(drive.Root)
		if ui.Checkbox(c, &watched, drive.DisplayPath()+" を監視する").Changed() {
			d.cfg.IgnoredDrives = slices.DeleteFunc(slices.Clone(d.cfg.IgnoredDrives), func(root string) bool {
				return strings.EqualFold(root, drive.Root)
			})
			if !watched {
				d.cfg.IgnoredDrives = append(d.cfg.IgnoredDrives, drive.Root)
			}
			d.saveConfig()
		}
	}
	// The Run entry points at the running executable, which under go run
	// is a temporary file that disappears; only offer it in release builds.
	login := ui.Checkbox(c, &d.openAtLogin, "Windows へのサインイン時にトレイで起動する").Disabled(mygo.IsDev())
	if mygo.IsDev() {
		login.Tooltip("配布版でのみ設定できます")
	}
	if login.Changed() {
		if d.loginErr = mygo.App.SetOpenAtLogin(d.openAtLogin); d.loginErr != nil {
			d.openAtLogin = !d.openAtLogin
		}
	}
	for _, err := range []error{d.saveErr, d.loginErr} {
		if err != nil {
			ui.Text(c, "設定を保存できませんでした: "+err.Error()).Wrap().TextColor(warningColor)
		}
	}
}
