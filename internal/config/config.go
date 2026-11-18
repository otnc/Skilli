// Package config loads and saves Skilli's user settings.
package config

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Limits of Config.LowSpaceBytes.
const (
	MinLowSpaceBytes uint64 = 1 << 30
	MaxLowSpaceBytes uint64 = 1 << 40
)

// Config holds the settings the user can change.
type Config struct {
	// LowSpaceBytes is the free space below which a drive counts as low.
	LowSpaceBytes uint64
	// Notify enables a Windows notification when a drive becomes low.
	Notify bool
	// IgnoredDrives lists drive roots such as `D:\` that are not monitored.
	IgnoredDrives []string
	// CustomTargets are caches the user added, which Skilli measures and
	// cleans like its own.
	CustomTargets []CustomTarget
}

// Cleanup methods of a CustomTarget. Only a command can be unusable (not
// installed), so with both, the command comes first and deletion follows.
const (
	MethodCommand           = "command"
	MethodDelete            = "delete"
	MethodCommandThenDelete = "command-then-delete"
)

// CustomTarget is a cache the user added: a folder, and optionally a
// command that cleans it.
type CustomTarget struct {
	// ID names the target in the CLI, as my-1.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Dir is the folder to measure and, by deletion, to empty.
	Dir string `json:"dir"`
	// Command is the cleanup command: an executable looked up in PATH and
	// its arguments, run without a shell.
	Command []string `json:"command,omitempty"`
	Method  string   `json:"method"`
}

// UsesCommand reports whether the method runs Command.
func (t CustomTarget) UsesCommand() bool { return t.Method != MethodDelete }

// UsesDelete reports whether the method deletes the folder's contents.
func (t CustomTarget) UsesDelete() bool { return t.Method != MethodCommand }

// Validate reports what is missing or wrong in t.
func (t CustomTarget) Validate() error {
	switch {
	case strings.TrimSpace(t.ID) == "":
		return errors.New("custom target has no id")
	case strings.TrimSpace(t.Name) == "":
		return fmt.Errorf("custom target %s has no name", t.ID)
	case !filepath.IsAbs(t.Dir):
		return fmt.Errorf("custom target %s: folder %q is not an absolute path", t.ID, t.Dir)
	case !slices.Contains([]string{MethodCommand, MethodDelete, MethodCommandThenDelete}, t.Method):
		return fmt.Errorf("custom target %s: unknown method %q", t.ID, t.Method)
	case t.UsesCommand() && len(t.Command) == 0:
		return fmt.Errorf("custom target %s: method %s needs a command", t.ID, t.Method)
	}
	return nil
}

// NextCustomID returns an ID no custom target uses yet, as my-3.
func (c Config) NextCustomID() string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("my-%d", n)
		if !slices.ContainsFunc(c.CustomTargets, func(t CustomTarget) bool { return t.ID == id }) {
			return id
		}
	}
}

// Default returns the settings used before the user changes anything.
func Default() Config {
	return Config{LowSpaceBytes: 20 << 30, Notify: true}
}

// Ignores reports whether the drive with the given root is excluded from
// monitoring. Drive letters compare case-insensitively.
func (c Config) Ignores(root string) bool {
	return slices.ContainsFunc(c.IgnoredDrives, func(ignored string) bool {
		return strings.EqualFold(ignored, root)
	})
}

// Validate reports a setting that is out of range.
func (c Config) Validate() error {
	if c.LowSpaceBytes < MinLowSpaceBytes || c.LowSpaceBytes > MaxLowSpaceBytes {
		return fmt.Errorf("low_space_bytes %d is outside %d..%d", c.LowSpaceBytes, MinLowSpaceBytes, MaxLowSpaceBytes)
	}
	seen := map[string]bool{}
	for _, t := range c.CustomTargets {
		if err := t.Validate(); err != nil {
			return err
		}
		if seen[t.ID] {
			return fmt.Errorf("custom target id %s is used twice", t.ID)
		}
		seen[t.ID] = true
	}
	return nil
}

// fileConfig uses pointers so that a key missing from the file keeps its
// default instead of becoming the zero value.
type fileConfig struct {
	SchemaVersion int            `json:"schema_version"`
	LowSpaceBytes *uint64        `json:"low_space_bytes,omitempty"`
	Notify        *bool          `json:"notify,omitempty"`
	IgnoredDrives []string       `json:"ignored_drives,omitempty"`
	CustomTargets []CustomTarget `json:"custom_targets,omitempty"`
}

const schemaVersion = 1

// Path returns %APPDATA%\Skilli\config.json.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Skilli", "config.json"), nil
}

// Load reads the settings at path. A missing file gives Default and no
// error. A broken or invalid file gives Default together with the error,
// so the caller can keep running and tell the user; it must not save over
// the file without the user's change, or the broken settings are lost.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}
	var file fileConfig
	if err := json.Unmarshal(data, &file); err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	if file.SchemaVersion != schemaVersion {
		return Default(), fmt.Errorf("%s: unsupported schema_version %d", path, file.SchemaVersion)
	}
	cfg := Default()
	if file.LowSpaceBytes != nil {
		cfg.LowSpaceBytes = *file.LowSpaceBytes
	}
	if file.Notify != nil {
		cfg.Notify = *file.Notify
	}
	cfg.IgnoredDrives = file.IgnoredDrives
	cfg.CustomTargets = file.CustomTargets
	if err := cfg.Validate(); err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to path through a temporary file in the same directory,
// so that a crash leaves either the old or the new settings.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(fileConfig{
		SchemaVersion: schemaVersion,
		LowSpaceBytes: new(cfg.LowSpaceBytes),
		Notify:        new(cfg.Notify),
		IgnoredDrives: cfg.IgnoredDrives,
		CustomTargets: cfg.CustomTargets,
	})
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
