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
	return nil
}

// fileConfig uses pointers so that a key missing from the file keeps its
// default instead of becoming the zero value.
type fileConfig struct {
	SchemaVersion int      `json:"schema_version"`
	LowSpaceBytes *uint64  `json:"low_space_bytes,omitempty"`
	Notify        *bool    `json:"notify,omitempty"`
	IgnoredDrives []string `json:"ignored_drives,omitempty"`
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
