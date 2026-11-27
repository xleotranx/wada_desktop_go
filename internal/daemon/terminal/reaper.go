package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	workerMetaFile = "worker.json"
	workerVersion  = "0.1.0-dev"
)

// WorkerRecord records a running detached wadad-term IPC endpoint and secret.
type WorkerRecord struct {
	PID       int    `json:"pid"`
	Endpoint  string `json:"endpoint"`
	Secret    string `json:"secret"`
	Version   string `json:"version"`
	StartedAt int64  `json:"startedAt"`
}

func workerDir(dataDir string) string {
	return filepath.Join(filepath.Clean(dataDir), "terminal-worker")
}

func readWorkerRecord(dataDir string) (WorkerRecord, error) {
	raw, err := os.ReadFile(filepath.Join(workerDir(dataDir), workerMetaFile))
	if err != nil {
		return WorkerRecord{}, err
	}
	var rec WorkerRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return WorkerRecord{}, err
	}
	return rec, nil
}

func writeWorkerRecord(dataDir string, rec WorkerRecord) error {
	dir := workerDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, workerMetaFile), data, 0o600)
}

func removeWorkerRecord(dataDir string) {
	_ = os.Remove(filepath.Join(workerDir(dataDir), workerMetaFile))
}

func writeClosingMarker(dataDir, termID string) {
	dir := workerDir(dataDir)
	_ = os.MkdirAll(dir, 0o700)
	_ = writeAtomic(filepath.Join(dir, "closing-"+termID+".json"), []byte("{}"), 0o600)
}

func removeClosingMarker(dataDir, termID string) {
	_ = os.Remove(filepath.Join(workerDir(dataDir), "closing-"+termID+".json"))
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
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	return os.Rename(tmp, path)
}
