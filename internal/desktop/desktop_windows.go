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

	version string
	// tab is the page shown: tabDrives, tabCaches or tabSettings.
	tab int
	// form is the custom target being added or edited, or nil.
	form *customForm
}

// Run starts the MyGo main loop. Call it from the main goroutine.
func Run(version string, background, checkUpdates bool) error {
	mygo.App.SetName("Skilli")
	mygo.App.SetVersion(version)
	// Hand over to a clean instance before taking the single-instance
	// lock, which the new instance needs.
	if redirectionGuardEnforced() && relaunchThroughShell() == nil {
		return nil
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}

	view := &dashboard{version: version}
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
			{Label: "キャッシュを計測", Click: func(*mygo.MenuItem, *mygo.Window) { d.tab = tabCaches; d.show(); d.scanCaches() }},
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
		Width:   600,
		Height:  720,
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
