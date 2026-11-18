package cleanup

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner resolves every command to exe and answers Output with dir.
type fakeRunner struct {
	exe  string
	dir  string
	runs [][]string
	// onRun lets a test change the world while the cleanup command runs.
	onRun func()
}

func (f *fakeRunner) LookPath(string) (string, error) { return f.exe, nil }

func (f *fakeRunner) Output(context.Context, string, []string) (string, error) {
	return f.dir + "\r\n", nil
}

func (f *fakeRunner) Run(_ context.Context, exe string, args []string) (string, error) {
	f.runs = append(f.runs, append([]string{exe}, args...))
	if f.onRun != nil {
		f.onRun()
	}
	return "done", nil
}

type fixture struct {
	engine  *Engine
	runner  *fakeRunner
	now     time.Time
	cache   string
	history string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "tool", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "blob"), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "uv.exe")
	if err := os.WriteFile(exe, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &fixture{runner: &fakeRunner{exe: exe, dir: cache}, now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC), cache: cache}
	f.history = filepath.Join(root, "history.jsonl")
	f.engine = &Engine{
		Runner:    f.runner,
		Protected: func() []string { return []string{filepath.Join(root, "profile")} },
		FreeBytes: func(string) (uint64, error) { return 1000, nil },
		Lock:      func() (func(), error) { return func() {}, nil },
		History:   &History{Path: f.history},
		Getenv:    func(string) string { return "" },
		Now:       func() time.Time { return f.now },
	}
	return f
}

func uvTarget(t *testing.T) Target {
	t.Helper()
	target, ok := FindTarget("uv")
	if !ok {
		t.Fatal("uv target missing")
	}
	return target
}

