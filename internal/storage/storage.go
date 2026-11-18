// Package storage measures Windows drive capacity and holds the shared result types.
package storage

import (
	"fmt"
	"strings"
	"time"
)

// Error codes that Drive.ErrorCode can hold. They stay English and stable
// so that scripts can branch on them regardless of the Windows UI language.
const (
	ErrorNotReady     = "not_ready"
	ErrorAccessDenied = "access_denied"
	ErrorUnknown      = "unknown"
)

// Drive is the capacity visible to the current Windows user.
type Drive struct {
	Root          string `json:"root"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	FreeBytes     uint64 `json:"free_bytes"`
	ErrorCode     string `json:"error_code,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Snapshot records fixed drives and their individual read errors.
type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	MeasuredAt    time.Time `json:"measured_at"`
	Drives        []Drive   `json:"drives"`
}

// SchemaVersion is the current version of the Snapshot JSON.
const SchemaVersion = 1

// Measured reports whether the drive's capacity was read.
func (d Drive) Measured() bool {
	return d.ErrorCode == ""
}

// DisplayPath omits the trailing separator from a drive root.
func (d Drive) DisplayPath() string {
	return strings.TrimSuffix(d.Root, `\`)
}

// UsedRatio returns the used share of the drive between 0 and 1, or 0 when
// the capacity is unknown.
func (d Drive) UsedRatio() float64 {
	if d.CapacityBytes == 0 || d.FreeBytes > d.CapacityBytes {
		return 0
	}
	return float64(d.CapacityBytes-d.FreeBytes) / float64(d.CapacityBytes)
}

// GiB formats bytes for a capacity label.
func GiB(value uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(value)/(1<<30))
}

// Size formats bytes with the largest binary unit that keeps the value at
// or above 1, for sizes that range from bytes to gibibytes.
func Size(value uint64) string {
	switch {
	case value >= 1<<30:
		return GiB(value)
	case value >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(value)/(1<<20))
	case value >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(value)/(1<<10))
	default:
		return fmt.Sprintf("%d B", value)
	}
}
