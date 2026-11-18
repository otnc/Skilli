// Package monitor decides when a drive is low on free space and when to
// notify about it. It keeps no I/O so that the rules can be tested alone.
package monitor

import (
	"time"

	"github.com/otnc/Skilli/internal/config"
	"github.com/otnc/Skilli/internal/storage"
)

// RepeatInterval is how often a drive that stays low is notified again.
const RepeatInterval = 6 * time.Hour

// Alert asks to notify that a drive is low.
type Alert struct {
	Root           string
	FreeBytes      uint64
	ThresholdBytes uint64
}

// Monitor remembers which drives are low between observations.
// The zero value is ready to use. It is not safe for concurrent use.
type Monitor struct {
	drives map[string]*driveState
}

type driveState struct {
	low          bool
	lastNotified time.Time
}

// Observe updates the low state of each drive in snapshot and returns the
// alerts to show now.
//
// A drive becomes low when its free space drops below cfg.LowSpaceBytes,
// and stops being low only once it rises to 110% of it, so that a drive
// hovering at the threshold does not alert again and again. While a drive
// stays low, it alerts again after RepeatInterval. With cfg.Notify off the
// state still updates but no alerts are returned.
//
//	alerts := m.Observe(snapshot, cfg, time.Now())
//	for _, a := range alerts { notify(a) }
func (m *Monitor) Observe(snapshot storage.Snapshot, cfg config.Config, now time.Time) []Alert {
	if m.drives == nil {
		m.drives = map[string]*driveState{}
	}
	var alerts []Alert
	for _, drive := range snapshot.Drives {
		if cfg.Ignores(drive.Root) {
			delete(m.drives, drive.Root)
			continue
		}
		// An unreadable drive keeps its last state: a USB enclosure that
		// sleeps should not resolve a low alert or raise a new one.
		if !drive.Measured() {
			continue
		}
		state := m.drives[drive.Root]
		if state == nil {
			state = &driveState{}
			m.drives[drive.Root] = state
		}
		switch {
		case drive.FreeBytes < cfg.LowSpaceBytes:
			due := !state.low || now.Sub(state.lastNotified) >= RepeatInterval
			state.low = true
			if due && cfg.Notify {
				state.lastNotified = now
				alerts = append(alerts, Alert{Root: drive.Root, FreeBytes: drive.FreeBytes, ThresholdBytes: cfg.LowSpaceBytes})
			}
		case drive.FreeBytes >= cfg.LowSpaceBytes/10*11:
			state.low = false
			state.lastNotified = time.Time{}
		}
	}
	return alerts
}

// Low reports whether the drive with the given root is currently low.
func (m *Monitor) Low(root string) bool {
	state := m.drives[root]
	return state != nil && state.low
}

// AnyLow reports whether any monitored drive is currently low.
func (m *Monitor) AnyLow() bool {
	for _, state := range m.drives {
		if state.low {
			return true
		}
	}
	return false
}
