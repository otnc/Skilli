package cleanup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"uuid"
)

// Location states.
const (
	Found = "found"
	// Missing means the tool is not installed or its folder does not exist.
	Missing = "missing"
	Failed  = "error"
)

// Timeouts of the external commands.
const (
	LocateTimeout = 30 * time.Second
	CleanTimeout  = 10 * time.Minute
	PlanLifetime  = 10 * time.Minute
)

var (
	// ErrBusy reports that another cleanup holds the lock.
	ErrBusy = errors.New("another cleanup is running")
	// ErrPlanExpired reports a plan older than PlanLifetime.
	ErrPlanExpired = errors.New("cleanup plan expired")
	// ErrPlanUsed reports a plan that was already applied once.
	ErrPlanUsed = errors.New("cleanup plan was already used")
	// ErrPlanChanged reports that the tool or its folder changed since the plan.
	ErrPlanChanged = errors.New("tool or folder changed since the plan")
	// ErrNotCleanable reports a measure-only target.
	ErrNotCleanable = errors.New("target has no cleanup command")
	// ErrInUse reports that a program using the cache is running.
	ErrInUse = errors.New("a program that uses the cache is running")
	// ErrWrongVersion reports a tool whose version would clean another cache.
	ErrWrongVersion = errors.New("tool version does not match the target")
)

// Runner runs external commands. Tests replace it.
type Runner interface {
	// LookPath resolves a command name to an absolute executable path. It
	// must not resolve to the current directory.
	LookPath(name string) (string, error)
	// Output runs exe and returns its standard output. Like Run, it runs
	// in a fixed directory, not the caller's, so that a project's own
	// settings cannot change the result.
	Output(ctx context.Context, dir, exe string, args []string) (string, error)
	// Run runs exe and returns its combined output for error messages.
	Run(ctx context.Context, dir, exe string, args []string) (string, error)
}

// Engine locates, measures and cleans targets.
type Engine struct {
	Runner Runner
	// Protected returns folders that a cleanup folder must not equal or contain.
	Protected func() []string
	// FreeBytes returns the free space of the volume that holds a path.
	FreeBytes func(path string) (uint64, error)
	// Lock takes the per-user cleanup lock, shared by the GUI and the CLI.
	Lock    func() (unlock func(), err error)
	History *History
	Getenv  func(string) string
	Home    func() (string, error)
	// Running returns which of the named executables are running.
	Running func(names []string) ([]string, error)
	Now     func() time.Time

	mu      sync.Mutex
	applied map[string]bool
}

// Location is where a target's folder is.
type Location struct {
	Target     Target `json:"-"`
	Status     string `json:"status"`
	Dir        string `json:"dir,omitempty"`
	Executable string `json:"-"`
	Error      string `json:"error,omitempty"`
}

// Locate finds the folder of t by asking its tool or reading its variable.
func (e *Engine) Locate(ctx context.Context, t Target) Location {
	loc := Location{Target: t}
	if t.RequireDir != "" {
		if _, err := os.Stat(t.RequireDir); err != nil {
			loc.Status = Missing
			return loc
		}
	}
	if !t.locatedByCommand() {
		dir, err := e.folderFromEnvironment(t)
		if err != nil {
			loc.Status, loc.Error = Failed, err.Error()
			return loc
		}
		loc.Dir = dir
	} else {
		exe, err := e.lookPath(t, t.Locate.Name)
		if err != nil {
			loc.Status = Missing
			return loc
		}
		loc.Executable = exe
		ctx, cancel := context.WithTimeout(ctx, LocateTimeout)
		defer cancel()
		out, err := e.Runner.Output(ctx, t.WorkDir, exe, t.Locate.Args)
		if err != nil && t.MissingIfLocateFails && ctx.Err() == nil {
			loc.Status = Missing
			return loc
		}
		if err != nil {
			loc.Status, loc.Error = Failed, err.Error()
			return loc
		}
		loc.Dir = strings.TrimSpace(out)
		if t.Parse != nil {
			if loc.Dir, err = t.Parse(out); err != nil {
				loc.Status, loc.Error = Failed, err.Error()
				return loc
			}
		}
	}
	if loc.Dir != "" && t.SubDir != "" {
		loc.Dir = filepath.Join(loc.Dir, t.SubDir)
	}
	if loc.Dir == "" || strings.ContainsAny(loc.Dir, "\r\n") || !filepath.IsAbs(loc.Dir) {
		loc.Status, loc.Error = Failed, fmt.Sprintf("unexpected folder %q", loc.Dir)
		loc.Dir = ""
		return loc
	}
	loc.Dir = filepath.Clean(loc.Dir)
	if _, err := os.Stat(loc.Dir); err != nil {
		loc.Status = Missing
		return loc
	}
	loc.Status = Found
	return loc
}

