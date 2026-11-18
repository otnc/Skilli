package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processRedirectionTrustPolicy is ProcessRedirectionTrustPolicy of the
// PROCESS_MITIGATION_POLICY enumeration.
const processRedirectionTrustPolicy = 16

var procGetProcessMitigationPolicy = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessMitigationPolicy")

// redirectionGuardEnforced reports whether this process runs under Windows'
// RedirectionGuard, which refuses to follow junctions that a standard user
// created. Skilli never turns it on, but inherits it when Inno Setup 6.7+
// starts it without elevation, as the installer's last page does. The
// guard then passes on to every tool Skilli runs, and shims that go
// through a junction, as vite-plus's npm does, fail with error 448.
func redirectionGuardEnforced() bool {
	if procGetProcessMitigationPolicy.Find() != nil {
		return false
	}
	var flags uint32
	r, _, _ := procGetProcessMitigationPolicy.Call(uintptr(windows.CurrentProcess()), processRedirectionTrustPolicy,
		uintptr(unsafe.Pointer(&flags)), unsafe.Sizeof(flags))
	return r != 0 && flags&1 != 0
}

// relaunchWindow keeps a relaunch from repeating: if the new instance
// still runs under the guard, it keeps running rather than relaunch again.
const relaunchWindow = 30 * time.Second

// relaunchThroughShell starts this executable again through the running
// Explorer, which starts it as its own child, without the guard. A process
// cannot turn the guard off for itself once it is on.
func relaunchThroughShell() error {
	dir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	marker := filepath.Join(dir, "Skilli", "relaunched")
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < relaunchWindow {
		return errors.New("relaunched moments ago and still guarded")
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	explorer := filepath.Join(os.Getenv("SystemRoot"), "explorer.exe")
	return exec.Command(explorer, exe).Start()
}
