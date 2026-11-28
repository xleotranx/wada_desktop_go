package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func projectFolders(ctx context.Context, prefix string) ([]string, error) {
	// On Windows a volume other than a drive letter can name a network
	// share or device namespace. Typing a path must not connect to it.
	if len(prefix) > 4096 || strings.ContainsRune(prefix, 0) ||
		!filepath.IsAbs(prefix) || len(filepath.VolumeName(prefix)) > 2 {
		return nil, errors.New("enter an absolute folder path on the host")
	}
	parent, name := filepath.Split(prefix)
	folders := []string{}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return folders, nil
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return folders, nil
	}
	defer dir.Close()

	const maxScanned = 8192
	const batchSize = 256
	for scanned := 0; scanned < maxScanned; scanned += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := dir.ReadDir(batchSize)
		for _, entry := range entries {
			label := entry.Name()
			if entry.IsDir() && strings.HasPrefix(label, name) && label != name &&
				(!strings.HasPrefix(label, ".") || strings.HasPrefix(name, ".")) {
				folders = append(folders, filepath.Join(parent, label))
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return []string{}, nil
			}
			break
		}
	}
	slices.Sort(folders)
	return folders[:min(len(folders), 8)], nil
}
