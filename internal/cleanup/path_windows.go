package cleanup

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// longPath expands 8.3 short names, as %TEMP% often holds
// (C:\Users\LONGNA~1\...), so that the link check, which compares a folder
// with its resolved path, does not mistake a short name for a link.
func longPath(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return path
	}
	return windows.UTF16ToString(buf[:n])
}

// pathEntries lists the folders in PATH: Skilli's own and the user's and
// the machine's in the registry, which new shells start from.
func pathEntries() []string {
	values := []string{os.Getenv("PATH")}
	for _, key := range []struct {
		root registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Environment`},
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	} {
		k, err := registry.OpenKey(key.root, key.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if value, _, err := k.GetStringValue("Path"); err == nil {
			values = append(values, value)
		}
		k.Close()
	}
	var entries []string
	for _, value := range values {
		for entry := range strings.SplitSeq(value, ";") {
			if entry = strings.TrimSpace(entry); entry != "" {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}
