package cleanup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsafeFolder reports a folder that Skilli refuses to clean.
var ErrUnsafeFolder = errors.New("unsafe folder")

// CheckCleanable refuses a folder that is not absolute, is a volume root,
// or equals or contains one of the protected folders. A tool whose cache
// setting points at, say, the user profile would otherwise be asked to
// clean the whole profile.
func CheckCleanable(dir string, protected []string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrUnsafeFolder, dir)
	}
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == dir {
		return fmt.Errorf("%w: %s is a volume root", ErrUnsafeFolder, dir)
	}
	for _, p := range protected {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if strings.EqualFold(dir, p) || isWithin(p, dir) {
			return fmt.Errorf("%w: %s contains the protected folder %s", ErrUnsafeFolder, dir, p)
		}
	}
	return nil
}

// CheckOnDisk refuses a folder that does not exist, is not a directory,
// or is reached through a symbolic link, junction or mount point on any
// part of its path. Such a link could make the tool clean somewhere other
// than the folder the user saw.
func CheckOnDisk(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return fmt.Errorf("%w: %s is a link or mount point", ErrUnsafeFolder, dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a folder", ErrUnsafeFolder, dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(real), filepath.Clean(dir)) {
		return fmt.Errorf("%w: %s resolves to %s", ErrUnsafeFolder, dir, real)
	}
	return nil
}

// IsAppExecutionAlias reports whether exe is in the WindowsApps folder of
// localAppData. Windows puts app execution aliases there, such as the
// python.exe that opens the Microsoft Store when Python is not installed,
// so a tool found there is treated as not installed.
func IsAppExecutionAlias(exe, localAppData string) bool {
	if localAppData == "" {
		return false
	}
	return isWithin(exe, filepath.Join(localAppData, "Microsoft", "WindowsApps"))
}

// isWithin reports whether path is strictly inside dir.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// samePath compares Windows paths case-insensitively.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
