package cleanup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/otnc/Skilli/internal/storage"
)

// New returns an Engine that runs real commands and writes the history
// under %LOCALAPPDATA%\Skilli.
func New() (*Engine, error) {
	historyPath, err := HistoryPath()
	if err != nil {
		return nil, err
	}
	return &Engine{
		Runner:    execRunner{},
		Protected: protectedFolders,
		FreeBytes: storage.FreeBytes,
		Lock:      lockCleanup,
		History:   &History{Path: historyPath},
		Getenv:    os.Getenv,
		Home:      os.UserHomeDir,
		Running:   runningExecutables,
		Now:       time.Now,
	}, nil
}

// runningExecutables returns which of names, compared without case, are
// executable names of running processes.
func runningExecutables(names []string) ([]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var running []string
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		for _, name := range names {
			if strings.EqualFold(exe, name) && !slices.Contains(running, name) {
				running = append(running, name)
			}
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return running, nil
}

type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) {
	// exec.LookPath already refuses a match in the current directory
	// (exec.ErrDot), which would let a planted uv.exe run instead.
	exe, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return "", err
	}
	if IsAppExecutionAlias(exe, os.Getenv("LOCALAPPDATA")) {
		return "", fmt.Errorf("%s: %w", exe, exec.ErrNotFound)
	}
	return exe, nil
}

func (execRunner) Output(ctx context.Context, dir, exe string, args []string) (string, error) {
	cmd := command(ctx, dir, exe, args)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", withOutput(err, stderr.String())
	}
	return string(out), nil
}

func (execRunner) Run(ctx context.Context, dir, exe string, args []string) (string, error) {
	out, err := command(ctx, dir, exe, args).CombinedOutput()
	if err != nil {
		return string(out), withOutput(err, string(out))
	}
	return string(out), nil
}

func command(ctx context.Context, dir, exe string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, args...)
	// Run in dir, or the home folder, rather than wherever the CLI was
	// started. In a project folder, go.mod could make go download a
	// toolchain, and mise, uv or Poetry could read the project's settings
	// and report another cache, so the GUI and the CLI would disagree.
	cmd.Dir = dir
	if home, err := os.UserHomeDir(); err == nil && dir == "" {
		cmd.Dir = home
	}
	cmd.Env = append(os.Environ(),
		// Plain output keeps the lines that Target.Parse reads free of
		// color codes and of the .NET SDK's first-run banner.
		"NO_COLOR=1", "DOTNET_NOLOGO=1",
		// Asking a tool where its cache is must not install anything:
		// without these, a mise shim installs a missing version, Corepack
		// downloads the pinned pnpm or Yarn without asking when stdin is
		// not a terminal, and go may download the toolchain GOTOOLCHAIN
		// names. The installed versions report the same cache folders.
		"MISE_AUTO_INSTALL=false", "COREPACK_ENABLE_NETWORK=0", "GOTOOLCHAIN=local",
		// uv waits 300 s by default for a cache lock that a uv process
		// started outside Skilli holds. Target.Busy catches most of those
		// beforehand; give up quickly on the rest.
		"UV_LOCK_TIMEOUT=10",
	)
	// The GUI build has no console, so each console tool would otherwise
	// flash a window of its own.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	// pnpm.cmd and npm.cmd start node, which can outlive the killed cmd.exe
	// and keep the pipes open; stop waiting for them after a while.
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func withOutput(err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" {
		return err
	}
	if len(output) > 2000 {
		output = output[len(output)-2000:]
	}
	return fmt.Errorf("%w: %s", err, output)
}

// protectedFolders lists folders that hold the user's own data or the
// system. A cleanup folder may be inside them, as most caches live under
// %LOCALAPPDATA%, but never one of them or above one of them.
func protectedFolders() []string {
	folders := []string{}
	for _, name := range []string{
		"USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot",
		"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ProgramData",
		"PUBLIC", "OneDrive", "OneDriveConsumer", "OneDriveCommercial",
	} {
		if value := os.Getenv(name); value != "" {
			folders = append(folders, value)
		}
	}
	for _, id := range []*windows.KNOWNFOLDERID{
		windows.FOLDERID_Profile, windows.FOLDERID_Desktop, windows.FOLDERID_Documents,
		windows.FOLDERID_Downloads, windows.FOLDERID_Pictures, windows.FOLDERID_Music,
		windows.FOLDERID_Videos, windows.FOLDERID_Windows, windows.FOLDERID_ProgramFiles,
	} {
		if path, err := windows.KnownFolderPath(id, 0); err == nil {
			folders = append(folders, path)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range []string{".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".config", "go"} {
			folders = append(folders, filepath.Join(home, name))
		}
	}
	return folders
}

// lockCleanup takes %LOCALAPPDATA%\Skilli\cleanup.lock. Windows releases
// the lock when the process exits, so a crash cannot leave it stuck.
func lockCleanup() (func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "Skilli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "cleanup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	overlapped := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		file.Close()
		return nil, ErrBusy
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
		file.Close()
	}, nil
}
