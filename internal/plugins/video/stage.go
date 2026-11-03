package video

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// renderDir is the folder the render CLI runs in: the project itself, or a
// copy of it with the user's variable values written onto the scene hosts.
//
// The CLI's --variables reaches a document's own bindings, but a mounted
// scene resolves its text and sources from the host's data-variable-values.
// Writing the values there is what makes the render show what the preview
// shows. The copy links every other file of the project, so assets, scenes
// and libraries stay where the composition reads them.
func renderDir(dir string, values map[string]any) (string, func(), error) {
	noop := func() {}
	if len(values) == 0 {
		return dir, noop, nil
	}
	index := filepath.Join(dir, "index.html")
	data, err := os.ReadFile(index)
	if err != nil {
		return "", noop, err
	}
	staged, err := stageIndex(string(data), values)
	if err != nil {
		return "", noop, err
	}
	tmp, err := os.MkdirTemp("", "wada-video-render-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	entries, err := os.ReadDir(dir)
	if err != nil {
		cleanup()
		return "", noop, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "index.html" || name == "renders" {
			continue
		}
		if err := os.Symlink(filepath.Join(dir, name), filepath.Join(tmp, name)); err != nil {
			cleanup()
			return "", noop, err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "index.html"), []byte(staged), 0o644); err != nil {
		cleanup()
		return "", noop, err
	}
	return tmp, cleanup, nil
}

// stageIndex writes the user's values onto every scene host of a root file.
// A value the host already pins wins, as it does in the platform.
func stageIndex(src string, values map[string]any) (string, error) {
	doc, err := scanHTML(src)
	if err != nil {
		return "", err
	}
	var hosts []*element
	doc.walk(func(n *element) bool {
		if n.value("data-composition-src") != "" {
			hosts = append(hosts, n)
		}
		return true
	})
	if len(hosts) == 0 {
		return src, nil
	}
	var changes []change
	for _, host := range hosts {
		merged := map[string]any{}
		for id, value := range values {
			merged[id] = value
		}
		if raw := strings.TrimSpace(host.value("data-variable-values")); raw != "" {
			var pinned map[string]any
			if err := json.Unmarshal([]byte(raw), &pinned); err == nil {
				for id, value := range pinned {
					merged[id] = value
				}
			}
		}
		if len(merged) == 0 {
			continue
		}
		data, err := json.Marshal(merged)
		if err != nil {
			return "", err
		}
		// Single quotes keep the JSON's own quotes literal; only a quote or
		// an ampersand inside a value needs escaping.
		changes = append(changes, attrChangesQuoted(src, host, map[string]string{
			"data-variable-values": attrEscape(string(data)),
		}, `'`)...)
	}
	if len(changes) == 0 {
		return src, nil
	}
	return applyChanges(src, changes)
}

// attrEscape makes a value safe inside a single-quoted HTML attribute.
func attrEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"'", "&#39;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(s)
}