func (e *Engine) folderFromEnvironment(t Target) (string, error) {
	if t.EnvDir != "" {
		if dir := e.Getenv(t.EnvDir); dir != "" {
			return dir, nil
		}
	}
	if t.DefaultDir == "" {
		return "", fmt.Errorf("%s is not set", t.EnvDir)
	}
	var base string
	if t.DefaultBase == "" {
		home, err := e.Home()
		if err != nil {
			return "", err
		}
		base = home
	} else if base = e.Getenv(t.DefaultBase); base == "" {
		return "", fmt.Errorf("%s is not set", t.DefaultBase)
	}
	return filepath.Join(base, t.DefaultDir), nil
}

// Scan is the measurement of one target.
type Scan struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Location          // flattened into the JSON
	Size     *Size    `json:"size,omitempty"`
	Clean    []string `json:"clean_command,omitempty"`
	Settings string   `json:"settings_uri,omitempty"`
	Deletes  bool     `json:"deletes,omitzero"`
}

// Scan locates and measures t. The measurement stops when ctx ends and is
// then marked partial.
func (e *Engine) Scan(ctx context.Context, t Target) Scan {
	result := Scan{ID: t.ID, Name: t.Name, Location: e.Locate(ctx, t), Settings: t.SettingsURI, Deletes: t.Delete != nil}
	if t.Clean != nil {
		result.Clean = append([]string{t.Clean.Name}, t.Clean.Args...)
	}
	if result.Status != Found {
		return result
	}
	size, err := Measure(ctx, result.Dir)
	if err != nil {
		result.Status, result.Error = Failed, err.Error()
		return result
	}
	result.Size = &size
	return result
}

