package desktop

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/config"
	"github.com/otnc/Skilli/internal/storage"
)

// Pages of the window, in the order of the switch at the top.
const (
	tabDrives = iota
	tabCaches
	tabSettings
)

// The layout keeps one decision per screen: a fixed header says whether
// anything needs attention, and each page shows one list of cards with at
// most two actions per card. Secondary information is muted, and lists of
// targets that need no action are folded away.

func (d *dashboard) render(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		d.renderHeader(c)
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).PaddingX(24).PaddingY(16).Gap(12).Children(func() {
				switch d.tab {
				case tabDrives:
					d.renderDrives(c)
				case tabCaches:
					d.renderCaches(c)
				default:
					d.renderSettings(c)
				}
			})
		})
	})
}

func (d *dashboard) renderHeader(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).PaddingX(24).PaddingY(16).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "Skilli").FontSize(22).Bold()
				status, attention := d.driveStatus()
				text := ui.Text(c, status).Wrap()
				if attention {
					text.TextColor(t.Warning)
				} else {
					text.TextColor(t.TextMuted)
				}
			})
			if ui.Button(c, "更新").Disabled(d.reading).Tooltip("空き容量を計測し直します").Clicked() {
				d.refresh()
			}
		})
		ui.Segmented(c, &d.tab, "ドライブ", "キャッシュ", "設定").FillEqually().Label("表示する画面")
		if d.trayErr != nil {
			ui.Text(c, "タスクトレイにアイコンを表示できませんでした。").TextColor(t.Warning)
		}
	})
}

// driveStatus sums up the drives in one sentence, and reports whether it
// asks for attention.
func (d *dashboard) driveStatus() (string, bool) {
	switch {
	case d.readErr != nil:
		return "ドライブを確認できませんでした", true
	case d.reading && len(d.snapshot.Drives) == 0:
		return "ドライブを確認しています…", false
	}
	var low []string
	for _, drive := range d.snapshot.Drives {
		if d.monitor.Low(drive.Root) {
			low = append(low, drive.DisplayPath())
		}
	}
	if len(low) > 0 {
		return strings.Join(low, "、") + " の空き容量が少なくなっています", true
	}
	return "すべてのドライブに十分な空きがあります", false
}

// card is the container of one item: a drive, a cache or a group of
// settings.
func card(c *ui.Context, fn func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).Padding(16).Gap(8).Radius(10).Background(t.Surface).Border(1, t.Border).Children(fn)
}

func muted(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).TextColor(c.Theme().TextMuted)
}

func (d *dashboard) renderDrives(c *ui.Context) {
	t := c.Theme()
	if d.readErr != nil {
		muted(c, d.readErr.Error()).Wrap()
	}
	if len(d.snapshot.Drives) == 0 && !d.reading && d.readErr == nil {
		muted(c, "固定ドライブが見つかりませんでした。")
	}
	for _, drive := range d.snapshot.Drives {
		card(c, func() {
			ignored := d.cfg.Ignores(drive.Root)
			low := d.monitor.Low(drive.Root)
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, drive.DisplayPath()).FontSize(16).Bold()
				ui.Spacer(c)
				if drive.Measured() {
					free := ui.Text(c, "空き "+storage.GiB(drive.FreeBytes)).FontSize(16).Bold()
					if low {
						free.TextColor(t.Warning)
					}
				}
			})
			if !drive.Measured() {
				muted(c, "このドライブは計測できませんでした ("+drive.ErrorCode+")")
				return
			}
			capacity := float64(drive.CapacityBytes)
			threshold := float64(d.cfg.LowSpaceBytes)
			ui.Meter(c, capacity-float64(drive.FreeBytes), 0, capacity,
				&ui.MeterLevels{Warning: max(capacity-threshold*1.5, 0), Critical: max(capacity-threshold, 0)}).
				FillWidth().Label(drive.DisplayPath() + " の使用量")
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				muted(c, fmt.Sprintf("%s 中 %.0f%% 使用", storage.GiB(drive.CapacityBytes), drive.UsedRatio()*100))
				ui.Spacer(c)
				switch {
				case ignored:
					muted(c, "監視していません")
				case low:
					ui.Badge(c, "空きが少ない").Background(t.Warning).TextColor(t.AccentText)
				}
			})
		})
	}
	if !d.snapshot.MeasuredAt.IsZero() {
		muted(c, "最終確認 "+d.snapshot.MeasuredAt.Format("15:04")+"  ・  常駐中は 15 分ごとに確認します")
	}
}

