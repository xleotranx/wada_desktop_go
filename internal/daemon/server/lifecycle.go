package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	PIDFileName         = "wadad.pid"
	CredentialFileName  = "local-credential"
	RemoteTokenFileName = "remote-token"
	PasswordHashFile    = "password.hash"
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

// CleanupRuntimeFiles removes the runtime files this process published, and
// leaves those of any other daemon in place.
func CleanupRuntimeFiles(dataDir string) {
	raw, err := os.ReadFile(filepath.Join(dataDir, PIDFileName))
	if err != nil {
		return
	}
	var rec PIDRecord
	if json.Unmarshal(raw, &rec) != nil || rec.PID != os.Getpid() {
		return
	}
	_ = os.Remove(filepath.Join(dataDir, CredentialFileName))
	_ = os.Remove(filepath.Join(dataDir, PIDFileName))
}

func LoadOrCreateRemoteToken(dataDir string) (string, error) {
	if dataDir == "" {
		return GenerateCredential()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dataDir, RemoteTokenFileName)
	if raw, err := os.ReadFile(p); err == nil {
		if tok := string(raw); len(tok) >= 32 {
			return tok, nil
		}
	}
	tok, err := GenerateCredential()
	if err != nil {
		return "", err
	}
	if err := writeAtomic(p, []byte(tok), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// RotateRemoteToken replaces the remote pairing token, so links and clients
// holding the old one stop working.
func RotateRemoteToken(dataDir string) (string, error) {
	tok, err := GenerateCredential()
	if err != nil {
		return "", err
	}
	if dataDir == "" {
		return tok, nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dataDir, RemoteTokenFileName), []byte(tok), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

func LoadPasswordHash(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, PasswordHashFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return raw, err
}

func SavePasswordHash(dataDir string, hash []byte) error {
	if dataDir == "" {
		return nil
	}
	p := filepath.Join(dataDir, PasswordHashFile)
	if len(hash) == 0 {
		err := os.Remove(p)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return writeAtomic(p, hash, 0o600)
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
