package worktree

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// TrustStore persists user approval for repository setup scripts keyed by repo root.
type TrustStore struct {
	path string
	mu   sync.Mutex
}

// NewTrustStore opens or prepares trust records under dataDir/worktrees/trust.json.
func NewTrustStore(dataDir string) *TrustStore {
	return &TrustStore{
		path: filepath.Join(filepath.Clean(dataDir), "worktrees", "trust.json"),
	}
}

func (s *TrustStore) IsTrusted(repoRoot, scriptHash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return false, err
	}
	got, ok := recs[filepath.Clean(repoRoot)]
	return ok && got == scriptHash, nil
}

func (s *TrustStore) Trust(repoRoot, scriptHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return err
	}
	recs[filepath.Clean(repoRoot)] = scriptHash
	return s.save(recs)
}

func (s *TrustStore) Revoke(repoRoot string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return err
	}
	delete(recs, filepath.Clean(repoRoot))
	return s.save(recs)
}

func (s *TrustStore) load() (map[string]string, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func (s *TrustStore) save(recs map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".trust-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o600); err == nil {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	return os.Rename(tmp, s.path)
}
