// Package config resolves Atlas's on-disk locations under the user config
// directory (~/.config/atlas) — the single source of truth for both the API
// client (env file) and the UI (read-state file).
package config

import (
	"os"
	"path/filepath"
)

// Dir returns ~/.config/atlas, or "" when no user config dir resolves.
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "atlas")
}

// EnvFile is the per-user env file: ~/.config/atlas/env (ATLAS_API_URL/KEY).
func EnvFile() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "env")
}

// StateFile is the client-side read-state file: ~/.config/atlas/state.json.
func StateFile() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "state.json")
}
