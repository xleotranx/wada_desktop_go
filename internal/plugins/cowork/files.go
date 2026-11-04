package cowork

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"wada-desktop-go/internal/contract/documents"
)

const maxFileBytes = 8 << 20
const maxOutput = 24000

type request struct {
	Expression string `json:"expression"`
	Path       string `json:"path"`
	Query      string `json:"query"`
	Content    string `json:"content"`
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	URL        string `json:"url"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
}

type readerResolver func(context.Context) (documents.Reader, error)

func workspaceOperation(ctx context.Context, dir, protected string, reader readerResolver, action string, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var req request
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return "", err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return "", errors.New("provide one tool input object")
	}
	if req.Offset < 0 || req.Limit < 0 {
		return "", errors.New("offset and limit must be nonnegative")
	}
	if action == "calculate" {
		return calculate(req.Expression)
	}
	if action == "fetch_url" {
		return readURL(ctx, req)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	path, err := workPath(dir, protected, req.Path)
	if err != nil {
		return "", err
	}
	encode := func(value any) (string, error) { data, err := json.Marshal(value); return string(data), err }
	switch action {
	case "list":
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", err
		}
		type item struct {
			Path   string `json:"path"`
			Folder bool   `json:"folder"`
			Bytes  int64  `json:"bytes,omitempty"`
		}
		items := []item{}
		for _, entry := range entries {
			rel, err := filepath.Rel(dir, filepath.Join(path, entry.Name()))
			if err != nil {
				continue
			}
			full, err := workPath(dir, protected, filepath.ToSlash(rel))
			if err != nil {
				continue
			}
			info, err := os.Stat(full)
			if err != nil {
				continue
			}
			items = append(items, item{filepath.ToSlash(rel), info.IsDir(), info.Size()})
		}
		start := min(req.Offset, len(items))
		limit := req.Limit
		if limit == 0 {
			limit = 100
		}
		limit = min(limit, 200)
		end := min(start+limit, len(items))
		return encode(map[string]any{"files": items[start:end], "total": len(items), "next_offset": end, "more": end < len(items)})
	case "read":
		text, err := readWorkFile(ctx, path, reader)
		if err != nil {
			return "", err
		}
		return pageText(req, text)
	case "search":
		if strings.TrimSpace(req.Query) == "" {
			return "", errors.New("search needs a nonempty literal query")
		}
		results := []map[string]any{}
		scanned := 0
		truncated := false
		err := filepath.WalkDir(path, func(file string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(dir, file)
			if err != nil {
				return err
			}
			full, err := workPath(dir, protected, filepath.ToSlash(rel))
			if err != nil {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if scanned >= 200 || len(results) >= 40 {
				truncated = true
				return filepath.SkipAll
			}
			if !readableExtension(full) {
				return nil
			}
			scanned++
			text, err := readWorkFile(ctx, full, reader)
			if err != nil {
				return nil
			}
			for line, value := range strings.Split(text, "\n") {
				if strings.Contains(strings.ToLower(value), strings.ToLower(req.Query)) {
					excerpt := []rune(value)
					if len(excerpt) > 400 {
						excerpt = excerpt[:400]
					}
					results = append(results, map[string]any{"path": filepath.ToSlash(rel), "line": line + 1, "excerpt": string(excerpt)})
					if len(results) >= 40 {
						truncated = true
						break
					}
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		return encode(map[string]any{"matches": results, "files_read": scanned, "truncated": truncated})
	case "write", "update":
		if !writableExtension(path) {
			return "", errors.New("write text/Markdown, CSV/TSV, HTML, JSON, SVG, CSS or JavaScript; native Office and PDF export needs a dedicated exporter")
		}
		content := req.Content
		if action == "update" {
			if req.OldText == "" {
				return "", errors.New("edit needs nonempty old_text")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			if len(data) > maxFileBytes {
				return "", errors.New("file exceeds the 8 MiB edit limit")
			}
			if strings.Count(string(data), req.OldText) != 1 {
				return "", errors.New("old_text must match exactly once; read the current file and choose a unique passage")
			}
			content = strings.Replace(string(data), req.OldText, req.NewText, 1)
		}
		if len(content) > maxFileBytes || !utf8.ValidString(content) {
			return "", errors.New("deliverable must be UTF-8 text up to 8 MiB")
		}
		if err := atomicWorkFile(ctx, path, []byte(content)); err != nil {
			return "", err
		}
		return encode(map[string]any{"saved": req.Path, "bytes": len(content)})
	default:
		return "", fmt.Errorf("unsupported workspace action %q", action)
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func workPath(dir, protected, path string) (string, error) {
	if path == "" {
		path = "."
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", errors.New("use a path relative to the selected work folder")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == "." {
			continue
		}
		if strings.HasPrefix(part, ".") || part == "node_modules" || part == "vendor" {
			return "", errors.New("app internals, hidden files and dependency folders are unavailable")
		}
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, clean)
	ancestor := full
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			if !within(root, resolved) {
				return "", errors.New("path leaves the selected work folder")
			}
			if protected != "" {
				safe, err := filepath.EvalSymlinks(protected)
				if err != nil {
					safe = filepath.Clean(protected)
				}
				if within(safe, resolved) && !within(filepath.Join(safe, "workspaces"), resolved) {
					return "", errors.New("app data and conversation history are unavailable to work tools")
				}
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		missing = append(missing, filepath.Base(ancestor))
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errors.New("work folder is unavailable")
		}
		ancestor = parent
	}
}

func writableExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".html", ".htm", ".json", ".svg", ".css", ".js":
		return true
	default:
		return false
	}
}
func readableExtension(path string) bool {
	if writableExtension(path) {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".doc", ".docx", ".docm", ".ppt", ".pptx", ".pptm", ".pps", ".ppsx", ".ppsm", ".xls", ".xlsx", ".xlsm", ".xlsb", ".pdf", ".odt", ".ods", ".odp", ".rtf", ".epub":
		return true
	default:
		return false
	}
}
func readWorkFile(ctx context.Context, path string, reader readerResolver) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return "", errors.New("read a regular work document up to 8 MiB")
	}
	if !readableExtension(path) {
		return "", errors.New("this file type is not a supported work document")
	}
	if writableExtension(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return "", errors.New("this file contains binary data")
		}
		return string(data), nil
	}
	if reader == nil {
		return "", errors.New("the document reader is unavailable")
	}
	r, err := reader(ctx)
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", errors.New("the document reader is unavailable")
	}
	source, err := r.Read(ctx, path)
	if err != nil {
		return "", err
	}
	if source.Skip != "" {
		return "", errors.New(source.Skip)
	}
	return source.Markdown, nil
}
func pageText(req request, text string) (string, error) {
	runes := []rune(text)
	start := min(req.Offset, len(runes))
	limit := req.Limit
	if limit == 0 {
		limit = 12000
	}
	limit = min(limit, maxOutput)
	end := min(start+limit, len(runes))
	data, err := json.Marshal(map[string]any{"path": req.Path, "url": req.URL, "text": string(runes[start:end]), "total_characters": len(runes), "next_offset": end, "more": end < len(runes)})
	return string(data), err
}
func atomicWorkFile(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("output must be a regular file")
		}
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cowork-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
