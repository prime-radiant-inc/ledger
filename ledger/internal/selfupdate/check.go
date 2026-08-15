package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The passive update check runs at most once a day, remembered in a small
// state file under the user cache dir. Every operation here is best-effort:
// a broken or unwritable state file must never surface as a command error.

const stateFile = "update-check.json"

const CheckInterval = 24 * time.Hour

type State struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// StateDir is where the check state lives; empty if the platform offers no
// user cache dir (then every load is a zero state and saves go nowhere).
func StateDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "ledger")
}

// LoadState reads the check state; any failure reads as the zero state.
func LoadState(dir string) State {
	var s State
	if dir == "" {
		return s
	}
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return State{}
	}
	if json.Unmarshal(data, &s) != nil {
		return State{}
	}
	return s
}

// SaveState writes the check state, best-effort.
func SaveState(dir string, s State) {
	if dir == "" {
		return
	}
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, stateFile), data, 0o644)
}

// Due reports whether a fresh network check is warranted.
func Due(s State, now time.Time) bool {
	return now.Sub(s.CheckedAt) >= CheckInterval
}
