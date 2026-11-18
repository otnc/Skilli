package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encoding/json/v2"

	"github.com/otnc/Skilli/internal/cleanup"
	"github.com/otnc/Skilli/internal/storage"
)

type testCLI struct {
	*cli
	stdout, stderr *bytes.Buffer
	runner         *fakeRunner
	openedURIs     []string
}

type fakeRunner struct {
	exe, dir string
	runs     int
}

func (f *fakeRunner) LookPath(string) (string, error) { return f.exe, nil }
func (f *fakeRunner) Output(context.Context, string, []string) (string, error) {
	return f.dir, nil
}
func (f *fakeRunner) Run(context.Context, string, []string) (string, error) {
	f.runs++
	return "", nil
}

func newTestCLI(t *testing.T, stdin string, interactive bool) *testCLI {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "uv-cache")
	os.MkdirAll(cache, 0o700)
	os.WriteFile(filepath.Join(cache, "blob"), make([]byte, 2048), 0o600)
	exe := filepath.Join(root, "uv.exe")
	os.WriteFile(exe, []byte("binary"), 0o700)

	tc := &testCLI{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: &fakeRunner{exe: exe, dir: cache}}
	tc.cli = &cli{
		stdin:       strings.NewReader(stdin),
		stdout:      tc.stdout,
		stderr:      tc.stderr,
		interactive: interactive,
		readDrives: func() (storage.Snapshot, error) {
			return storage.Snapshot{}, errors.New("not used")
		},
		newEngine: func() (*cleanup.Engine, error) {
			return &cleanup.Engine{
				Runner:    tc.runner,
				Protected: func() []string { return nil },
				FreeBytes: func(string) (uint64, error) { return 0, nil },
				Lock:      func() (func(), error) { return func() {}, nil },
				History:   &cleanup.History{Path: filepath.Join(root, "history.jsonl")},
				Getenv:    func(string) string { return cache },
				Now:       time.Now,
			}, nil
		},
		openUpdater: func() error { return nil },
		openURI: func(uri string) error {
			tc.openedURIs = append(tc.openedURIs, uri)
			return nil
		},
	}
	return tc
}

func TestStatusJSONReportsPartialMeasurement(t *testing.T) {
	tc := newTestCLI(t, "", false)
	tc.readDrives = func() (storage.Snapshot, error) {
		return storage.Snapshot{
			SchemaVersion: 1,
			MeasuredAt:    time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
			Drives: []storage.Drive{
				{Root: `C:\`, CapacityBytes: 100, FreeBytes: 30},
				{Root: `D:\`, ErrorCode: storage.ErrorNotReady, Error: "device not ready"},
			},
		}, nil
	}
	if code := tc.run(t.Context(), []string{"status", "--json"}); code != exitPartial {
		t.Fatalf("exit = %d; want %d; stderr = %q", code, exitPartial, tc.stderr.String())
	}
	var output storage.Snapshot
	if err := json.Unmarshal(tc.stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.SchemaVersion != 1 || len(output.Drives) != 2 ||
		output.Drives[0].FreeBytes != 30 || output.Drives[1].ErrorCode != storage.ErrorNotReady {
		t.Fatalf("unexpected snapshot: %+v", output)
	}
}

func TestStatusRejectsUnexpectedArgumentsBeforeReading(t *testing.T) {
	tc := newTestCLI(t, "", false)
	tc.readDrives = func() (storage.Snapshot, error) {
		t.Fatal("invalid arguments must not read drive capacity")
		return storage.Snapshot{}, nil
	}
	if code := tc.run(t.Context(), []string{"status", "extra"}); code != exitUsage {
		t.Fatalf("exit = %d; want %d", code, exitUsage)
	}
	if !strings.Contains(tc.stderr.String(), "skilli status") || tc.stdout.Len() != 0 {
		t.Fatalf("stdout = %q; stderr = %q", tc.stdout.String(), tc.stderr.String())
	}
}

func TestStatusReportsDriveEnumerationFailure(t *testing.T) {
	tc := newTestCLI(t, "", false)
	tc.readDrives = func() (storage.Snapshot, error) {
		return storage.Snapshot{}, errors.New("drive enumeration failed")
	}
	if code := tc.run(t.Context(), []string{"status", "--json"}); code != exitError {
		t.Fatalf("exit = %d; want %d", code, exitError)
	}
	if tc.stdout.Len() != 0 || !strings.Contains(tc.stderr.String(), "drive enumeration failed") {
		t.Fatalf("stdout = %q; stderr = %q", tc.stdout.String(), tc.stderr.String())
	}
}

func TestScanJSONMeasuresSelectedTarget(t *testing.T) {
	tc := newTestCLI(t, "", false)
	if code := tc.run(t.Context(), []string{"scan", "--json", "uv"}); code != exitOK {
		t.Fatalf("exit = %d; stderr = %q", code, tc.stderr.String())
	}
	var report struct {
		Targets []struct {
			ID   string `json:"id"`
			Size struct {
				Bytes uint64 `json:"bytes"`
			} `json:"size"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(tc.stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].ID != "uv" || report.Targets[0].Size.Bytes != 2048 {
		t.Fatalf("report = %+v from %s", report, tc.stdout.String())
	}
}

func TestScanRejectsUnknownTarget(t *testing.T) {
	tc := newTestCLI(t, "", false)
	if code := tc.run(t.Context(), []string{"scan", "nope"}); code != exitUsage {
		t.Fatalf("exit = %d", code)
	}
}

func TestCleanNeedsInteractiveConfirmation(t *testing.T) {
	tc := newTestCLI(t, "y\n", false)
	if code := tc.run(t.Context(), []string{"clean", "uv"}); code != exitNeedConfirm {
		t.Fatalf("exit = %d; stderr = %q", code, tc.stderr.String())
	}
	if tc.runner.runs != 0 {
		t.Fatal("a non-interactive clean must not run the command")
	}
}

func TestCleanRunsOnlyAfterYes(t *testing.T) {
	declined := newTestCLI(t, "\n", true)
	if code := declined.run(t.Context(), []string{"clean", "uv"}); code != exitOK || declined.runner.runs != 0 {
		t.Fatalf("exit = %d, runs = %d", code, declined.runner.runs)
	}

	accepted := newTestCLI(t, "y\n", true)
	if code := accepted.run(t.Context(), []string{"clean", "uv"}); code != exitOK || accepted.runner.runs != 1 {
		t.Fatalf("exit = %d, runs = %d, stderr = %q", code, accepted.runner.runs, accepted.stderr.String())
	}
	accepted.stdout.Reset()
	if code := accepted.run(t.Context(), []string{"history", "--json"}); code != exitOK || !strings.Contains(accepted.stdout.String(), `"status":"succeeded"`) {
		t.Fatalf("history exit = %d, out = %s", code, accepted.stdout.String())
	}
}

func TestCleanTempOpensWindowsSettingsInsteadOfDeleting(t *testing.T) {
	tc := newTestCLI(t, "y\n", true)
	if code := tc.run(t.Context(), []string{"clean", "temp"}); code != exitOK || tc.runner.runs != 0 {
		t.Fatalf("exit = %d, runs = %d", code, tc.runner.runs)
	}
	if len(tc.openedURIs) != 1 || tc.openedURIs[0] != "ms-settings:storagesense" {
		t.Fatalf("opened = %q", tc.openedURIs)
	}
}
