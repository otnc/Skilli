package desktop

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/config"
	"github.com/otnc/Skilli/internal/storage"
)

const gib = 1 << 30

func sampleDashboard() *dashboard {
	d := &dashboard{version: "0.2.0", cfg: config.Default(), notify: true, thresholdGiB: 20}
	d.snapshot = storage.Snapshot{
		MeasuredAt: time.Date(2026, 11, 18, 14, 2, 0, 0, time.Local),
		Drives: []storage.Drive{
			{Root: `C:\`, CapacityBytes: 234 * gib, FreeBytes: 12 * gib},
			{Root: `D:\`, CapacityBytes: 931 * gib, FreeBytes: 556 * gib},
		},
	}
	d.monitor.Observe(d.snapshot, d.cfg, d.snapshot.MeasuredAt)
	d.caches.engine = &cleanup.Engine{}
	d.caches.scannedAt = d.snapshot.MeasuredAt
	d.caches.scans = []cleanup.Scan{
		{ID: "npm", Name: "npm キャッシュ", Location: cleanup.Location{Status: cleanup.Found, Dir: `C:\Users\me\AppData\Local\npm-cache`}, Size: &cleanup.Size{Bytes: 5 * gib}},
		{ID: "bun", Name: "Bun キャッシュ", Location: cleanup.Location{Status: cleanup.Found, Dir: `C:\Users\me\.bun\install\cache`}, Size: &cleanup.Size{Bytes: 2 * gib}},
		{ID: "go-build", Name: "Go ビルドキャッシュ", Location: cleanup.Location{Status: cleanup.Found, Dir: `D:\go\cache`}, Size: &cleanup.Size{Bytes: 6 * gib}},
		{ID: "temp", Name: "ユーザーの一時フォルダ", Location: cleanup.Location{Status: cleanup.Found, Dir: `C:\Users\me\AppData\Local\Temp`}, Size: &cleanup.Size{Bytes: 7 * gib}},
		{ID: "conda", Name: "conda パッケージキャッシュ", Location: cleanup.Location{Status: cleanup.Missing}},
		{ID: "deno", Name: "Deno キャッシュ", Location: cleanup.Location{Status: cleanup.Missing}},
		{ID: "composer", Name: "Composer キャッシュ", Location: cleanup.Location{Status: cleanup.Failed, Error: "exit status 1"}},
	}
	return d
}

// saveSnapshot writes the tester's frame to SKILLI_SNAPSHOT_DIR when that is
// set, to look at the layout without opening a window.
func saveSnapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("SKILLI_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestDrivesPageSummarizesLowSpace(t *testing.T) {
	d := sampleDashboard()
	tt := ui.NewTester(d.render, 600, 720)
	saveSnapshot(t, tt, "drives")
	for _, want := range []string{"C: の空き容量が少なくなっています", "空き 12.0 GiB", "空きが少ない", "空き 556.0 GiB"} {
		if !tt.HasText(want) {
			t.Errorf("drives page lacks %q; texts = %q", want, tt.Texts())
		}
	}
}

func TestCachesPageListsFoundLargestFirstAndFoldsTheRest(t *testing.T) {
	d := sampleDashboard()
	d.tab = tabCaches
	tt := ui.NewTester(d.render, 600, 720)
	saveSnapshot(t, tt, "caches")

	// 5 + 2 + 6 + 7 GiB of found caches.
	if !tt.HasText("20.0 GiB") {
		t.Errorf("total missing; texts = %q", tt.Texts())
	}
	tempBox, _ := tt.Find("ユーザーの一時フォルダ")
	bunBox, _ := tt.Find("Bun キャッシュ")
	if tempBox.Y >= bunBox.Y {
		t.Errorf("the largest cache must come first: temp at %v, bun at %v", tempBox, bunBox)
	}
	if tt.HasText("Deno キャッシュ") {
		t.Error("caches that were not found must stay folded")
	}
	tt.SetSize(600, 1100)
	if err := tt.Click("見つからなかった対象 (2)"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	saveSnapshot(t, tt, "caches-missing-open")
	if !d.caches.showMissing {
		t.Error("clicking the folded list must open it")
	}
}

func TestSettingsPageGroupsSettings(t *testing.T) {
	d := sampleDashboard()
	d.tab = tabSettings
	tt := ui.NewTester(d.render, 600, 720)
	saveSnapshot(t, tt, "settings")
	for _, want := range []string{"通知", "監視するドライブ", "起動", "アプリ", "Skilli 0.2.0"} {
		if !tt.HasText(want) {
			t.Errorf("settings page lacks %q; texts = %q", want, tt.Texts())
		}
	}
}

func TestDarkThemeRenders(t *testing.T) {
	d := sampleDashboard()
	d.tab = tabCaches
	tt := ui.NewTesterWith(d.render, 600, 720, ui.TesterOptions{Dark: true})
	saveSnapshot(t, tt, "caches-dark")
	if !tt.HasText("20.0 GiB") {
		t.Errorf("texts = %q", tt.Texts())
	}
}
