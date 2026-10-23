package filechanges

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
)

const gitOutputLimit = 256 << 10

// gitChanges reads the whole local repository containing directory. Staged and
// unstaged patches stay separate so opposing edits never hide one another.
func gitChanges(ctx context.Context, directory string) ([]agent.FileChange, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("choose a local project folder to see Git changes")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	root, _, err := readGit(ctx, directory, "rev-parse", "--show-toplevel")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("git isn’t installed on this Mac")
		}
		return nil, errors.New("the conversation folder isn’t a local Git repository")
	}
	root = strings.TrimSuffix(root, "\n")
	status, truncated, err := readGit(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, errors.New("couldn’t read local Git changes")
	}
	if truncated {
		return nil, errors.New("too many Git changes to display")
	}
	var changes []agent.FileChange
	previewBytes := 0
	paths := map[string]int{}
	for _, entry := range strings.Split(status, "\x00") {
		if entry == "" {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, errors.New("couldn’t read Git file status")
		}
		xy, path := entry[:2], entry[3:]
		change := agent.FileChange{Path: path}
		switch {
		case xy == "??" || strings.Contains(xy, "A"):
			change.Status = agent.FileAdded
		case strings.Contains(xy, "D"):
			change.Status = agent.FileDeleted
		}
		if xy == "??" {
			// Git lists a nested repository as one untracked directory. Avoid
			// recursively diffing its contents against /dev/null.
			if strings.HasSuffix(path, "/") {
				change.Patch = "Untracked directory"
			} else {
				err = gitPatch(ctx, root, &change, "Untracked file", "--no-index", "--", os.DevNull, filepath.Join(root, path))
			}
		} else if strings.Contains(xy, "U") || xy == "AA" || xy == "DD" {
			change.Status = agent.FileModified
			err = gitPatch(ctx, root, &change, "Unmerged changes (ours)", "--ours", "--", path)
		} else {
			if xy[0] != ' ' {
				err = gitPatch(ctx, root, &change, "Staged changes", "--cached", "--", path)
			}
			if err == nil && xy[1] != ' ' {
				err = gitPatch(ctx, root, &change, "Unstaged changes", "--", path)
			}
		}
		if err != nil {
			return nil, errors.New("couldn’t read local Git changes")
		}
		previewBytes += len(change.Patch)
		if previewBytes > 4<<20 {
			change.Patch = "Diff preview omitted: repository preview limit reached"
		}
		// A staged deletion and an untracked replacement can be separate
		// status records for the same path. Keep one row and both patches.
		if i, found := paths[path]; found {
			existing := &changes[i]
			existing.Status = agent.FileModified
			existing.Added += change.Added
			existing.Removed += change.Removed
			existing.Patch += "\n" + change.Patch
		} else {
			paths[path] = len(changes)
			changes = append(changes, change)
		}
	}
	return changes, nil
}

// Each call is for a single literal path, with rename detection off: numstat
// supplies the full counts even when the following patch preview is truncated.
func gitPatch(ctx context.Context, root string, change *agent.FileChange, label string, args ...string) error {
	options := []string{"diff", "--numstat", "-z", "--patch", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-relative"}
	out, truncated, err := readGit(ctx, root, append(options, args...)...)
	if err != nil || out == "" {
		return err
	}
	// A single path can have two numstat records while it is unmerged.
	// Unmerged output also has an empty NUL record before the patch.
	for {
		stat, rest, ok := strings.Cut(out, "\x00")
		if !ok {
			break
		}
		out = rest
		if stat == "" {
			break
		}
		fields := strings.SplitN(stat, "\t", 3)
		if len(fields) != 3 {
			return errors.New("invalid Git diff statistics")
		}
		// --no-index can use the two-path numstat format even with renames off.
		if fields[2] == "" {
			for range 2 {
				_, rest, ok := strings.Cut(out, "\x00")
				if !ok {
					return errors.New("missing Git diff path")
				}
				out = rest
			}
		}
		// Git reports '-' for binary files.
		if fields[0] == "-" {
			continue
		}
		added, err := strconv.Atoi(fields[0])
		if err != nil {
			return err
		}
		removed, err := strconv.Atoi(fields[1])
		if err != nil {
			return err
		}
		change.Added += added
		change.Removed += removed
	}
	patch := out
	if truncated {
		patch += "\nDiff preview truncated\n"
	}
	if change.Patch != "" {
		change.Patch += "\n"
	}
	change.Patch += label + "\n" + strings.TrimLeft(patch, "\n")
	return nil
}

// No shell, index refresh, configured diff driver, or filesystem-monitor hook
// runs for these reads. Ignore ambient Git overrides that could select another
// repository or index than the conversation's folder.
func readGit(ctx context.Context, directory string, args ...string) (string, bool, error) {
	options := []string{"--no-optional-locks", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-C", directory}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.WaitDelay = time.Second
	var out limitedGitOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	err := cmd.Run()
	// --no-index uses exit 1 to report that the paths differ.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		for _, arg := range args {
			if arg == "--no-index" {
				err = nil
				break
			}
		}
	}
	return out.buf.String(), out.truncated, err
}

type limitedGitOutput struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *limitedGitOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := gitOutputLimit - b.buf.Len()
	if n > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}