func TestApplyRunsCleanCommandAndRecordsHistory(t *testing.T) {
	f := newFixture(t)
	plan, err := f.engine.NewPlan(t.Context(), uvTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Size.Bytes != 100 || plan.Size.Files != 1 {
		t.Fatalf("size = %+v", plan.Size)
	}
	if _, err := f.engine.Apply(t.Context(), plan, "cli"); err != nil {
		t.Fatal(err)
	}
	if len(f.runner.runs) != 1 || strings.Join(f.runner.runs[0][1:], " ") != "cache prune" {
		t.Fatalf("runs = %q", f.runner.runs)
	}
	entries, err := (&History{Path: f.history}).Read(time.Time{})
	if err != nil || len(entries) != 1 || entries[0].Status != StatusSucceeded || entries[0].Source != "cli" {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}
}

func TestApplyRefusesReuseAndExpiry(t *testing.T) {
	f := newFixture(t)
	plan, err := f.engine.NewPlan(t.Context(), uvTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Apply(t.Context(), plan, "gui"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Apply(t.Context(), plan, "gui"); !errors.Is(err, ErrPlanUsed) {
		t.Fatalf("second apply: %v", err)
	}

	expired, err := f.engine.NewPlan(t.Context(), uvTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(PlanLifetime)
	if _, err := f.engine.Apply(t.Context(), expired, "gui"); !errors.Is(err, ErrPlanExpired) {
		t.Fatalf("expired apply: %v", err)
	}
	if len(f.runner.runs) != 1 {
		t.Fatalf("refused plans must not run: %q", f.runner.runs)
	}
}

func TestApplyRefusesWhenFolderOrExecutableChanged(t *testing.T) {
	cases := map[string]func(f *fixture){
		"folder moved": func(f *fixture) {
			other := filepath.Join(filepath.Dir(f.cache), "other")
			os.MkdirAll(other, 0o700)
			f.runner.dir = other
		},
		"executable replaced": func(f *fixture) {
			os.WriteFile(f.runner.exe, []byte("a different binary"), 0o700)
		},
		"executable resolves elsewhere": func(f *fixture) {
			f.runner.exe = filepath.Join(filepath.Dir(f.runner.exe), "other-uv.exe")
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			plan, err := f.engine.NewPlan(t.Context(), uvTarget(t))
			if err != nil {
				t.Fatal(err)
			}
			change(f)
			if _, err := f.engine.Apply(t.Context(), plan, "gui"); !errors.Is(err, ErrPlanChanged) {
				t.Fatalf("apply: %v", err)
			}
			if len(f.runner.runs) != 0 {
				t.Fatalf("changed plans must not run: %q", f.runner.runs)
			}
			entries, _ := (&History{Path: f.history}).Read(time.Time{})
			if len(entries) != 1 || entries[0].Status != StatusRefused {
				t.Fatalf("entries = %+v", entries)
			}
		})
	}
}

func TestNewPlanRefusesProtectedFolders(t *testing.T) {
	f := newFixture(t)
	f.engine.Protected = func() []string { return []string{filepath.Join(f.cache, "inner")} }
	if _, err := f.engine.NewPlan(t.Context(), uvTarget(t)); !errors.Is(err, ErrUnsafeFolder) {
		t.Fatalf("NewPlan: %v", err)
	}
}

func TestNewPlanRefusesMeasureOnlyTarget(t *testing.T) {
	f := newFixture(t)
	target, _ := FindTarget("go-mod")
	if _, err := f.engine.NewPlan(t.Context(), target); !errors.Is(err, ErrNotCleanable) {
		t.Fatalf("NewPlan: %v", err)
	}
}

func TestApplyRefusesWhenLockIsHeld(t *testing.T) {
	f := newFixture(t)
	f.engine.Lock = func() (func(), error) { return nil, ErrBusy }
	plan, err := f.engine.NewPlan(t.Context(), uvTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Apply(t.Context(), plan, "cli"); !errors.Is(err, ErrBusy) || len(f.runner.runs) != 0 {
		t.Fatalf("apply: %v, runs = %q", err, f.runner.runs)
	}
}

func TestCheckCleanable(t *testing.T) {
	protected := []string{`C:\Users\me`, `C:\Windows`}
	for dir, wantOK := range map[string]bool{
		`C:\Users\me\AppData\Local\uv\cache`: true,
		`D:\caches\go-build`:                 true,
		`C:\Users\me`:                        false,
		`c:\users\ME\`:                       false,
		`C:\Users`:                           false,
		`C:\`:                                false,
		`D:\`:                                false,
		`relative\cache`:                     false,
	} {
		err := CheckCleanable(dir, protected)
		if (err == nil) != wantOK {
			t.Errorf("CheckCleanable(%q) = %v; want ok = %v", dir, err, wantOK)
		}
	}
}

func TestLocateRejectsUnexpectedOutput(t *testing.T) {
	f := newFixture(t)
	for _, out := range []string{"", "off", "C:\\a\nC:\\b"} {
		f.runner.dir = out
		if loc := f.engine.Locate(t.Context(), uvTarget(t)); loc.Status != Failed {
			t.Errorf("Locate with output %q = %+v", out, loc)
		}
	}
}

func TestMeasureSkipsLinkedFolders(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a"), make([]byte, 10), 0o600)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "big"), make([]byte, 1000), 0o600)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	size, err := Measure(t.Context(), root)
	if err != nil || size.Bytes != 10 {
		t.Fatalf("size = %+v, err = %v", size, err)
	}
}

func TestScanJSONFlattensLocation(t *testing.T) {
	f := newFixture(t)
	data, err := json.Marshal(f.engine.Scan(t.Context(), uvTarget(t)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"uv"`, `"status":"found"`, `"bytes":100`, `"clean_command":["uv","cache","prune"]`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%s lacks %s", data, want)
		}
	}
}

func TestCleanCommandsUseTheLocatingTool(t *testing.T) {
	for _, target := range Targets() {
		if target.Clean != nil && target.Clean.Name != target.Locate.Name {
			t.Errorf("%s locates with %s but cleans with %s", target.ID, target.Locate.Name, target.Clean.Name)
		}
	}
}
