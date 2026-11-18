package cleanup

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/otnc/Skilli/internal/storage"
)

// Size is the logical size of the files under a folder.
type Size struct {
	Bytes uint64 `json:"bytes"`
	Files int    `json:"files"`
	// Links counts symbolic links and junctions, which take almost no
	// space but can pile up, as fnm's per-shell links do.
	Links int `json:"links,omitzero"`
	// Partial is set when some entries could not be read or the
	// measurement ran out of time, so Bytes is a lower bound.
	Partial bool `json:"partial,omitzero"`
}

// String formats the size for people, showing the count of links when a
// folder holds nothing else, as fnm's per-shell links.
func (s Size) String() string {
	if s.Bytes == 0 && s.Links > 0 {
		return fmt.Sprintf("リンク %d 個", s.Links)
	}
	return storage.Size(s.Bytes)
}

func (s *Size) add(other Size) {
	s.Bytes += other.Bytes
	s.Files += other.Files
	s.Links += other.Links
	s.Partial = s.Partial || other.Partial
}

// Measure adds up the files under dir until ctx ends. It does not follow
// symbolic links, junctions or mount points, so a link to another drive is
// not counted and cannot make the walk loop; it counts them in Links.
func Measure(ctx context.Context, dir string) (Size, error) {
	return MeasureSelected(ctx, dir, RemoveOptions{})
}

// MeasureSelected counts only what RemoveContents would remove with opts.
func MeasureSelected(ctx context.Context, dir string, opts RemoveOptions) (Size, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return Size{}, err
	}
	if !isPlainDir(info) {
		return Size{}, fmt.Errorf("%w: %s is not a plain folder", ErrUnsafeFolder, dir)
	}
	var size Size
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
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
		if path == dir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			size.Partial = true
			return nil
		}
		switch {
		case isLink(info):
			// Links are neither entered nor, except with LinksOnly at the
			// top level, removed.
			if !opts.LinksOnly || filepath.Dir(path) == dir && opts.Selected(path, info) {
				size.Links++
			}
		case opts.LinksOnly:
			if entry.IsDir() {
				return filepath.SkipDir
			}
		case entry.Type().IsRegular() && opts.Selected(path, info):
			size.Bytes += uint64(info.Size())
			size.Files++
		}
		return nil
	})
	return size, err
}