func (d *dashboard) renderCaches(c *ui.Context) {
	v := &d.caches
	if v.engineErr != nil {
		muted(c, "キャッシュを計測できません: "+v.engineErr.Error()).Wrap()
		return
	}
	found := v.found()
	var total uint64
	for _, scan := range found {
		total += scan.Size.Bytes
	}

	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			switch {
			case v.scanning:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Label("計測中")
					ui.Text(c, fmt.Sprintf("計測しています  %d / %d", v.scanned, v.total)).FontSize(16).Bold()
				})
			case v.scannedAt.IsZero():
				ui.Text(c, "開発ツールのキャッシュ").FontSize(16).Bold()
			default:
				ui.Text(c, storage.Size(total)).FontSize(22).Bold()
				muted(c, fmt.Sprintf("%d 件のキャッシュ  ・  %s に計測", len(found), v.scannedAt.Format("15:04")))
			}
		})
		label := "計測"
		if !v.scannedAt.IsZero() {
			label = "計測し直す"
		}
		var button ui.Element
		if v.scannedAt.IsZero() && !v.scanning {
			button = ui.PrimaryButton(c, label)
		} else {
			button = ui.Button(c, label)
		}
		if button.Disabled(d.busy()).Clicked() {
			d.scanCaches()
		}
	})

	if v.scannedAt.IsZero() && !v.scanning {
		muted(c, "「計測」を押すと、uv や npm、Go などのキャッシュの場所とサイズを調べます。調べるだけでは何も削除しません。").Wrap()
		return
	}

	for _, scan := range found {
		d.renderCacheCard(c, scan)
	}

	if failed := v.withStatus(cleanup.Failed); len(failed) > 0 {
		ui.Collapsible(c, fmt.Sprintf("計測できなかった対象 (%d)", len(failed)), &v.showFailed, func() {
			ui.Column(c).Gap(8).PaddingY(4).Children(func() {
				for _, scan := range failed {
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, scan.Name).Bold()
						muted(c, scan.Error).Wrap()
					})
				}
			})
		})
	}
	if missing := v.withStatus(cleanup.Missing); len(missing) > 0 {
		names := make([]string, len(missing))
		for i, scan := range missing {
			names[i] = scan.Name
		}
		ui.Collapsible(c, fmt.Sprintf("見つからなかった対象 (%d)", len(missing)), &v.showMissing, func() {
			muted(c, "インストールされていないか、キャッシュのフォルダがまだありません。\n"+strings.Join(names, "、")).Wrap()
		})
	}
	if !v.scanning {
		muted(c, "各ツールの公式コマンドがあるものはそれで削除し、ないものだけ Skilli が中身を直接削除します。削除の前に必ず確認します。").Wrap()
	}
}

