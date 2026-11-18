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

// fakeRunner resolves every command to exe, answers --version with
// version and any other Output with dir.
type fakeRunner struct {
	exe       string
	dir       string
	version   string
	outputErr error
	runs      [][]string
	// onRun lets a test change the world while the cleanup command runs.
	onRun func()
}

func (f *fakeRunner) LookPath(string) (string, error) { return f.exe, nil }

func (f *fakeRunner) Output(_ context.Context, _, _ string, args []string) (string, error) {
	if f.outputErr != nil {
		return "", f.outputErr
	}
	if len(args) == 1 && args[0] == "--version" {
		return f.version + "\r\n", nil
	}
	return f.dir + "\r\n", nil
}

func (f *fakeRunner) Run(_ context.Context, dir, exe string, args []string) (string, error) {
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
		Home:      func() (string, error) { return root, nil },
		Running:   func([]string) ([]string, error) { return nil, nil },
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
	target, _ := FindTarget("temp")
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

func TestLocateFailureMeansMissingOnlyWhereDeclared(t *testing.T) {
	f := newFixture(t)
	f.runner.outputErr = errors.New("exit status 1: No module named pip")
	pip, _ := FindTarget("pip")
	if loc := f.engine.Locate(t.Context(), pip); loc.Status != Missing {
		t.Fatalf("pip without pip must be missing, got %+v", loc)
	}
	if loc := f.engine.Locate(t.Context(), uvTarget(t)); loc.Status != Failed {
		t.Fatalf("a failing uv must be an error, got %+v", loc)
	}
}

func TestIsAppExecutionAlias(t *testing.T) {
	local := `C:\Users\me\AppData\Local`
	for exe, want := range map[string]bool{
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps\python.exe`: true,
		`c:\users\me\appdata\local\microsoft\windowsapps\python.exe`: true,
		`C:\Users\me\.local\bin\python.exe`:                          false,
		`C:\Program Files\Go\bin\go.exe`:                             false,
	} {
		if got := IsAppExecutionAlias(exe, local); got != want {
			t.Errorf("IsAppExecutionAlias(%q) = %v; want %v", exe, got, want)
		}
	}
}

func TestTargetsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, target := range Targets() {
		if seen[target.ID] {
			t.Errorf("duplicate ID %s", target.ID)
		}
		seen[target.ID] = true
		byCommand := target.locatedByCommand()
		byEnvironment := target.EnvDir != "" || target.DefaultDir != ""
		if byCommand == byEnvironment {
			t.Errorf("%s must be located either by a command or by the environment", target.ID)
		}
		if byCommand && target.Clean != nil && target.Clean.Name != target.Locate.Name {
			t.Errorf("%s locates with %s but cleans with %s", target.ID, target.Locate.Name, target.Clean.Name)
		}
		if target.Note == "" {
			t.Errorf("%s has no note", target.ID)
		}
	}
}

func TestLocateFromEnvironmentWithDefaultsAndSubDir(t *testing.T) {
	f := newFixture(t)
	home, _ := f.engine.Home()
	cargo, _ := FindTarget("cargo")
	os.MkdirAll(filepath.Join(home, ".cargo", "registry"), 0o700)
	if loc := f.engine.Locate(t.Context(), cargo); loc.Status != Found || !samePath(loc.Dir, filepath.Join(home, ".cargo", "registry")) {
		t.Fatalf("default CARGO_HOME: %+v", loc)
	}

	custom := filepath.Join(home, "custom-cargo")
	os.MkdirAll(filepath.Join(custom, "registry"), 0o700)
	f.engine.Getenv = func(name string) string {
		if name == "CARGO_HOME" {
			return custom
		}
		return ""
	}
	if loc := f.engine.Locate(t.Context(), cargo); loc.Status != Found || !samePath(loc.Dir, filepath.Join(custom, "registry")) {
		t.Fatalf("CARGO_HOME set: %+v", loc)
	}

	pub, _ := FindTarget("dart-pub")
	if loc := f.engine.Locate(t.Context(), pub); loc.Status != Failed {
		t.Fatalf("pub without LOCALAPPDATA must fail, got %+v", loc)
	}
}

func TestPlanForEnvironmentTargetResolvesCleanCommand(t *testing.T) {
	f := newFixture(t)
	f.engine.Getenv = func(name string) string {
		if name == "PUB_CACHE" {
			return f.cache
		}
		return ""
	}
	pub, _ := FindTarget("dart-pub")
	plan, err := f.engine.NewPlan(t.Context(), pub)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executable != f.runner.exe || !samePath(plan.Dir, f.cache) {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := f.engine.Apply(t.Context(), plan, "cli"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.runner.runs[0][1:], " "); got != "pub cache clean --force" {
		t.Fatalf("ran %q", got)
	}
}

func TestPerDriveCopies(t *testing.T) {
	targets := TargetsFor([]string{`C:\`, `D:\`, `E:\`}, `C:\Users\me`)
	var ids []string
	for _, target := range targets {
		if target.IsDriveCopy() {
			ids = append(ids, target.ID)
		}
	}
	if strings.Join(ids, ",") != "pnpm-d,pnpm-e" {
		t.Fatalf("drive copies = %v", ids)
	}
	copyD, ok := FindTarget("pnpm-d")
	if !ok || copyD.WorkDir != `D:\` || copyD.Name != "pnpm ストア (D:)" || copyD.Clean == nil {
		t.Fatalf("FindTarget(pnpm-d) = %+v, %v", copyD, ok)
	}
	for _, id := range []string{"uv-d", "pnpm-", "pnpm-dd", "pnpm-1"} {
		if _, ok := FindTarget(id); ok {
			t.Errorf("FindTarget(%q) must fail", id)
		}
	}
}

func TestCommandsRunInTheTargetWorkDir(t *testing.T) {
	f := newFixture(t)
	var dirs []string
	f.engine.Runner = &dirRecorder{fakeRunner: f.runner, dirs: &dirs}
	copyD, _ := FindTarget("pnpm-d")
	if copyD.RequireDir != `D:\.pnpm-store` {
		t.Fatalf("RequireDir = %q", copyD.RequireDir)
	}

	copyD.RequireDir = filepath.Join(filepath.Dir(f.cache), "no-store-here")
	if loc := f.engine.Locate(t.Context(), copyD); loc.Status != Missing || len(dirs) != 0 {
		t.Fatalf("a drive without a store must be missing without running pnpm: %+v, dirs = %q", loc, dirs)
	}

	copyD.RequireDir = f.cache
	f.engine.Locate(t.Context(), copyD)
	f.engine.Locate(t.Context(), uvTarget(t))
	if len(dirs) != 2 || dirs[0] != `D:\` || dirs[1] != "" {
		t.Fatalf("dirs = %q", dirs)
	}
}

type dirRecorder struct {
	*fakeRunner
	dirs *[]string
}

func (r *dirRecorder) Output(ctx context.Context, dir, exe string, args []string) (string, error) {
	*r.dirs = append(*r.dirs, dir)
	return r.fakeRunner.Output(ctx, dir, exe, args)
}

func TestYarnCleanupChecksTheYarnVersion(t *testing.T) {
	cases := []struct {
		id, version string
		wantErr     bool
		wantArgs    string
	}{
		{"yarn", "1.22.22", false, "cache clean"},
		{"yarn", "4.5.1", true, ""},
		{"yarn-berry", "4.5.1", false, "cache clean --all"},
		{"yarn-berry", "3.8.7", false, "cache clean --all"},
		{"yarn-berry", "1.22.22", true, ""},
		{"yarn-berry", "not a version", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.id+"@"+tc.version, func(t *testing.T) {
			f := newFixture(t)
			f.runner.version = tc.version
			f.engine.Getenv = func(name string) string {
				if name == "YARN_GLOBAL_FOLDER" {
					return filepath.Dir(f.cache)
				}
				return ""
			}
			target, _ := FindTarget(tc.id)
			plan, err := f.engine.NewPlan(t.Context(), target)
			if tc.wantErr {
				if !errors.Is(err, ErrWrongVersion) {
					t.Fatalf("NewPlan: %v; want ErrWrongVersion", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// The version is checked again right before running.
			f.runner.version = "0.0.1"
			if _, err := f.engine.Apply(t.Context(), plan, "cli"); !errors.Is(err, ErrWrongVersion) || len(f.runner.runs) != 0 {
				t.Fatalf("Apply after a version change: %v, runs = %q", err, f.runner.runs)
			}
			f.runner.version = tc.version
			if plan, err = f.engine.NewPlan(t.Context(), target); err != nil {
				t.Fatal(err)
			}
			if _, err := f.engine.Apply(t.Context(), plan, "cli"); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(f.runner.runs[0][1:], " "); got != tc.wantArgs {
				t.Fatalf("ran %q; want %q", got, tc.wantArgs)
			}
		})
	}
}

func TestFlutterBundledDart(t *testing.T) {
	bin := t.TempDir()
	wrapper := filepath.Join(bin, "dart.bat")
	os.WriteFile(wrapper, nil, 0o700)

	if got, err := flutterBundledDart(wrapper); err != nil || got != wrapper {
		t.Fatalf("dart.bat outside Flutter = %q, %v; want it unchanged", got, err)
	}

	os.WriteFile(filepath.Join(bin, "flutter.bat"), nil, 0o700)
	if _, err := flutterBundledDart(wrapper); err == nil {
		t.Fatal("Flutter without its Dart SDK must be refused rather than run the wrapper")
	}

	bundled := filepath.Join(bin, "cache", "dart-sdk", "bin", "dart.exe")
	os.MkdirAll(filepath.Dir(bundled), 0o700)
	os.WriteFile(bundled, nil, 0o700)
	if got, err := flutterBundledDart(wrapper); err != nil || got != bundled {
		t.Fatalf("Flutter's dart = %q, %v; want %q", got, err, bundled)
	}

	standalone := filepath.Join(t.TempDir(), "dart.exe")
	if got, err := flutterBundledDart(standalone); err != nil || got != standalone {
		t.Fatalf("standalone dart = %q, %v", got, err)
	}
}

func TestMajorVersion(t *testing.T) {
	for out, want := range map[string]int{"4.5.1\r\n": 4, "1.22.22": 1, " 10.0.403\nextra": 10} {
		if got, err := majorVersion(out); err != nil || got != want {
			t.Errorf("majorVersion(%q) = %d, %v; want %d", out, got, err, want)
		}
	}
	if _, err := majorVersion("yarn"); err == nil {
		t.Error("a non-number must be an error")
	}
}

func TestParsers(t *testing.T) {
	nuget := "Welcome to .NET!\r\nglobal-packages: C:\\Users\\me\\.nuget\\packages\\\r\n"
	if got, err := valueAfter("global-packages:")(nuget); err != nil || got != `C:\Users\me\.nuget\packages\` {
		t.Errorf("nuget = %q, %v", got, err)
	}
	deno := `{"version":1,"denoDir":"C:\\Users\\me\\AppData\\Local\\deno","modulesCache":"x"}`
	if got, err := stringInJSON("denoDir")(deno); err != nil || got != `C:\Users\me\AppData\Local\deno` {
		t.Errorf("deno = %q, %v", got, err)
	}
	if _, err := stringInJSON("denoDir")(`{"denoDir": 1}`); err == nil {
		t.Error("a non-string denoDir must be an error")
	}
	quoted := "label: \"C:\\a b\"\n"
	if got, err := valueAfter("label:")(quoted); err != nil || got != `C:\a b` {
		t.Errorf("quoted = %q, %v", got, err)
	}
	if _, err := valueAfter("global-packages:")("nothing here"); err == nil {
		t.Error("a missing label must be an error")
	}
	conda := `{"pkgs_dirs": ["C:\\Users\\me\\miniconda3\\pkgs", "C:\\Users\\me\\.conda\\pkgs"], "envs": []}`
	if got, err := firstOfJSONList("pkgs_dirs")(conda); err != nil || got != `C:\Users\me\miniconda3\pkgs` {
		t.Errorf("conda = %q, %v", got, err)
	}
	for _, bad := range []string{`not json`, `{"pkgs_dirs": []}`, `{"pkgs_dirs": [1]}`} {
		if _, err := firstOfJSONList("pkgs_dirs")(bad); err == nil {
			t.Errorf("conda %q must be an error", bad)
		}
	}
}
