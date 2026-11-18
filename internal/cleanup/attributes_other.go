//go:build !windows

package cleanup

import "io/fs"

// protectedAttributes keeps read-only files elsewhere than Windows, which
// has no hidden or system attribute.
func protectedAttributes(info fs.FileInfo) bool {
	return info.Mode().Perm()&0o200 == 0
}