func (d *dashboard) renderCacheCard(c *ui.Context, scan cleanup.Scan) {
	target, _ := cleanup.FindTarget(scan.ID)
	card(c, func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, scan.Name).Bold().Grow(1).Ellipsis("…")
			size := storage.Size(scan.Size.Bytes)
			if scan.Size.Partial {
				size += " 以上"
			}
			ui.Text(c, size).Bold()
		})
		muted(c, scan.Dir).SingleLine().TruncateMiddle().Tooltip(scan.Dir)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "フォルダを開く").Clicked() {
				_ = mygo.Shell.OpenPath(scan.Dir)
			}
			ui.Spacer(c)
			switch {
			case target.SettingsURI != "":
				if ui.Button(c, "Windows の設定で削除…").Tooltip(target.Note).Clicked() {
					_ = mygo.Shell.OpenExternal(target.SettingsURI)
				}
			case target.Cleanable():
				method := "公式コマンド"
				if target.Delete != nil {
					method = "直接削除"
				}
				muted(c, method).Tooltip(target.Note)
				label := "削除…"
				if d.caches.cleaning == scan.ID {
					label = "削除しています…"
				}
				if ui.Button(c, label).Disabled(d.busy()).Tooltip(target.Note).Clicked() {
					d.cleanTarget(target)
				}
			default:
				muted(c, "Skilli からは削除しません").Tooltip(target.Note)
			}
		})
	})
}

func (d *dashboard) renderSettings(c *ui.Context) {
	t := c.Theme()
	if d.configErr != nil {
		ui.Text(c, "設定ファイルを読み込めなかったため、既定値を使っています。変更すると上書きします。\n"+d.configErr.Error()).Wrap().TextColor(t.Warning)
	}

	sectionTitle(c, "通知")
	card(c, func() {
		if ui.Checkbox(c, &d.notify, "空き容量が少なくなったら通知する").Changed() {
			d.cfg.Notify = d.notify
			d.saveConfig()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "空きが")
			if ui.NumberInput(c, &d.thresholdGiB, float64(config.MinLowSpaceBytes>>30), float64(config.MaxLowSpaceBytes>>30), 1).
				Width(110).Label("通知する空き容量 (GiB)").Changed() {
				d.cfg.LowSpaceBytes = uint64(d.thresholdGiB) << 30
				d.saveConfig()
			}
			ui.Text(c, "GiB を下回ったら通知")
		})
		muted(c, "下回ったままのときは 6 時間おきに通知し、基準の 110% まで空きが戻ると解除します。").Wrap()
	})

	if len(d.snapshot.Drives) > 0 {
		sectionTitle(c, "監視するドライブ")
		card(c, func() {
			for _, drive := range d.snapshot.Drives {
				watched := !d.cfg.Ignores(drive.Root)
				if ui.Checkbox(c, &watched, drive.DisplayPath()).Changed() {
					d.cfg.IgnoredDrives = slices.DeleteFunc(slices.Clone(d.cfg.IgnoredDrives), func(root string) bool {
						return strings.EqualFold(root, drive.Root)
					})
					if !watched {
						d.cfg.IgnoredDrives = append(d.cfg.IgnoredDrives, drive.Root)
					}
					d.saveConfig()
				}
			}
		})
	}

	sectionTitle(c, "起動")
	card(c, func() {
		// The Run entry points at the running executable, which under go
		// run is a temporary file that disappears; only offer it in
		// release builds.
		login := ui.Checkbox(c, &d.openAtLogin, "Windows へのサインイン時にトレイで起動する").Disabled(mygo.IsDev())
		if mygo.IsDev() {
			login.Tooltip("配布版でのみ設定できます")
		}
		if login.Changed() {
			if d.loginErr = mygo.App.SetOpenAtLogin(d.openAtLogin); d.loginErr != nil {
				d.openAtLogin = !d.openAtLogin
			}
		}
	})

	sectionTitle(c, "アプリ")
	card(c, func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "Skilli "+d.version)
				switch {
				case d.installing:
					muted(c, "更新しています…")
				case d.updating:
					muted(c, "更新を確認しています…")
				}
			})
			if ui.Button(c, "更新を確認").Disabled(d.updating).Clicked() {
				d.checkUpdates()
			}
		})
	})

	for _, err := range []error{d.saveErr, d.loginErr} {
		if err != nil {
			ui.Text(c, "設定を保存できませんでした: "+err.Error()).Wrap().TextColor(t.Warning)
		}
	}
}

func sectionTitle(c *ui.Context, text string) {
	muted(c, text).FontSize(13).Bold().MarginStart(4)
}
