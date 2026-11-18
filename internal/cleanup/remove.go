package cleanup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// RemoveContents deletes everything inside dir but keeps dir itself, and
// returns how many entries it left in place.
//
// It never follows a symbolic link, junction or mount point: such an entry
// is counted as skipped and left as it is, together with what it points
// to. A file that cannot be removed, typically one in use, is skipped too,
// and so is the folder that still holds it. The walk stops when ctx ends.
//
//	skipped, err := cleanup.RemoveContents(ctx, `C:\Users\me\.bun\install\cache`)
func RemoveContents(ctx context.Context, dir string) (skipped int, err error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return 0, err
	}
	if !isPlainDir(info) {
		return 0, &fs.PathError{Op: "remove contents", Path: dir, Err: ErrUnsafeFolder}
	}
	return removeChildren(ctx, dir)
}

func removeChildren(ctx context.Context, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 1, nil
	}
	skipped := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return skipped, err
		}
		path := filepath.Join(dir, entry.Name())
		// Lstat again rather than trust the directory listing, so that an
		// entry replaced by a link since the listing is still not followed.
		info, err := os.Lstat(path)
		switch {
		case err != nil:
			skipped++
		case info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0:
			skipped++
		case info.IsDir():
			n, err := removeChildren(ctx, path)
			skipped += n
			if err != nil {
				return skipped, err
			}
			if n == 0 && os.Remove(path) != nil {
				skipped++
			}
		default:
			if removeFile(path, info) != nil {
				skipped++
			}
		}
	}
	return skipped, nil
}

// removeFile deletes a regular file, clearing the read-only attribute
// first when Windows refuses because of it, as for Go's module cache.
func removeFile(path string, info fs.FileInfo) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) || info.Mode().Perm()&0o200 != 0 {
		return err
	}
	if chmodErr := os.Chmod(path, info.Mode().Perm()|0o200); chmodErr != nil {
		return err
	}
	return os.Remove(path)
}

func isPlainDir(info fs.FileInfo) bool {
	return info.IsDir() && info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0
}
