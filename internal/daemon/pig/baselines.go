package pig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func (e *engine) baselinePath(id string) string {
	return filepath.Join(e.root, "baselines", id+".json")
}

func (e *engine) loadBaselines(id string) (map[string]fileSnapshot, error) {
	data, err := os.ReadFile(e.baselinePath(id))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]fileSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	var base map[string]fileSnapshot
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, err
	}
	if base == nil {
		base = map[string]fileSnapshot{}
	}
	return base, nil
}

func (e *engine) saveBaselines(id string, base map[string]fileSnapshot) error {
	if len(base) == 0 {
		return nil
	}
	name := e.baselinePath(id)
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(base)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".baselines-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, name)
}

func (e *engine) copyBaselines(from, to string) error {
	base, err := e.loadBaselines(from)
	if err != nil || len(base) == 0 {
		return err
	}
	return e.saveBaselines(to, base)
}

func (e *engine) removeBaselines(id string) {
	_ = os.Remove(e.baselinePath(id))
}
