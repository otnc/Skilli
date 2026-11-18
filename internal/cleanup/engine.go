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
)

// Runner runs external commands. Tests replace it.
type Runner interface {
	// LookPath resolves a command name to an absolute executable path. It
	// must not resolve to the current directory.
	LookPath(name string) (string, error)
	// Output runs exe and returns its standard output.
	Output(ctx context.Context, exe string, args []string) (string, error)
	// Run runs exe and returns its combined output for error messages.
	Run(ctx context.Context, exe string, args []string) (string, error)
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
	if t.EnvDir != "" {
		loc.Dir = e.Getenv(t.EnvDir)
	} else {
		exe, err := e.Runner.LookPath(t.Locate.Name)
		if err != nil {
			loc.Status = Missing
			return loc
		}
		loc.Executable = exe
		ctx, cancel := context.WithTimeout(ctx, LocateTimeout)
		defer cancel()
		out, err := e.Runner.Output(ctx, exe, t.Locate.Args)
		if err != nil {
			loc.Status, loc.Error = Failed, err.Error()
			return loc
		}
		loc.Dir = strings.TrimSpace(out)
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

// Scan is the measurement of one target.
type Scan struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Location          // flattened into the JSON
	Size     *Size    `json:"size,omitempty"`
	Clean    []string `json:"clean_command,omitempty"`
	Settings string   `json:"settings_uri,omitempty"`
}

// Scan locates and measures t. The measurement stops when ctx ends and is
// then marked partial.
func (e *Engine) Scan(ctx context.Context, t Target) Scan {
	result := Scan{ID: t.ID, Name: t.Name, Location: e.Locate(ctx, t), Settings: t.SettingsURI}
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
	ID         string
	Target     Target
	Dir        string
	Executable string
	exeInfo    os.FileInfo
	Size       Size
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// Command returns the command line the plan runs, for display.
func (p *Plan) Command() string {
	return strings.Join(append([]string{p.Executable}, p.Target.Clean.Args...), " ")
}

// NewPlan locates and measures t and checks that its folder is safe to
// hand to the tool's cleanup command.
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
	info, err := os.Stat(scan.Executable)
	if err != nil {
		return nil, err
	}
	now := e.Now()
	return &Plan{
		ID:         uuid.New().String(),
		Target:     t,
		Dir:        scan.Dir,
		Executable: scan.Executable,
		exeInfo:    info,
		Size:       *scan.Size,
		CreatedAt:  now,
		ExpiresAt:  now.Add(PlanLifetime),
	}, nil
}

// Result is the outcome of Apply.
type Result struct {
	SizeBefore Size
	SizeAfter  Size
	FreeBefore uint64
	FreeAfter  uint64
	Output     string
}

// Apply runs the plan's cleanup command after checking again that the
// plan is unused and unexpired, that PATH resolves the same unchanged
// executable, that the tool reports the same folder, and that the folder
// is still safe. Any difference refuses the cleanup instead of falling
// back to another method. Every attempt is written to the history.
//
// source names who asked, such as "gui" or "cli".
func (e *Engine) Apply(ctx context.Context, p *Plan, source string) (Result, error) {
	entry := Entry{Time: e.Now(), Source: source, Target: p.Target.ID, SizeBefore: p.Size.Bytes}
	result, err := e.apply(ctx, p)
	switch {
	case err == nil:
		entry.Status = StatusSucceeded
	case errors.Is(err, ErrBusy), errors.Is(err, ErrPlanExpired), errors.Is(err, ErrPlanUsed),
		errors.Is(err, ErrPlanChanged), errors.Is(err, ErrUnsafeFolder):
		entry.Status = StatusRefused
	default:
		entry.Status = StatusFailed
	}
	if err != nil {
		entry.Error = err.Error()
	}
	entry.SizeAfter, entry.FreeBefore, entry.FreeAfter = result.SizeAfter.Bytes, result.FreeBefore, result.FreeAfter
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
	result.Output, err = e.Runner.Run(runCtx, p.Executable, p.Target.Clean.Args)
	if err != nil {
		if runCtx.Err() != nil {
			err = fmt.Errorf("%s did not finish within %s: %w", p.Target.Clean.Name, CleanTimeout, err)
		}
		return result, err
	}
	if free, err := e.FreeBytes(p.Dir); err == nil {
		result.FreeAfter = free
	}
	if size, err := Measure(ctx, p.Dir); err == nil {
		result.SizeAfter = size
	}
	return result, nil
}

func (e *Engine) verify(ctx context.Context, p *Plan) error {
	exe, err := e.Runner.LookPath(p.Target.Clean.Name)
	if err != nil || !samePath(exe, p.Executable) {
		return fmt.Errorf("%w: %s now resolves to %q", ErrPlanChanged, p.Target.Clean.Name, exe)
	}
	info, err := os.Stat(exe)
	if err != nil || !os.SameFile(info, p.exeInfo) || !info.ModTime().Equal(p.exeInfo.ModTime()) || info.Size() != p.exeInfo.Size() {
		return fmt.Errorf("%w: %s was replaced", ErrPlanChanged, exe)
	}
	loc := e.Locate(ctx, p.Target)
	if loc.Status != Found || !samePath(loc.Dir, p.Dir) {
		return fmt.Errorf("%w: the folder is now %q", ErrPlanChanged, loc.Dir)
	}
	return e.checkFolder(p.Dir)
}

func (e *Engine) checkFolder(dir string) error {
	if err := CheckCleanable(dir, e.Protected()); err != nil {
		return err
	}
	return CheckOnDisk(dir)
}
