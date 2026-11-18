package cleanup

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Size is the logical size of the files under a folder.
type Size struct {
	Bytes uint64 `json:"bytes"`
	Files int    `json:"files"`
	// Partial is set when some entries could not be read or the
	// measurement ran out of time, so Bytes is a lower bound.
	Partial bool `json:"partial,omitzero"`
}

// Measure adds up the files under dir until ctx ends. It does not follow
// symbolic links, junctions or mount points, so a link to another drive is
// not counted and cannot make the walk loop.
func Measure(ctx context.Context, dir string) (Size, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return Size{}, err
	}
	if !info.IsDir() || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return Size{}, fmt.Errorf("%w: %s is not a plain folder", ErrUnsafeFolder, dir)
	}
	var size Size
	err = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			size.Partial = true
			return filepath.SkipAll
		}
		if err != nil {
			size.Partial = true
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Links and mount points are neither counted nor entered; WalkDir
		// already does not descend into them because they are not IsDir.
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			size.Partial = true
			return nil
		}
		size.Bytes += uint64(info.Size())
		size.Files++
		return nil
	})
	return size, err
}
