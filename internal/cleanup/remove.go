package cleanup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RemoveOptions narrows what RemoveContents removes.
type RemoveOptions struct {
	// Before keeps files and folders modified at or after it; zero removes
	// regardless of age.
	Before time.Time
	// KeepProtected keeps read-only, hidden and system files, as Windows'
	// Disk Cleanup does in the temporary folder.
	KeepProtected bool
	// LinksOnly removes only the links directly inside the folder, never
	// what they point to, as for fnm's per-shell links.
	LinksOnly bool
	// Keep lists paths, compared without case, that stay even when they
	// match, such as links that PATH still names.
	Keep []string
}

// RemoveStats counts what RemoveContents left in place.
type RemoveStats struct {
	// Skipped is what could not be removed, mostly files in use, plus the
	// links that a full removal never follows.
	Skipped int
	// Kept is what the options excluded, such as recent files.
	Kept int
}

// RemoveContents deletes what is inside dir but keeps dir itself.
//
// It never follows a symbolic link, junction or mount point: a full
// removal leaves such an entry and what it points to in place, and with
// LinksOnly it removes the link itself and nothing else. A file that cannot
// be removed, typically one in use, is skipped, and so is the folder that
// still holds it. The walk stops when ctx ends.
//
//	stats, err := cleanup.RemoveContents(ctx, `C:\Users\me\.bun\install\cache`, cleanup.RemoveOptions{})
func RemoveContents(ctx context.Context, dir string, opts RemoveOptions) (RemoveStats, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return RemoveStats{}, err
	}
	if !isPlainDir(info) {
		return RemoveStats{}, &fs.PathError{Op: "remove contents", Path: dir, Err: ErrUnsafeFolder}
	}
	var stats RemoveStats
	if opts.LinksOnly {
		err = removeLinks(ctx, dir, opts, &stats)
	} else {
		_, err = removeChildren(ctx, dir, opts, &stats)
	}
	return stats, err
}

// Selected reports whether an entry passes the age, attribute and Keep
// filters; Measure uses it to size the same files a removal would take.
func (o RemoveOptions) Selected(path string, info fs.FileInfo) bool {
	if !o.Before.IsZero() && !info.ModTime().Before(o.Before) {
		return false
	}
	if o.KeepProtected && protectedAttributes(info) {
		return false
	}
	for _, keep := range o.Keep {
		if strings.EqualFold(filepath.Clean(keep), filepath.Clean(path)) {
			return false
		}
	}
	return true
}

func removeLinks(ctx context.Context, dir string, opts RemoveOptions, stats *RemoveStats) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		switch {
		case err != nil:
			stats.Skipped++
		case !isLink(info) || !opts.Selected(path, info):
			stats.Kept++
		// os.Remove deletes a junction or symbolic link itself, through
		// RemoveDirectory or DeleteFile, and never what it points to.
		case os.Remove(path) != nil:
			stats.Skipped++
		}
	}
	return nil
}

// removeChildren empties dir and reports whether it is now empty.
func removeChildren(ctx context.Context, dir string, opts RemoveOptions, stats *RemoveStats) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		stats.Skipped++
		return false, nil
	}
	empty := true
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		path := filepath.Join(dir, entry.Name())
		// Lstat again rather than trust the directory listing, so that an
		// entry replaced by a link since the listing is still not followed.
		info, err := os.Lstat(path)
		switch {
		case err != nil:
			stats.Skipped++
			empty = false
		case isLink(info):
			stats.Skipped++
			empty = false
		case info.IsDir():
			childEmpty, err := removeChildren(ctx, path, opts, stats)
			if err != nil {
				return false, err
			}
			// Removing the children updated the folder's own time, so the
			// age check uses info from before them.
			if !childEmpty || !opts.Selected(path, info) {
				empty = false
				continue
			}
			if os.Remove(path) != nil {
				stats.Skipped++
				empty = false
			}
		case !opts.Selected(path, info):
			stats.Kept++
			empty = false
		default:
			if removeFile(path, info) != nil {
				stats.Skipped++
				empty = false
			}
		}
	}
	return empty, nil
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

func isLink(info fs.FileInfo) bool {
	return info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

func isPlainDir(info fs.FileInfo) bool {
	return info.IsDir() && !isLink(info)
}
