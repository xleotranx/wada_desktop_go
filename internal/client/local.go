package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	pidFileName        = "wadad.pid"
	credentialFileName = "local-credential"
)

// PIDRecord mirrors wadad's runtime PID file without importing internal/daemon.
type PIDRecord struct {
	PID            int    `json:"pid"`
	StartedAt      int64  `json:"startedAt"`
	Listen         string `json:"listen"`
	Version        string `json:"version"`
	DesktopManaged bool   `json:"desktopManaged,omitempty"`
}

// DefaultDataDir resolves Wada's data directory (WADA_DATA_DIR or OS config dir).
func DefaultDataDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv("WADA_DATA_DIR")); env != "" {
		return filepath.Clean(env), nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "wada-desktop-go"), nil
}

// ReadLocalEndpoint reads wadad.pid and local-credential from dataDir.
func ReadLocalEndpoint(dataDir string) (PIDRecord, string, error) {
	rawPID, err := os.ReadFile(filepath.Join(dataDir, pidFileName))
	if err != nil {
		return PIDRecord{}, "", err
	}
	var rec PIDRecord
	if err := json.Unmarshal(rawPID, &rec); err != nil {
		return PIDRecord{}, "", err
	}
	cred, err := os.ReadFile(filepath.Join(dataDir, credentialFileName))
	if err != nil {
		return PIDRecord{}, "", err
	}
	return rec, strings.TrimSpace(string(cred)), nil
}
