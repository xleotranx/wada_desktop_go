package desktop

import (
	"context"
	"github.com/egoist/mygo/ui/editor"
	"wada-desktop-go/internal/features/coding"
	terminalfeature "wada-desktop-go/internal/features/terminal"
	"wada-desktop-go/internal/workspace"
)

func codingModule(a *app) *coding.Module {
	for _, module := range a.features {
		if m, ok := module.(*coding.Module); ok {
			return m
		}
	}
	panic("test needs coding module")
}
func terminalModule(a *app) *terminalfeature.Module {
	for _, module := range a.features {
		if m, ok := module.(*terminalfeature.Module); ok {
			return m
		}
	}
	panic("test needs terminal module")
}

type filesState = coding.FilesState
type changesState = coding.ChangesState
type reviewNote = coding.ReviewNote
type recentFile = coding.RecentFile
type terminalTab = terminalfeature.Tab

const filesShown = 256 * 1024
const recentFileLimit = 8
const changesWorking = "working"
const changesBranch = "branch"
const terminalDefaultHeight float32 = 220
const terminalMaxHeight float32 = 480

func terminalTabLabel(tab *terminalTab) string { return terminalfeature.TabLabel(tab) }
func localShells() []string                    { return terminalfeature.LocalShells() }
func (a *app) openFiles()                      { codingModule(a).OpenFiles() }
func (a *app) filesList(rel string)            { codingModule(a).FilesList(rel) }
func (a *app) filesFind()                      { codingModule(a).FilesFind() }
func (a *app) filesRead(rel string)            { codingModule(a).FilesRead(rel) }
func (a *app) filesShowInTree()                { codingModule(a).FilesShowInTree() }
func (a *app) copyFilesContents()              { codingModule(a).CopyFilesContents() }
func (a *app) openChanges(mode string)         { codingModule(a).OpenChanges(mode) }
func (a *app) beginReviewNote(file string, r editor.DiffLineRange) {
	codingModule(a).BeginReviewNote(file, r)
}
func (a *app) reviewNotesFor(file string) []*reviewNote { return codingModule(a).ReviewNotesFor(file) }
func (a *app) quickOpenEntries(st *filesState) []workspace.FileEntry {
	return codingModule(a).QuickOpenEntries(st)
}
func (a *app) newTerminalTab()                  { terminalModule(a).NewTerminalTab() }
func (a *app) ensureTerminal()                  { terminalModule(a).EnsureTerminal() }
func statusLetter(status string) string         { return coding.StatusLetter(status) }
func (a *app) mentionOpen() bool                { return codingModule(a).MentionOpen() }
func (a *app) ensureVcs()                       { codingModule(a).EnsureVcs() }
func (a *app) watchWorkspacePolicy(dir string)  { codingModule(a).WatchWorkspacePolicy(dir) }
func (a *app) acceptPolicy(result policyResult) { codingModule(a).AcceptPolicy(result) }

type policyTarget = workspace.PolicyTarget
type policyChange = workspace.PolicyChange
type policyResult = workspace.PolicyResult

func (a *app) refreshWorkspacePolicy()              { codingModule(a).RefreshPolicy() }
func (a *app) setAsk(ask bool)                      { codingModule(a).SetAsk(ask) }
func vcsKey(server, dir string) string              { return coding.VCSKey(server, dir) }
func mentionToken(draft string) (int, string, bool) { return coding.MentionToken(draft) }
func watchPolicy(ctx context.Context, policy workspace.Policy, dirs <-chan policyTarget, writes <-chan policyChange, publish func(policyResult)) {
	policy.(workspace.PolicyMonitor).MonitorPolicy(ctx, dirs, writes, publish)
}
