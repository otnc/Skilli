package cleanup

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/otnc/Skilli/internal/config"
)

func age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestTempDeletionKeepsRecentAndProtectedFiles(t *testing.T) {
	root := t.TempDir()
	oldFile := filepath.Join(root, "old.tmp")
	newFile := filepath.Join(root, "new.tmp")
	hidden := filepath.Join(root, "hidden.tmp")
	nestedOld := filepath.Join(root, "installer", "part.bin")
	for _, p := range []string{oldFile, newFile, hidden, nestedOld} {
		write(t, p, 10)
	}
	p, _ := syscall.UTF16PtrFromString(hidden)
	if err := syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{oldFile, hidden, nestedOld, filepath.Dir(nestedOld)} {
		age(t, p, 30*24*time.Hour)
	}

	opts := RemoveOptions{Before: time.Now().Add(-7 * 24 * time.Hour), KeepProtected: true}
	size, err := MeasureSelected(t.Context(), root, opts)
	if err != nil || size.Files != 2 {
		t.Fatalf("measured %+v, %v; want the 2 old plain files", size, err)
	}
	stats, err := RemoveContents(t.Context(), root, opts)
	if err != nil {
		t.Fatal(err)
	}
	for path, wantKept := range map[string]bool{oldFile: false, nestedOld: false, filepath.Dir(nestedOld): false, newFile: true, hidden: true} {
		_, err := os.Stat(path)
		if kept := err == nil; kept != wantKept {
			t.Errorf("%s kept = %v; want %v", filepath.Base(path), kept, wantKept)
		}
	}
	if stats.Kept != 2 || stats.Skipped != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

// The links' own times cannot be set (os.Chtimes follows links), so the
// age rule is checked by moving the cutoff instead.
func TestLinksOnlyRemovesOldLinksNotInPath(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	write(t, filepath.Join(target, "node.exe"), 10)
	link := func(name string) string {
		path := filepath.Join(root, name)
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symbolic links are unavailable: %v", err)
		}
		return path
	}
	first, inPath := link("1"), link("2_in_path")
	plain := filepath.Join(root, "plain.txt")
	write(t, plain, 10)

	// A cutoff an hour ago makes every link too recent to remove.
	stats, err := RemoveContents(t.Context(), root, RemoveOptions{LinksOnly: true, Before: time.Now().Add(-time.Hour), Keep: []string{inPath}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(first); err != nil || stats.Kept != 3 {
		t.Fatalf("recent links must stay: %v, stats = %+v", err, stats)
	}

	// A cutoff in the future makes them old: all go but the one in PATH.
	opts := RemoveOptions{LinksOnly: true, Before: time.Now().Add(time.Hour), Keep: []string{inPath}}
	size, err := MeasureSelected(t.Context(), root, opts)
	if err != nil || size.Links != 1 || size.Files != 0 {
		t.Fatalf("measured %+v, %v; want 1 link", size, err)
	}
	if _, err := RemoveContents(t.Context(), root, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(first); !errors.Is(err, os.ErrNotExist) {
		t.Error("an old link outside PATH must be removed")
	}
	for _, path := range []string{inPath, plain} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s must stay: %v", filepath.Base(path), err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "node.exe")); err != nil {
		t.Fatal("what a link points to must never be removed")
	}
}

func TestFallbackFromMissingCommandToDeletion(t *testing.T) {
	f := newFixture(t)
	custom := FromCustom(config.CustomTarget{
		ID: "my-1", Name: "mine", Dir: f.cache, Command: []string{"mytool", "clean"}, Method: config.MethodCommandThenDelete,
	})
	f.engine.Runner = &missingTool{fakeRunner: f.runner}
	plan, err := f.engine.NewPlan(t.Context(), custom)
	if err != nil || !plan.Deletes() || plan.Fallback == "" {
		t.Fatalf("plan = %+v, %v; want a deletion after the missing command", plan, err)
	}

	commandOnly := FromCustom(config.CustomTarget{ID: "my-2", Name: "mine", Dir: f.cache, Command: []string{"mytool"}, Method: config.MethodCommand})
	if _, err := f.engine.NewPlan(t.Context(), commandOnly); !errors.Is(err, ErrToolMissing) {
		t.Fatalf("a command-only target must not fall back: %v", err)
	}
}

func TestFallbackDoesNotSkipSafetyChecks(t *testing.T) {
	f := newFixture(t)
	f.engine.Protected = func() []string { return []string{filepath.Join(f.cache, "inner")} }
	custom := FromCustom(config.CustomTarget{ID: "my-1", Name: "mine", Dir: f.cache, Command: []string{"mytool"}, Method: config.MethodCommandThenDelete})
	f.engine.Runner = &missingTool{fakeRunner: f.runner}
	if _, err := f.engine.NewPlan(t.Context(), custom); !errors.Is(err, ErrUnsafeFolder) {
		t.Fatalf("NewPlan = %v; want ErrUnsafeFolder", err)
	}
}

// missingTool behaves as fakeRunner but finds no executable.
type missingTool struct{ *fakeRunner }

func (missingTool) LookPath(name string) (string, error) {
	return "", errors.New(name + ": executable file not found in %PATH%")
}

func TestFindCustomTarget(t *testing.T) {
	custom := []Target{FromCustom(config.CustomTarget{ID: "my-1", Name: "mine", Dir: `C:\x`, Method: config.MethodDelete})}
	if got, ok := Find(custom, "my-1"); !ok || !got.Custom || got.Delete == nil || got.Clean != nil {
		t.Fatalf("Find(my-1) = %+v, %v", got, ok)
	}
	if got, ok := Find(custom, "uv"); !ok || got.ID != "uv" {
		t.Fatalf("built-in targets must still be found: %+v", got)
	}
	if got, ok := Find(custom, "pnpm-d"); !ok || got.WorkDir != `D:\` {
		t.Fatalf("drive copies must still be found: %+v", got)
	}
}
