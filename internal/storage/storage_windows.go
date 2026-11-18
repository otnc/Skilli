package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// Read measures the drive letters that Windows identifies as local fixed drives.
func Read() (Snapshot, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return Snapshot{}, fmt.Errorf("GetLogicalDrives: %w", err)
	}
	result := Snapshot{
		SchemaVersion: SchemaVersion,
		Drives:        []Drive{},
	}
	for i := range 26 {
		if mask&(1<<i) == 0 {
			continue
		}
		drive := Drive{Root: fmt.Sprintf("%c:\\", 'A'+i)}
		root, err := windows.UTF16PtrFromString(drive.Root)
		if err != nil {
			return Snapshot{}, fmt.Errorf("drive root %q: %w", drive.Root, err)
		}
		if windows.GetDriveType(root) != windows.DRIVE_FIXED {
			continue
		}
		if err := windows.GetDiskFreeSpaceEx(root, &drive.FreeBytes, &drive.CapacityBytes, nil); err != nil {
			drive.ErrorCode, drive.Error = errorCode(err), err.Error()
		}
		result.Drives = append(result.Drives, drive)
	}
	result.MeasuredAt = time.Now()
	return result, nil
}

// FreeBytes returns the free space of the volume that holds path.
func FreeBytes(path string) (uint64, error) {
	root := filepath.VolumeName(path) + `\`
	ptr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, windows.ERROR_NOT_READY):
		return ErrorNotReady
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return ErrorAccessDenied
	default:
		return ErrorUnknown
	}
}
