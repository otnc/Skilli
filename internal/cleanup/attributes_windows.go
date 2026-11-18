package cleanup

import (
	"io/fs"
	"syscall"
)

// protectedAttributes reports read-only, hidden or system files, which
// Windows' Disk Cleanup leaves in the temporary folder.
func protectedAttributes(info fs.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	const protected = syscall.FILE_ATTRIBUTE_READONLY | syscall.FILE_ATTRIBUTE_HIDDEN | syscall.FILE_ATTRIBUTE_SYSTEM
	return data.FileAttributes&protected != 0
}
