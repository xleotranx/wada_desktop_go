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
	"wada-desktop-go/internal/protocol"
)

func acceptsMIME(mime string) bool {
	return strings.HasPrefix(mime, "text/") || slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, mime)
}

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

// maxWalkedFiles bounds the walk of a folder outside Git.
const maxWalkedFiles = 25000

var errTooManyFiles = errors.New("the folder contains more than 25,000 files")

// projectFiles lists dir's files: Git's tracked and unignored files, or else
// the first maxWalkedFiles regular files of a walk, with truncated set when
// the walk stopped there.
func projectFiles(ctx context.Context, dir string) (files []string, truncated bool, err error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if data, err := cmd.Output(); err == nil {
		files := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
		files = slices.DeleteFunc(files, func(s string) bool { return s == "" })
		slices.Sort(files)
		return slices.Compact(files), false, nil
	}
	var paths []string
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
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
			if len(paths) == maxWalkedFiles {
				truncated = true
				return filepath.SkipAll
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	return paths, truncated, err
}

func (r *Runtime) FindFiles(ctx context.Context, dir, query string, limit int) ([]protocol.FileEntry, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	root, err := e.workDirectory(dir)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	// A folder too large to walk whole still offers the files it walked.
	files, _, err := projectFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	seen := map[string]bool{}
	var matches []protocol.FileEntry
	for _, file := range files {
		paths := []protocol.FileEntry{{Path: file}}
		for parent := filepath.ToSlash(filepath.Dir(file)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			paths = append(paths, protocol.FileEntry{Path: parent, IsDir: true})
		}
		for _, entry := range paths {
			if !seen[entry.Path] && strings.Contains(strings.ToLower(entry.Path), query) {
				seen[entry.Path] = true
				matches = append(matches, entry)
			}
		}
	}
	slices.SortFunc(matches, func(a, b protocol.FileEntry) int { return strings.Compare(a.Path, b.Path) })
	// Resolving symlinks costs a stat per path element, so only the entries
	// returned are checked.
	out := []protocol.FileEntry{}
	for _, entry := range matches {
		if len(out) == limit {
			break
		}
		if _, err := contained(root, entry.Path); err == nil {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (r *Runtime) ReadFile(ctx context.Context, dir, path string) ([]byte, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err = e.workDirectory(dir)
	if err != nil {
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

func (r *Runtime) Skills(ctx context.Context, dir string) ([]protocol.Skill, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	root, err := e.workDirectory(dir)
	if err != nil {
		return nil, err
	}
	svcs, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: root, AgentDir: filepath.Join(e.root, "agent"), ModelRuntime: e.models, ProjectTrusted: new(false)})
	if err != nil {
		return nil, err
	}
	defer svcs.Close()
	var out []protocol.Skill
	loader := coding.NewDefaultResourceLoader(coding.DefaultResourceLoaderOptions{CWD: root, AgentDir: filepath.Join(e.root, "agent"), SettingsManager: svcs.SettingsManager(), NoContextFiles: true, NoExtensions: true, NoThemes: true, NoPromptTemplates: true, NoSkills: true, AdditionalSkillPaths: []string{filepath.Join(root, ".agents", "skills"), filepath.Join(e.root, "agent", "skills")}})
	defer loader.Close()
	if err = loader.ReloadWith(ctx); err != nil {
		return nil, err
	}
	for _, skill := range loader.GetSkills().Skills {
		out = append(out, protocol.Skill{ID: skill.FilePath, Name: skill.Name, Description: skill.Description})
	}
	return out, nil
}

func (e *engine) workDirectory(dir string) (string, error) {
	if dir == "" {
		dir = filepath.Join(filepath.Dir(e.root), "workspaces", "inbox")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	return directory(dir)
}
