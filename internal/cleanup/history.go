package cleanup

import (
	"bufio"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Status values of an Entry.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusRefused   = "refused"
)

// Method values of an Entry.
const (
	MethodCommand = "command"
	MethodDelete  = "delete"
)

// Entry records one cleanup attempt. It holds the target ID and sizes, and
// in Error the home folder is written as ~, so that the history can be
// shared without the user name.
type Entry struct {
	Time       time.Time `json:"time"`
	Source     string    `json:"source"`
	Target     string    `json:"target"`
	Method     string    `json:"method,omitempty"`
	Status     string    `json:"status"`
	SizeBefore uint64    `json:"size_before_bytes,omitzero"`
	SizeAfter  uint64    `json:"size_after_bytes,omitzero"`
	FreeBefore uint64    `json:"drive_free_before_bytes,omitzero"`
	FreeAfter  uint64    `json:"drive_free_after_bytes,omitzero"`
	Skipped    int       `json:"skipped,omitzero"`
	Error      string    `json:"error,omitempty"`
}

// Retention is how long history entries are kept.
const Retention = 90 * 24 * time.Hour

// pruneThreshold keeps Append cheap: the file is rewritten without old
// entries only once it grows past this size.
const pruneThreshold = 1 << 20

// History is a JSON Lines file of cleanup attempts.
type History struct {
	Path string
}

// HistoryPath returns %LOCALAPPDATA%\Skilli\history.jsonl.
func HistoryPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Skilli", "history.jsonl"), nil
}

// Append adds e to the end of the file.
func (h History) Append(e Entry) error {
	if err := os.MkdirAll(filepath.Dir(h.Path), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(h.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(line, '\n'))
	info, statErr := file.Stat()
	if err := errors.Join(writeErr, statErr, file.Close()); err != nil {
		return err
	}
	if info.Size() > pruneThreshold {
		return h.prune(e.Time.Add(-Retention))
	}
	return nil
}

// Read returns the entries at or after since, oldest first. Lines that do
// not parse are skipped so that one damaged line does not hide the rest.
func (h History) Read(since time.Time) ([]Entry, error) {
	file, err := os.Open(h.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var entries []Entry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var e Entry
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Time.Before(since) {
			continue
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

func (h History) prune(before time.Time) error {
	entries, err := h.Read(before)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(h.Path), "history-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	writer := bufio.NewWriter(temp)
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			temp.Close()
			return err
		}
		writer.Write(append(line, '\n'))
	}
	if err := errors.Join(writer.Flush(), temp.Close()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), h.Path)
}
