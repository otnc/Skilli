//go:build !windows

package cleanup

import (
	"os"
	"path/filepath"
)

func longPath(path string) string { return path }

func pathEntries() []string { return filepath.SplitList(os.Getenv("PATH")) }
