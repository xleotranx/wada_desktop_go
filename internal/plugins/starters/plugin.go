// Package starters reads the new chat's folder and offers coding-focused
// starting points for the repository or directory. It owns the scan and draws
// under the composer; the chat decides where its column goes and adds the
// composer's hint.
package starters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"wada-desktop-go/internal/contract/chat"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Plugin is the new chat's starters.
type Plugin struct {
	ctx  *kernel.Context
	chat chat.Controller
	scan kernel.Task

	folder folderStarters
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "starters", Title: "New chat starters",
		Description: "Read the new chat's folder and offer what it makes possible.",
		Requires:    []kernel.Named{chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.chat = kernel.Require(c, chat.ControllerKey)
	kernel.On(c, chat.Changed, p.changed)
	p.changed(p.chat.Current())
	return kernel.Contribute(c, chat.Starters, "starters.folder", 0, shell.View{Render: p.view})
}

// changed follows the open chat: a new chat with a folder gets its starters,
// and any other conversation lets the old scan go.
func (p *Plugin) changed(conv chat.Conversation) {
	if conv.Session != "" || conv.Directory == "" {
		had := p.folder.dir != ""
		p.scan.Cancel()
		p.folder = folderStarters{}
		if had {
			p.ctx.Invalidate()
		}
		return
	}
	if conv.Directory == p.folder.dir {
		return
	}
	p.scanFolder(conv.Directory)
}

// scanFolder reads the folder for its starters. Only the latest read lands,
// and only while the same new chat still shows its folder.
func (p *Plugin) scanFolder(dir string) {
	p.scan.Cancel()
	p.folder = folderStarters{dir: dir, loading: true}
	kernel.Go(p.ctx, &p.scan, func(ctx context.Context) (folderInfo, error) {
		return readFolder(ctx, dir)
	}, func(info folderInfo, err error) {
		conv := p.chat.Current()
		if conv.Session != "" || conv.Directory != dir {
			return
		}
		p.folder.loading = false
		p.folder.info, p.folder.err = info, err
		p.ctx.Invalidate()
	})
}

// folderStarters is what the folder holds, as the starters see it.
type folderStarters struct {
	dir     string
	loading bool
	info    folderInfo
	err     error
}

// folderInfo counts the files the starters care about. Names are the first
// few code files or markers, to show that Wada sees the folder the user chose.
type folderInfo struct {
	Files, Code, Markers int
	CodeNames            []string
}

// codeFolder reports whether the folder contains recognizable code or project markers.
func (i folderInfo) codeFolder() bool {
	return i.Markers > 0 || i.Code > 0
}

var codeExtensions = map[string]bool{
	".go": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".py": true, ".rb": true, ".rs": true, ".java": true, ".kt": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".cs": true, ".swift": true, ".php": true, ".sh": true, ".sql": true,
	".html": true, ".htm": true, ".css": true, ".vue": true, ".svelte": true,
}

// codeMarkers are files that make a folder a project even when its source
// files are nested, as a folder with only go.mod at its top.
var codeMarkers = map[string]bool{
	"go.mod": true, "go.work": true, "package.json": true, "cargo.toml": true,
	"pyproject.toml": true, "requirements.txt": true, "gemfile": true,
	"makefile": true, "cmakelists.txt": true, "build.gradle": true,
	"pom.xml": true, "composer.json": true, "tsconfig.json": true, "dockerfile": true,
}

// readFolder counts the folder's top-level files. Project markers are read
// by name; properties and contents are the agent's job once it starts.
func readFolder(ctx context.Context, dir string) (folderInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return folderInfo{}, err
	}
	info := folderInfo{}
	for i, e := range entries {
		if i%256 == 0 && ctx.Err() != nil {
			return folderInfo{}, ctx.Err()
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		info.Files++
		lower := strings.ToLower(name)
		ext := strings.ToLower(filepath.Ext(name))
		if codeExtensions[ext] || codeMarkers[lower] {
			info.Code++
			if codeMarkers[lower] {
				info.Markers++
			}
			info.CodeNames = appendName(info.CodeNames, name)
		}
	}
	return info, nil
}

// appendName keeps the first three names of a kind.
func appendName(names []string, name string) []string {
	if len(names) >= 3 {
		return names
	}
	return append(names, name)
}