// Plan is a cleanup the user has to confirm. It is valid for PlanLifetime
// and can be applied once.
type Plan struct {
	ID     string
	Target Target
	Dir    string
	// Executable is the cleanup command's executable; empty for a deletion.
	Executable string
	exeInfo    os.FileInfo
	// Scope lists the folders whose contents a deletion removes.
	Scope     []string
	dirInfo   os.FileInfo
	Size      Size
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Deletes reports whether the plan has Skilli remove files itself.
func (p *Plan) Deletes() bool { return p.Target.Delete != nil }

// Command describes what the plan runs or deletes, for display.
func (p *Plan) Command() string {
	if p.Deletes() {
		return "Skilli が中身を直接削除: " + strings.Join(p.Scope, ", ")
	}
	return strings.Join(append([]string{p.Executable}, p.Target.Clean.Args...), " ")
}

// NewPlan locates and measures t and checks that its folder is safe to
// clean: for a cleanup command, that the tool resolves to a fixed
// executable of an accepted version; for a deletion, that no program named
// in t.Busy runs.
func (e *Engine) NewPlan(ctx context.Context, t Target) (*Plan, error) {
	if !t.Cleanable() {
		return nil, ErrNotCleanable
	}
	scan := e.Scan(ctx, t)
	switch {
	case scan.Status == Missing:
		return nil, fmt.Errorf("%s was not found", t.Name)
	case scan.Status != Found:
		return nil, errors.New(scan.Error)
	}
	if err := e.checkFolder(scan.Dir); err != nil {
		return nil, err
	}
	now := e.Now()
	plan := &Plan{
		ID:        uuid.New().String(),
		Target:    t,
		Dir:       scan.Dir,
		Size:      *scan.Size,
		CreatedAt: now,
		ExpiresAt: now.Add(PlanLifetime),
	}
	if err := e.checkBusy(t); err != nil {
		return nil, err
	}
	if t.Delete != nil {
		if err := e.planDeletion(ctx, plan); err != nil {
			return nil, err
		}
		return plan, nil
	}

	exe := scan.Executable
	if !t.locatedByCommand() {
		var err error
		if exe, err = e.lookPath(t, t.Clean.Name); err != nil {
			return nil, fmt.Errorf("%s was not found: %w", t.Clean.Name, err)
		}
	}
	info, err := os.Stat(exe)
	if err != nil {
		return nil, err
	}
	if err := e.checkVersion(ctx, t, exe); err != nil {
		return nil, err
	}
	plan.Executable, plan.exeInfo = exe, pinIdentity(info)
	return plan, nil
}

func (e *Engine) planDeletion(ctx context.Context, p *Plan) error {
	info, err := os.Lstat(p.Dir)
	if err != nil {
		return err
	}
	p.dirInfo = pinIdentity(info)
	p.Scope = deletionScope(p.Dir, p.Target.Delete)
	// The plan shows only what would be deleted, which for Hugging Face
	// is less than the measured folder.
	p.Size = Size{}
	for _, dir := range p.Scope {
		if err := CheckOnDisk(dir); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		size, err := Measure(ctx, dir)
		if err != nil {
			return err
		}
		p.Size.Bytes += size.Bytes
		p.Size.Files += size.Files
		p.Size.Partial = p.Size.Partial || size.Partial
	}
	return nil
}

func deletionScope(dir string, d *Deletion) []string {
	if len(d.Paths) == 0 {
		return []string{dir}
	}
	scope := make([]string, 0, len(d.Paths))
	for _, path := range d.Paths {
		scope = append(scope, filepath.Join(dir, path))
	}
	return scope
}

// Result is the outcome of Apply.
type Result struct {
	SizeBefore Size
	SizeAfter  Size
	FreeBefore uint64
	FreeAfter  uint64
	Output     string
	// Skipped counts what a deletion left in place: files in use or
	// protected, and links, which it never follows or removes.
	Skipped int
}

// Apply cleans up after checking again that the plan is unused and
// unexpired and that the folder is the same, still safe one. A cleanup
// command also needs PATH to resolve the same unchanged executable of an
// accepted version; a deletion needs the folder to be the very directory
// measured and the programs that use it to be closed. Any difference
// refuses the cleanup instead of falling back to another method. Every
// attempt is written to the history.
//
// source names who asked, such as "gui" or "cli".
func (e *Engine) Apply(ctx context.Context, p *Plan, source string) (Result, error) {
	entry := Entry{Time: e.Now(), Source: source, Target: p.Target.ID, Method: MethodCommand, SizeBefore: p.Size.Bytes}
	if p.Deletes() {
		entry.Method = MethodDelete
	}
	result, err := e.apply(ctx, p)
	switch {
	case err == nil:
		entry.Status = StatusSucceeded
	case errors.Is(err, ErrBusy), errors.Is(err, ErrPlanExpired), errors.Is(err, ErrPlanUsed),
		errors.Is(err, ErrPlanChanged), errors.Is(err, ErrUnsafeFolder), errors.Is(err, ErrWrongVersion),
		errors.Is(err, ErrInUse):
		entry.Status = StatusRefused
	default:
		entry.Status = StatusFailed
	}
	if err != nil {
		entry.Error = err.Error()
		if home, herr := e.Home(); herr == nil && home != "" {
			// Paths appear both as they are and quoted with %q, which
			// doubles every backslash.
			entry.Error = replaceFold(entry.Error, strings.ReplaceAll(home, `\`, `\\`), "~")
			entry.Error = replaceFold(entry.Error, home, "~")
		}
	}
	entry.SizeAfter, entry.FreeBefore, entry.FreeAfter, entry.Skipped = result.SizeAfter.Bytes, result.FreeBefore, result.FreeAfter, result.Skipped
	if e.History != nil {
		if herr := e.History.Append(entry); herr != nil && err == nil {
			err = fmt.Errorf("cleanup succeeded but the history could not be written: %w", herr)
		}
	}
	return result, err
}

func (e *Engine) apply(ctx context.Context, p *Plan) (Result, error) {
	result := Result{SizeBefore: p.Size}
	e.mu.Lock()
	if e.applied[p.ID] {
		e.mu.Unlock()
		return result, ErrPlanUsed
	}
	if e.applied == nil {
		e.applied = map[string]bool{}
	}
	e.applied[p.ID] = true
	e.mu.Unlock()

	if !e.Now().Before(p.ExpiresAt) {
		return result, ErrPlanExpired
	}
	unlock, err := e.Lock()
	if err != nil {
		return result, err
	}
	defer unlock()

	if err := e.verify(ctx, p); err != nil {
		return result, err
	}
	if free, err := e.FreeBytes(p.Dir); err == nil {
		result.FreeBefore = free
	}
	runCtx, cancel := context.WithTimeout(ctx, CleanTimeout)
	defer cancel()
	if p.Deletes() {
		for _, dir := range p.Scope {
			if _, statErr := os.Lstat(dir); errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			skipped, delErr := RemoveContents(runCtx, dir)
			result.Skipped += skipped
			if delErr != nil {
				err = delErr
				break
			}
		}
	} else {
		result.Output, err = e.Runner.Run(runCtx, p.Target.WorkDir, p.Executable, p.Target.Clean.Args)
	}
	if err != nil && runCtx.Err() != nil {
		err = fmt.Errorf("the cleanup did not finish within %s: %w", CleanTimeout, err)
	}
	if free, ferr := e.FreeBytes(p.Dir); ferr == nil {
		result.FreeAfter = free
	}
	for _, dir := range measuredAfter(p) {
		if size, merr := Measure(ctx, dir); merr == nil {
			result.SizeAfter.Bytes += size.Bytes
			result.SizeAfter.Files += size.Files
		}
	}
	return result, err
}

func measuredAfter(p *Plan) []string {
	if p.Deletes() {
		return p.Scope
	}
	return []string{p.Dir}
}

func (e *Engine) verify(ctx context.Context, p *Plan) error {
	if err := e.checkBusy(p.Target); err != nil {
		return err
	}
	if p.Deletes() {
		// The folder must be the directory that was measured, not one
		// created in its place at the same path since.
		info, err := os.Lstat(p.Dir)
		if err != nil || !os.SameFile(info, p.dirInfo) {
			return fmt.Errorf("%w: %s was replaced", ErrPlanChanged, p.Dir)
		}
	} else {
		exe, err := e.lookPath(p.Target, p.Target.Clean.Name)
		if err != nil || !samePath(exe, p.Executable) {
			return fmt.Errorf("%w: %s now resolves to %q", ErrPlanChanged, p.Target.Clean.Name, exe)
		}
		info, err := os.Stat(exe)
		if err != nil || !os.SameFile(info, p.exeInfo) || !info.ModTime().Equal(p.exeInfo.ModTime()) || info.Size() != p.exeInfo.Size() {
			return fmt.Errorf("%w: %s was replaced", ErrPlanChanged, exe)
		}
		if err := e.checkVersion(ctx, p.Target, exe); err != nil {
			return err
		}
	}
	loc := e.Locate(ctx, p.Target)
	if loc.Status != Found || !samePath(loc.Dir, p.Dir) {
		return fmt.Errorf("%w: the folder is now %q", ErrPlanChanged, loc.Dir)
	}
	if err := e.checkFolder(p.Dir); err != nil {
		return err
	}
	for _, dir := range p.Scope {
		if err := CheckOnDisk(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// pinIdentity makes info remember the file it describes now. On Windows,
// a FileInfo from Stat or Lstat reads the file's ID only when os.SameFile
// first needs it, by opening its path again; comparing two infos later
// would then compare whatever is at the path at that moment, and a folder
// or executable replaced since the plan would go unnoticed.
func pinIdentity(info os.FileInfo) os.FileInfo {
	os.SameFile(info, info)
	return info
}

// checkBusy refuses a cleanup while a program that uses the cache runs.
func (e *Engine) checkBusy(t Target) error {
	if len(t.Busy) == 0 {
		return nil
	}
	running, err := e.Running(t.Busy)
	if err != nil {
		return fmt.Errorf("%w: could not list running programs: %w", ErrInUse, err)
	}
	if len(running) > 0 {
		return fmt.Errorf("%w: %s", ErrInUse, strings.Join(running, ", "))
	}
	return nil
}

// lookPath resolves name through PATH and then through t.Executable.
func (e *Engine) lookPath(t Target, name string) (string, error) {
	exe, err := e.Runner.LookPath(name)
	if err != nil || t.Executable == nil {
		return exe, err
	}
	return t.Executable(exe)
}

// checkVersion refuses a cleanup whose tool has a major version the
// target does not accept, such as Yarn 1 for the Yarn 2+ cache.
func (e *Engine) checkVersion(ctx context.Context, t Target, exe string) error {
	if t.AcceptMajor == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, LocateTimeout)
	defer cancel()
	out, err := e.Runner.Output(ctx, t.WorkDir, exe, []string{"--version"})
	if err != nil {
		return fmt.Errorf("%w: %s --version failed: %w", ErrWrongVersion, t.Clean.Name, err)
	}
	major, err := majorVersion(out)
	if err != nil || !t.AcceptMajor(major) {
		return fmt.Errorf("%w: %s is version %q", ErrWrongVersion, t.Clean.Name, strings.TrimSpace(out))
	}
	return nil
}

func (e *Engine) checkFolder(dir string) error {
	if err := CheckCleanable(dir, e.Protected()); err != nil {
		return err
	}
	return CheckOnDisk(dir)
}
