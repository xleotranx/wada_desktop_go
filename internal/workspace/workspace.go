// Package workspace defines optional file and version-control capabilities.
// Chat backends need not implement them.
package workspace

import (
	"context"

	"wada-desktop-go/internal/agent"
)

type FileEntry struct {
	Path, Name string
	Directory  bool
}
type VCSInfo struct{ Provider, Branch, Base string }
type Change struct {
	File, Status         string
	Additions, Deletions int
	Patch                string
}
type Client interface {
	ListDir(context.Context, string, string) ([]FileEntry, error)
	FindFiles(context.Context, string, string, int) ([]FileEntry, error)
	ReadWorkspaceFile(context.Context, string, string) ([]byte, error)
	WorkspaceVcs(context.Context, string) (VCSInfo, error)
	WorkspaceChanges(context.Context, string, string) ([]Change, error)
}

type Folders interface {
	Location(context.Context, string) (string, error)
	ListDir(context.Context, string, string) ([]FileEntry, error)
}
type Policy interface {
	ReadPolicy(context.Context, string) (string, error)
	WritePolicy(context.Context, string, bool) (string, error)
}

type PolicyTarget struct {
	Dir      string
	Revision uint64
}
type PolicyChange struct {
	Dir      string
	Ask      bool
	Revision uint64
}
type PolicyResult struct {
	Dir, Level string
	Revision   uint64
	Err        error
}
type PolicyMonitor interface {
	MonitorPolicy(context.Context, <-chan PolicyTarget, <-chan PolicyChange, func(PolicyResult))
}

type PolicyProvider interface {
	Policy(agent.Config) (Policy, error)
}
