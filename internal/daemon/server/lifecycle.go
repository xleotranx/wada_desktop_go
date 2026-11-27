package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	PIDFileName        = "wadad.pid"
	CredentialFileName = "local-credential"
)

// PIDRecord is published atomically while a wadad worker is listening.
type PIDRecord struct {
	PID            int    `json:"pid"`
	StartedAt      int64  `json:"startedAt"`
	Listen         string `json:"listen"`
	Version        string `json:"version"`
	DesktopManaged bool   `json:"desktopManaged,omitempty"`
}

func GenerateCredential() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func WriteRuntimeFiles(dataDir string, rec PIDRecord, credential string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dataDir, CredentialFileName), []byte(credential), 0o600); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dataDir, PIDFileName), raw, 0o600)
}

func CleanupRuntimeFiles(dataDir string) {
	_ = os.Remove(filepath.Join(dataDir, CredentialFileName))
	_ = os.Remove(filepath.Join(dataDir, PIDFileName))
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
