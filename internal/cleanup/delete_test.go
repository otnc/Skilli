package cleanup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveContentsKeepsRootAndLinkedTargets(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.bin"), 10)
	write(t, filepath.Join(root, "nested", "deep", "b.bin"), 10)
	readOnly := filepath.Join(root, "nested", "ro.bin")
	write(t, readOnly, 10)
	if err := os.Chmod(readOnly, 0o400); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	keep := filepath.Join(outside, "keep.bin")
	write(t, keep, 10)
	linked := os.Symlink(outside, filepath.Join(root, "link")) == nil

	stats, err := RemoveContents(t.Context(), root, RemoveOptions{})
	skipped := stats.Skipped
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if linked {
		if skipped != 1 || len(entries) != 1 || entries[0].Name() != "link" {
			t.Fatalf("skipped = %d, left = %v; want only the link left", skipped, entries)
		}
	} else if skipped != 0 || len(entries) != 0 {
		t.Fatalf("skipped = %d, left = %v", skipped, entries)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("the folder itself must stay")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a link's target must never be deleted")
	}
}

func TestRemoveContentsRefusesALinkAsRoot(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "keep.bin"), 10)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := RemoveContents(t.Context(), link, RemoveOptions{}); !errors.Is(err, ErrUnsafeFolder) {
		t.Fatalf("RemoveContents(link) = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.bin")); err != nil {
		t.Fatal("nothing behind the link may be deleted")
	}
}

func TestDeletionRemovesOnlyItsPaths(t *testing.T) {
	f := newFixture(t)
	home, _ := f.engine.Home()
	hf := filepath.Join(home, ".cache", "huggingface")
	write(t, filepath.Join(hf, "hub", "models--x", "blob"), 100)
	write(t, filepath.Join(hf, "xet", "shard"), 50)
	write(t, filepath.Join(hf, "token"), 5)

	target, _ := FindTarget("huggingface")
	plan, err := f.engine.NewPlan(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Deletes() || plan.Size.Bytes != 150 || len(plan.Scope) != 3 {
		t.Fatalf("plan = %+v", plan)
	}
	result, err := f.engine.Apply(t.Context(), plan, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if result.SizeAfter.Bytes != 0 || result.Skipped != 0 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(hf, "token")); err != nil {
		t.Fatal("the token must survive")
	}
	if entries, _ := os.ReadDir(filepath.Join(hf, "hub")); len(entries) != 0 {
		t.Fatalf("hub still holds %v", entries)
	}
	if len(f.runner.runs) != 0 {
		t.Fatalf("a deletion must not run commands: %q", f.runner.runs)
	}
	history, _ := (&History{Path: f.history}).Read(f.now.AddDate(0, 0, -1))
	if len(history) != 1 || history[0].Method != MethodDelete || history[0].Status != StatusSucceeded {
		t.Fatalf("history = %+v", history)
	}
}

func TestDeletionRefusesAReplacedFolder(t *testing.T) {
	f := newFixture(t)
	home, _ := f.engine.Home()
	bun := filepath.Join(home, ".bun", "install", "cache")
	write(t, filepath.Join(bun, "pkg"), 10)
	target, _ := FindTarget("bun")
	plan, err := f.engine.NewPlan(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(bun); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(bun, "someone-elses"), 10)
	if _, err := f.engine.Apply(t.Context(), plan, "cli"); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("Apply = %v; want ErrPlanChanged", err)
	}
	if _, err := os.Stat(filepath.Join(bun, "someone-elses")); err != nil {
		t.Fatal("a folder created after the plan must not be emptied")
	}
}

func TestCleanupIsRefusedWhileTheToolRuns(t *testing.T) {
	f := newFixture(t)
	running := []string{"uv.exe"}
	f.engine.Running = func(names []string) ([]string, error) {
		var hit []string
		for _, name := range names {
			for _, r := range running {
				if strings.EqualFold(name, r) {
					hit = append(hit, name)
				}
			}
		}
		return hit, nil
	}
	if _, err := f.engine.NewPlan(t.Context(), uvTarget(t)); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "uv.exe") {
		t.Fatalf("NewPlan while uv runs = %v", err)
	}

	running = nil
	plan, err := f.engine.NewPlan(t.Context(), uvTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	running = []string{"uvx.exe"}
	if _, err := f.engine.Apply(t.Context(), plan, "cli"); !errors.Is(err, ErrInUse) || len(f.runner.runs) != 0 {
		t.Fatalf("Apply after uvx started = %v, runs = %q", err, f.runner.runs)
	}
}

func TestHistoryHidesTheHomeFolder(t *testing.T) {
	f := newFixture(t)
	home, _ := f.engine.Home()
	plan, err := f.engine.NewPlan(t.Context(), uvTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	f.runner.dir = filepath.Join(strings.ToUpper(home), "elsewhere")
	os.MkdirAll(f.runner.dir, 0o700)
	f.engine.Apply(t.Context(), plan, "cli")
	history, _ := (&History{Path: f.history}).Read(f.now.AddDate(0, 0, -1))
	if len(history) != 1 || strings.Contains(strings.ToLower(history[0].Error), strings.ToLower(home)) || !strings.Contains(history[0].Error, "~") {
		t.Fatalf("history = %+v", history)
	}
}
