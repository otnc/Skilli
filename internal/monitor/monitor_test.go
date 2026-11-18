package monitor

import (
	"testing"
	"time"

	"github.com/otnc/Skilli/internal/config"
	"github.com/otnc/Skilli/internal/storage"
)

const gib = 1 << 30

var start = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

func snapshot(free uint64) storage.Snapshot {
	return storage.Snapshot{Drives: []storage.Drive{{Root: `C:\`, CapacityBytes: 500 * gib, FreeBytes: free}}}
}

func cfg() config.Config {
	return config.Config{LowSpaceBytes: 20 * gib, Notify: true}
}

func TestAlertsOnceWhenDriveBecomesLow(t *testing.T) {
	var m Monitor
	if alerts := m.Observe(snapshot(30*gib), cfg(), start); len(alerts) != 0 {
		t.Fatalf("enough space must not alert: %+v", alerts)
	}
	alerts := m.Observe(snapshot(19*gib), cfg(), start.Add(15*time.Minute))
	if len(alerts) != 1 || alerts[0].Root != `C:\` || alerts[0].FreeBytes != 19*gib || !m.Low(`C:\`) {
		t.Fatalf("alerts = %+v, low = %v", alerts, m.Low(`C:\`))
	}
	if alerts := m.Observe(snapshot(18*gib), cfg(), start.Add(30*time.Minute)); len(alerts) != 0 {
		t.Fatalf("a drive that stays low must not alert before the repeat interval: %+v", alerts)
	}
}

func TestRepeatsAlertAfterInterval(t *testing.T) {
	var m Monitor
	m.Observe(snapshot(10*gib), cfg(), start)
	if alerts := m.Observe(snapshot(10*gib), cfg(), start.Add(RepeatInterval-time.Second)); len(alerts) != 0 {
		t.Fatalf("alerted too early: %+v", alerts)
	}
	if alerts := m.Observe(snapshot(10*gib), cfg(), start.Add(RepeatInterval)); len(alerts) != 1 {
		t.Fatalf("want a repeated alert, got %+v", alerts)
	}
}

func TestResolvesOnlyAboveHysteresis(t *testing.T) {
	var m Monitor
	m.Observe(snapshot(19*gib), cfg(), start)
	m.Observe(snapshot(21*gib), cfg(), start.Add(time.Minute))
	if !m.Low(`C:\`) {
		t.Fatal("21 GiB is below 110% of 20 GiB; the drive must stay low")
	}
	if alerts := m.Observe(snapshot(19*gib), cfg(), start.Add(2*time.Minute)); len(alerts) != 0 {
		t.Fatalf("hovering around the threshold must not alert again: %+v", alerts)
	}
	m.Observe(snapshot(22*gib), cfg(), start.Add(3*time.Minute))
	if m.Low(`C:\`) || m.AnyLow() {
		t.Fatal("22 GiB reaches 110%; the drive must be resolved")
	}
	if alerts := m.Observe(snapshot(19*gib), cfg(), start.Add(4*time.Minute)); len(alerts) != 1 {
		t.Fatalf("a resolved drive that drops again must alert, got %+v", alerts)
	}
}

func TestUnreadableDriveKeepsState(t *testing.T) {
	var m Monitor
	m.Observe(snapshot(10*gib), cfg(), start)
	unreadable := storage.Snapshot{Drives: []storage.Drive{{Root: `C:\`, ErrorCode: storage.ErrorNotReady}}}
	if alerts := m.Observe(unreadable, cfg(), start.Add(RepeatInterval)); len(alerts) != 0 || !m.Low(`C:\`) {
		t.Fatalf("alerts = %+v, low = %v", alerts, m.Low(`C:\`))
	}
}

func TestNotifyOffTracksStateWithoutAlerts(t *testing.T) {
	var m Monitor
	quiet := cfg()
	quiet.Notify = false
	if alerts := m.Observe(snapshot(10*gib), quiet, start); len(alerts) != 0 || !m.Low(`C:\`) {
		t.Fatalf("alerts = %+v, low = %v", alerts, m.Low(`C:\`))
	}
	if alerts := m.Observe(snapshot(10*gib), cfg(), start.Add(time.Minute)); len(alerts) != 1 {
		t.Fatalf("turning notifications on for a low drive must alert, got %+v", alerts)
	}
}

func TestIgnoredDriveIsForgotten(t *testing.T) {
	var m Monitor
	m.Observe(snapshot(10*gib), cfg(), start)
	ignoring := cfg()
	ignoring.IgnoredDrives = []string{`c:\`}
	if alerts := m.Observe(snapshot(10*gib), ignoring, start.Add(RepeatInterval)); len(alerts) != 0 || m.AnyLow() {
		t.Fatalf("alerts = %+v, anyLow = %v", alerts, m.AnyLow())
	}
}
