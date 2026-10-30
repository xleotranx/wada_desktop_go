package pig

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"wada-desktop-go/internal/contract/agent"
)

type fileTypes struct{}

func (fileTypes) Accepts(mime string) bool {
	return strings.HasPrefix(mime, "text/") || slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, mime)
}
func (fileTypes) Summary() string { return "images and text files" }
func directory(dir string) (string, error) {
	if dir == "" {
		var err error
		dir, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.EvalSymlinks(dir)
}
func contained(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", errors.New("the path must stay inside the conversation folder")
	}
	root, err := directory(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	resolved, err := filepath.EvalSymlinks(full)
	if errors.Is(err, os.ErrNotExist) {
		// A write can create several missing parent directories. Resolve the nearest
		// existing ancestor before checking that the final path stays in the project.
		ancestor := full
		var suffix []string
		for errors.Is(err, os.ErrNotExist) {
			suffix = append(suffix, filepath.Base(ancestor))
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				break
			}
			ancestor = parent
			resolved, err = filepath.EvalSymlinks(ancestor)
		}
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
		}
	}
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("the path leaves the conversation folder")
	}
	return resolved, nil
}
func projectFiles(ctx context.Context, dir string) ([]string, error) {
	// Git supplies the same project ignore semantics for mentions and snapshots.
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if data, err := cmd.Output(); err == nil {
		files := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
		files = slices.DeleteFunc(files, func(s string) bool { return s == "" })
		slices.Sort(files)
		return slices.Compact(files), nil
	}
	var paths []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
			if len(paths) > 25000 {
				return errors.New("the folder contains more than 25,000 files")
			}
		}
		return nil
	})
	return paths, err
}
func (p *Plugin) Find(ctx context.Context, dir, query string, limit int) ([]agent.FileEntry, error) {
	if _, err := p.current(); err != nil {
		return nil, err
	}
	root, err := directory(dir)
	if err != nil {
		return nil, err
	}
	files, err := projectFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	query = strings.ToLower(query)
	seen := map[string]bool{}
	out := []agent.FileEntry{}
	for _, file := range files {
		paths := []agent.FileEntry{{Path: file}}
		for parent := filepath.ToSlash(filepath.Dir(file)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			paths = append(paths, agent.FileEntry{Path: parent, IsDir: true})
		}
		for _, entry := range paths {
			if !seen[entry.Path] && strings.Contains(strings.ToLower(entry.Path), query) {
				if _, err = contained(root, entry.Path); err != nil {
					continue
				}
				seen[entry.Path] = true
				out = append(out, entry)
			}
		}
	}
	slices.SortFunc(out, func(a, b agent.FileEntry) int { return strings.Compare(a.Path, b.Path) })
	return out[:min(limit, len(out))], nil
}
func (p *Plugin) Read(ctx context.Context, dir, path string) ([]byte, error) {
	if _, err := p.current(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := contained(dir, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, errors.New("the file is not a regular file smaller than 8 MiB")
	}
	return os.ReadFile(full)
}
func (p *Plugin) Skills(ctx context.Context, dir string) ([]agent.Skill, error) {
	e, release, err := p.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	root, err := directory(dir)
	if err != nil {
		return nil, err
	}
	svcs, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: root, AgentDir: filepath.Join(e.root, "agent"), ModelRuntime: e.models})
	if err != nil {
		return nil, err
	}
	defer svcs.Close()
	var out []agent.Skill
	loader := coding.NewDefaultResourceLoader(coding.DefaultResourceLoaderOptions{CWD: root, AgentDir: filepath.Join(e.root, "agent"), SettingsManager: svcs.SettingsManager()})
	defer loader.Close()
	if err = loader.ReloadWith(ctx); err != nil {
		return nil, err
	}
	for _, skill := range loader.GetSkills().Skills {
		out = append(out, agent.Skill{ID: skill.FilePath, Name: skill.Name, Description: skill.Description})
	}
	return out, nil
}
