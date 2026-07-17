// Package update implements the self-update system: the daily-cached GitHub
// release check, the passive update notice, and the CLI self-update apply
// path (issue #124). It is shared by the sc CLI and the auth-app's version
// card.
package update

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// State is the persisted result of the last release check, stored as JSON in
// the sandcastle config dir. A missing or unreadable file is equivalent to a
// zero State: the check simply runs again.
type State struct {
	CheckedAt   time.Time `json:"checked_at"`
	ETag        string    `json:"etag,omitempty"`
	LatestTag   string    `json:"latest_tag,omitempty"`
	LatestURL   string    `json:"latest_url,omitempty"`
	PublishedAt time.Time `json:"published_at,omitzero"`
	// NoticedAt is when the passive CLI notice was last printed; it throttles
	// the notice to at most once per 24h independently of the check itself.
	NoticedAt time.Time `json:"noticed_at,omitzero"`
}

// LoadState reads the state file. Missing or corrupt files yield a zero
// State without error — the update check is failure-tolerant by design.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, nil
	}
	return st, nil
}

// SaveState writes the state file, creating parent directories as needed.
func SaveState(path string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
