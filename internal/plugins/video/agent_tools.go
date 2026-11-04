package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	kit "wada-desktop-go/internal/contract/videokit"
)

func decodeProjectInput(data json.RawMessage, out any) error {
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("input must contain one object")
	}
	return nil
}

// videoProjectTool is stateless and runs on the backend worker. It never
// discovers conversations, reads app data or accepts a project path from AI.
func videoProjectTool(ctx context.Context, dir, action string, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if action == "inspect" || action == "update" || action == "check" {
		if err := validateVideoToolProject(dir, action); err != nil {
			return "", err
		}
	}
	encode := func(v any) (string, error) { data, err := json.Marshal(v); return string(data), err }
	switch action {
	case "inspect":
		d, err := readDocument(dir)
		if err == nil {
			components := make([]map[string]any, 0, len(d.Snapshot.Components))
			for _, c := range d.Snapshot.Components {
				components = append(components, map[string]any{"id": c.ID, "version": c.Version, "title": c.Title, "description": c.Description, "fields": c.Fields, "layouts": c.Layouts, "defaultDuration": c.DefaultDuration})
			}
			layouts := make([]map[string]any, 0, len(d.Snapshot.Layouts))
			for _, l := range d.Snapshot.Layouts {
				layouts = append(layouts, map[string]any{"id": l.ID, "version": l.Version, "title": l.Title, "width": l.Width, "height": l.Height, "maxParts": l.MaxParts})
			}
			return encode(map[string]any{"kind": "component-video", "title": d.Title, "theme": d.Theme, "scenes": d.Scenes, "overrides": d.Overrides, "components": components, "layouts": layouts, "themes": d.Snapshot.Themes, "next": "Use update with title, scenes, theme and/or overrides. Omitted top-level fields stay unchanged. The app validates and regenerates HTML; do not investigate app internals or other videos."})
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		p, err := openProject(dir)
		if err != nil {
			return "", err
		}
		scenes := make([]string, 0, len(p.Scenes))
		for _, s := range p.Scenes {
			if s.Comp != nil {
				scenes = append(scenes, s.Comp.File)
			}
		}
		return encode(map[string]any{"kind": "html-video", "title": p.Title, "duration": p.Duration(), "root": p.File, "scenes": scenes, "next": "Use read/write for source files, then check. Preview reload and rendering belong to the app."})
	case "update":
		var fields map[string]json.RawMessage
		if err := decodeProjectInput(input, &fields); err != nil {
			return "", err
		}
		if len(fields) == 0 {
			return "", errors.New("update needs title, theme, scenes or overrides")
		}
		for key, value := range fields {
			if key != "overrides" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return "", fmt.Errorf("%s cannot be null", key)
			}
			if key != "title" && key != "theme" && key != "scenes" && key != "overrides" {
				return "", fmt.Errorf("cannot update %q; reusable definitions are preserved", key)
			}
		}
		err := changeDocument(dir, func(d *kit.Document) error {
			data, err := json.Marshal(d)
			if err != nil {
				return err
			}
			var merged map[string]json.RawMessage
			if err := json.Unmarshal(data, &merged); err != nil {
				return err
			}
			for key, value := range fields {
				merged[key] = value
			}
			data, err = json.Marshal(merged)
			if err != nil {
				return err
			}
			if err := decodeProjectInput(data, d); err != nil {
				return err
			}
			return validateVideoToolOutputs(dir, d)
		})
		if err != nil {
			return "", err
		}
		return videoProjectTool(ctx, dir, "check", nil)
	case "read", "write":
		var file struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := decodeProjectInput(input, &file); err != nil {
			return "", err
		}
		path, err := videoToolFile(dir, file.Path)
		if err != nil {
			return "", err
		}
		if action == "read" {
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
				return "", errors.New("read accepts source files up to 2 MiB; use inspect for the component catalog")
			}
			data, err := os.ReadFile(path)
			return string(data), err
		}
		if _, err := os.Stat(filepath.Join(dir, documentName)); err == nil {
			return "", errors.New("use update for this component video; generated files and definitions are owned by the app")
		}
		if len(file.Content) > 2*1024*1024 {
			return "", errors.New("source file exceeds 2 MiB")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := writeFileAtomic(path, []byte(file.Content), 0o644); err != nil {
			return "", err
		}
		return "Saved " + file.Path + ". Use check after completing the composition.", nil
	case "check":
		p, err := openProject(dir)
		if err != nil {
			return "", err
		}
		check := checkProject(p)
		return encode(map[string]any{"title": p.Title, "duration": p.Duration(), "scenes": len(p.Scenes), "errors": check.Errors, "warnings": check.Warnings, "findings": check.Findings, "preview": "The app refreshes the editor from these files after tool completion. Use the editor's Render action for video output."})
	default:
		return "", fmt.Errorf("unknown project action %q", action)
	}
}

func videoToolFile(dir, path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".css", ".js", ".json", ".svg":
	default:
		return "", errors.New("only video source files are available")
	}
	return videoToolPath(dir, path)
}

func videoToolPath(dir, path string) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", errors.New("path must stay inside the active video project")
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") {
			return "", errors.New("hidden paths and traversal are unavailable")
		}
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	// Resolve every existing ancestor, including the target, before a write can
	// create missing directories. A symlink cannot escape the bound project.
	ancestor := full
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			rel, err := filepath.Rel(root, resolved)
			if err != nil || !filepath.IsLocal(rel) {
				return "", errors.New("path leaves the active video project")
			}
			return full, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errors.New("path has no project ancestor")
		}
		ancestor = parent
	}
}

// Validate every source/output path before readers or generation can follow a
// symlink. The model cannot use inspect/update/check to bypass read/write bounds.
func validateVideoToolProject(dir, action string) error {
	if _, err := videoToolFile(dir, documentName); err != nil {
		return err
	}
	d, err := readDocument(dir)
	if err == nil {
		if action == "check" {
			return validateVideoToolOutputs(dir, d)
		}
		// Inspection must expose invalid scenes so update can repair them.
		// Neither operation reads or generates output until the edit validates.
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := videoToolFile(dir, "index.html")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	root, err := parseComposition(dir, "index.html", string(data))
	if err != nil {
		return err
	}
	for _, clip := range root.Clips {
		if clip.Mount != "" {
			if _, err := videoToolFile(dir, clip.Mount); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateVideoToolOutputs(dir string, d *kit.Document) error {
	outputs, err := compileDocument(d)
	if err != nil {
		return err
	}
	for name := range outputs {
		if _, err := videoToolPath(dir, name); err != nil {
			return err
		}
	}
	return nil
}
