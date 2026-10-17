package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workdir is the app's own directory, the one the agent falls back to
// when a chat does not name its own.
func workdir() string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	return dir
}

// expandPath turns a typed folder into the path a session can sit in:
// ~ grows into the home directory and the path must exist.
func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("name a folder first")
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = home + strings.TrimPrefix(path, "~")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if st, serr := os.Stat(abs); serr != nil || !st.IsDir() {
		return "", fmt.Errorf("that folder does not exist")
	}
	return filepath.EvalSymlinks(abs)
}

// canonDir is one folder path as the app matches it: cleaned, with
// symlinks resolved where the folder still exists. Sessions and projects
// both pass through it, so a chat lines up with its folder however the
// path was spelled.
func canonDir(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

// expandFile resolves a file path the user typed, through ~ to the
// absolute file, cut at the size the draft can quote.
func expandFile(path string) (string, []byte, error) {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
		path = home + strings.TrimPrefix(path, "~")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", nil, err
	}
	st, serr := os.Stat(abs)
	if serr != nil {
		return "", nil, fmt.Errorf("that file is not there")
	}
	if st.IsDir() {
		return "", nil, fmt.Errorf("that is a folder, not a file")
	}
	if st.Size() > 256*1024 {
		return "", nil, fmt.Errorf("that file is too large to quote")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", nil, err
	}
	return abs, data, nil
}

// codeLang is the tag a file's fence carries, from its ending.
func codeLang(path string) string {
	dot := strings.LastIndex(path, ".")
	if dot < 0 {
		return ""
	}
	switch strings.TrimPrefix(path[dot:], ".") {
	case "go":
		return "go"
	case "json":
		return "json"
	case "ts", "tsx":
		return "ts"
	case "js", "mjs", "cjs":
		return "js"
	case "sh", "zsh", "bash":
		return "sh"
	case "py":
		return "py"
	case "md":
		return "md"
	case "rs":
		return "rs"
	case "toml", "yaml", "yml":
		return "yaml"
	}
	return ""
}

// dirBase is a directory's name for showing, its last segment.
func dirBase(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}
