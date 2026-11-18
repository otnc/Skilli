package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileGivesDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LowSpaceBytes != 20<<30 || !cfg.Notify {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Config{LowSpaceBytes: 5 << 30, Notify: false, IgnoredDrives: []string{`D:\`}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.LowSpaceBytes != want.LowSpaceBytes || got.Notify || !got.Ignores(`d:\`) {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadKeepsDefaultsForMissingKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"low_space_bytes":2147483648}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LowSpaceBytes != 2<<30 || !cfg.Notify {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadRejectsBrokenOrOutOfRangeFiles(t *testing.T) {
	for name, content := range map[string]string{
		"broken JSON":    `{"schema_version":`,
		"unknown schema": `{"schema_version":99}`,
		"too small":      `{"schema_version":1,"low_space_bytes":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err == nil {
				t.Fatal("want an error")
			}
			if cfg.LowSpaceBytes != Default().LowSpaceBytes {
				t.Fatalf("broken file must fall back to Default, got %+v", cfg)
			}
		})
	}
}

func TestSaveRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, Config{LowSpaceBytes: 0}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid config must not be written: %v", err)
	}
}

func TestCustomTargetsRoundTripAndValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.CustomTargets = []CustomTarget{
		{ID: "my-1", Name: "tool cache", Dir: `C:\tool\cache`, Command: []string{"tool", "clean"}, Method: MethodCommandThenDelete},
		{ID: "my-2", Name: "other", Dir: `D:\other`, Method: MethodDelete},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || len(got.CustomTargets) != 2 || got.CustomTargets[0].Command[1] != "clean" {
		t.Fatalf("got %+v, %v", got.CustomTargets, err)
	}
	if id := got.NextCustomID(); id != "my-3" {
		t.Fatalf("NextCustomID = %s", id)
	}

	for name, bad := range map[string]CustomTarget{
		"relative folder":        {ID: "my-1", Name: "x", Dir: `cache`, Method: MethodDelete},
		"no name":                {ID: "my-1", Dir: `C:\x`, Method: MethodDelete},
		"command method, no cmd": {ID: "my-1", Name: "x", Dir: `C:\x`, Method: MethodCommand},
		"unknown method":         {ID: "my-1", Name: "x", Dir: `C:\x`, Method: "delete-then-command"},
	} {
		c := Default()
		c.CustomTargets = []CustomTarget{bad}
		if c.Validate() == nil {
			t.Errorf("%s must be invalid", name)
		}
	}
	dup := Default()
	dup.CustomTargets = []CustomTarget{cfg.CustomTargets[1], cfg.CustomTargets[1]}
	if dup.Validate() == nil {
		t.Error("duplicate IDs must be invalid")
	}
}
