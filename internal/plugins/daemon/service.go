package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/terminal"
	"wada-desktop-go/internal/protocol"
)

type fileTypes struct{}

func (fileTypes) Accepts(mime string) bool {
	return strings.HasPrefix(mime, "text/") ||
		slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, mime)
}

func (fileTypes) Summary() string { return "images and text files" }

func (p *Plugin) Sessions(ctx context.Context, q agent.SessionQuery) (agent.SessionPage, error) {
	cli, err := p.current()
	if err != nil {
		return agent.SessionPage{}, err
	}
	page, err := cli.Sessions(ctx, protocol.SessionQuery{
		Cursor:    q.Cursor,
		Search:    q.Search,
		Directory: q.Directory,
	})
	if err != nil {
		return agent.SessionPage{}, err
	}
	out := make([]agent.Session, len(page.Sessions))
	for i, s := range page.Sessions {
		out[i] = fromProtoSession(s)
	}
	return agent.SessionPage{Sessions: out, Next: page.Next}, nil
}

func (p *Plugin) Create(ctx context.Context, req agent.CreateSession) (agent.Session, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Session{}, err
	}
	sess, err := cli.Create(ctx, protocol.CreateSessionRequest{
		IdempotencyKey: uuid.NewString(),
		Title:          req.Title,
		Directory:      req.Directory,
		Provider:       req.Provider,
		Profile:        req.Profile,
		Model:          toProtoModelRef(req.Model),
		Mode:           req.Mode,
	})
	return fromProtoSession(sess), err
}

func (p *Plugin) Spawn(ctx context.Context, req agent.Spawn) (agent.Session, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Session{}, err
	}
	pol := toProtoToolPolicy(&req.Policy)
	var policy protocol.ToolPolicy
	if pol != nil {
		policy = *pol
	}
	sess, err := cli.Spawn(ctx, protocol.SpawnRequest{
		IdempotencyKey: uuid.NewString(),
		Parent:         string(req.Parent),
		Name:           req.Name,
		Directory:      req.Directory,
		Mode:           req.Mode,
		Model:          toProtoModelRef(req.Model),
		Policy:         policy,
	})
	return fromProtoSession(sess), err
}

func (p *Plugin) Messages(ctx context.Context, id agent.SessionID) ([]agent.Message, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	msgs, err := cli.Messages(ctx, string(id))
	if err != nil {
		return nil, err
	}
	out := make([]agent.Message, len(msgs))
	for i, m := range msgs {
		out[i] = fromProtoMessage(m)
	}
	return out, nil
}

func (p *Plugin) Send(ctx context.Context, req agent.Send) (agent.SendDisposition, error) {
	cli, err := p.current()
	if err != nil {
		return 0, err
	}
	files := make([]protocol.File, len(req.Files))
	for i, f := range req.Files {
		files[i] = toProtoFile(f)
	}
	skills := make([]protocol.SkillRef, len(req.Skills))
	for i, s := range req.Skills {
		skills[i] = protocol.SkillRef{ID: s.ID}
	}
	disp, err := cli.Send(ctx, protocol.SendRequest{
		MessageID: uuid.NewString(),
		Session:   string(req.Session),
		Mode:      req.Mode,
		Text:      req.Text,
		Files:     files,
		Skills:    skills,
		Deliver:   toProtoDelivery(req.Deliver),
	})
	if err != nil {
		var rpcErr *protocol.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == protocol.ErrCodeNotSent {
			return 0, fmt.Errorf("%w: %s", agent.ErrNotSent, rpcErr.Message)
		}
		return 0, err
	}
	return fromProtoDisposition(disp), nil
}

func (p *Plugin) Abort(ctx context.Context, id agent.SessionID) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.Abort(ctx, string(id))
}

func (p *Plugin) Reply(ctx context.Context, req agent.Reply) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.Reply(ctx, protocol.ReplyRequest{
		Session:  string(req.Session),
		Approval: string(req.Approval),
		Decision: toProtoDecision(req.Decision),
	})
}

func (p *Plugin) Rename(ctx context.Context, id agent.SessionID, title string) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.Rename(ctx, string(id), title)
}

func (p *Plugin) Delete(ctx context.Context, id agent.SessionID) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.Delete(ctx, string(id))
}

func (p *Plugin) Models(ctx context.Context) ([]agent.Model, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	models, err := cli.Models(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Model, len(models))
	for i, m := range models {
		out[i] = fromProtoModel(m)
	}
	return out, nil
}

func (p *Plugin) DefaultModel(ctx context.Context) (agent.ModelRef, error) {
	cli, err := p.current()
	if err != nil {
		return agent.ModelRef{}, err
	}
	ref, err := cli.DefaultModel(ctx)
	return fromProtoModelRef(ref), err
}

func (p *Plugin) SetModel(ctx context.Context, id agent.SessionID, model agent.ModelRef) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.SetModel(ctx, string(id), toProtoModelRef(model))
}

func (p *Plugin) Modes(ctx context.Context) ([]agent.Mode, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	modes, err := cli.Modes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Mode, len(modes))
	for i, m := range modes {
		out[i] = agent.Mode{ID: m.ID, Name: m.Name, Description: m.Description}
	}
	return out, nil
}

func (p *Plugin) Find(ctx context.Context, directory, query string, limit int) ([]agent.FileEntry, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	entries, err := cli.FindFiles(ctx, directory, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]agent.FileEntry, len(entries))
	for i, e := range entries {
		out[i] = agent.FileEntry{Path: e.Path, IsDir: e.IsDir}
	}
	return out, nil
}

func (p *Plugin) Read(ctx context.Context, directory, path string) ([]byte, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	return cli.ReadFile(ctx, directory, path)
}

func (p *Plugin) Fit(ctx context.Context, model agent.ModelRef, file agent.File) (int, int, error) {
	cli, err := p.current()
	if err != nil {
		return 0, 0, err
	}
	return cli.FitImage(ctx, toProtoModelRef(model), toProtoFile(file))
}

func (p *Plugin) Skills(ctx context.Context, directory string) ([]agent.Skill, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	skills, err := cli.Skills(ctx, directory)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Skill, len(skills))
	for i, s := range skills {
		out[i] = agent.Skill{ID: s.ID, Name: s.Name, Description: s.Description}
	}
	return out, nil
}

func (p *Plugin) Changes(ctx context.Context, id agent.SessionID) ([]agent.FileChange, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	changes, err := cli.Changes(ctx, string(id))
	if err != nil {
		return nil, err
	}
	return fromProtoChanges(changes), nil
}

func (p *Plugin) Rewind(ctx context.Context, id agent.SessionID, msg agent.MessageID) (agent.SessionID, error) {
	cli, err := p.current()
	if err != nil {
		return "", err
	}
	nextID, err := cli.Rewind(ctx, string(id), string(msg))
	return agent.SessionID(nextID), err
}

func (p *Plugin) Pending(ctx context.Context, id agent.SessionID) ([]agent.Interaction, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	pending, err := cli.PendingInteractions(ctx, string(id))
	if err != nil {
		return nil, err
	}
	out := make([]agent.Interaction, len(pending))
	for i := range pending {
		out[i] = *fromProtoInteraction(&pending[i])
	}
	return out, nil
}

func (p *Plugin) Answer(ctx context.Context, ans agent.InteractionAnswer) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.AnswerInteraction(ctx, protocol.InteractionAnswer{
		Answers:   ans.Answers,
		Session:   string(ans.Session),
		ID:        string(ans.ID),
		Cancelled: ans.Cancelled,
		Confirmed: ans.Confirmed,
		Value:     ans.Value,
	})
}

func (p *Plugin) Harnesses(ctx context.Context) ([]agent.Harness, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.Harnesses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Harness, len(list))
	for i, h := range list {
		out[i] = fromProtoHarness(h)
	}
	return out, nil
}

func (p *Plugin) Profiles(ctx context.Context, dir string) ([]agent.Profile, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.Profiles(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Profile, len(list))
	for i, pr := range list {
		src := agent.ProfileUser
		if pr.Source == protocol.ProfileProject {
			src = agent.ProfileProject
		}
		out[i] = agent.Profile{Name: pr.Name, Description: pr.Description, Provider: pr.Provider,
			Mode: pr.Mode, Instructions: pr.Instructions, Source: src}
	}
	return out, nil
}

func (p *Plugin) Workers(ctx context.Context, parent agent.SessionID) ([]agent.Worker, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.Workers(ctx, string(parent))
	if err != nil {
		return nil, err
	}
	out := make([]agent.Worker, len(list))
	for i, w := range list {
		out[i] = fromProtoWorker(w)
	}
	return out, nil
}

func (p *Plugin) PRStatus(ctx context.Context, directory string) (*agent.PRStatus, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	pr, err := cli.WorktreePR(ctx, directory)
	if err != nil {
		return nil, err
	}
	return fromProtoPRStatus(pr), nil
}

type terminalService struct{ p *Plugin }

func (s terminalService) List(ctx context.Context) ([]terminal.Info, error) {
	cli, err := s.p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.Terminals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]terminal.Info, len(list))
	for i, t := range list {
		out[i] = fromProtoTerminal(t)
	}
	return out, nil
}

func (s terminalService) Create(ctx context.Context, req terminal.CreateRequest) (terminal.Info, error) {
	cli, err := s.p.current()
	if err != nil {
		return terminal.Info{}, err
	}
	info, err := cli.CreateTerminal(ctx, protocol.TerminalCreateRequest{
		Name:      req.Name,
		Directory: req.Directory,
		Shell:     req.Shell,
		Cols:      req.Cols,
		Rows:      req.Rows,
	})
	if err != nil {
		return terminal.Info{}, err
	}
	return fromProtoTerminal(info), nil
}

func (s terminalService) Attach(ctx context.Context, req terminal.AttachRequest) (terminal.Stream, error) {
	cli, err := s.p.current()
	if err != nil {
		return nil, err
	}
	st, err := cli.AttachTerminal(ctx, protocol.TerminalAttachRequest{
		ID:   req.ID,
		Cols: req.Cols,
		Rows: req.Rows,
	})
	if err != nil {
		return nil, err
	}
	return terminalStreamAdapter{s: st}, nil
}

func (s terminalService) Kill(ctx context.Context, id string) error {
	cli, err := s.p.current()
	if err != nil {
		return err
	}
	return cli.KillTerminal(ctx, id)
}

type terminalStreamAdapter struct{ s *client.TerminalStream }

func (a terminalStreamAdapter) Info() terminal.Info          { return fromProtoTerminal(a.s.Info()) }
func (a terminalStreamAdapter) Snapshot() []byte             { return a.s.Snapshot() }
func (a terminalStreamAdapter) Output() <-chan []byte        { return a.s.Output() }
func (a terminalStreamAdapter) WriteInput(data []byte) error { return a.s.WriteInput(data) }
func (a terminalStreamAdapter) Resize(cols, rows int) error  { return a.s.Resize(cols, rows) }
func (a terminalStreamAdapter) Close() error                 { return a.s.Close() }

// ---- agent.Workspaces

func (p *Plugin) Projects(ctx context.Context, archived bool) ([]agent.Project, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.Projects(ctx, archived)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Project, len(list))
	for i, pr := range list {
		out[i] = fromProtoProject(pr)
	}
	return out, nil
}

func (p *Plugin) Workspaces(ctx context.Context, archived bool) ([]agent.Workspace, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.Workspaces(ctx, protocol.WorkspacesListRequest{Archived: archived})
	if err != nil {
		return nil, err
	}
	out := make([]agent.Workspace, len(list))
	for i, w := range list {
		out[i] = fromProtoWorkspace(w)
	}
	return out, nil
}

func (p *Plugin) AddProject(ctx context.Context, root string) (agent.Project, agent.Workspace, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Project{}, agent.Workspace{}, err
	}
	resp, err := cli.AddProject(ctx, root)
	if err != nil {
		return agent.Project{}, agent.Workspace{}, err
	}
	return fromProtoProject(resp.Project), fromProtoWorkspace(resp.Workspace), nil
}

func (p *Plugin) RenameProject(ctx context.Context, id agent.ProjectID, name string) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.RenameProject(ctx, string(id), name)
}

func (p *Plugin) ArchiveProject(ctx context.Context, id agent.ProjectID) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.ArchiveProject(ctx, string(id))
}

func (p *Plugin) RenameWorkspace(ctx context.Context, id agent.WorkspaceID, title string) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.RenameWorkspace(ctx, string(id), title)
}

func (p *Plugin) CreateWorkspace(ctx context.Context, req agent.CreateWorkspace) (agent.Workspace, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Workspace{}, err
	}
	w, err := cli.CreateWorkspace(ctx, protocol.WorkspaceCreateRequest{
		IdempotencyKey: uuid.NewString(),
		Project:        string(req.Project),
		Isolation:      toProtoIsolation(req.Isolation),
		Mode:           toProtoStartMode(req.Mode),
		From:           req.From,
		PR:             req.PR,
		Path:           req.Path,
		Slug:           req.Slug,
		Title:          req.Title,
	})
	if err != nil {
		return agent.Workspace{}, err
	}
	return fromProtoWorkspace(w), nil
}

func (p *Plugin) ArchiveWorkspace(ctx context.Context, id agent.WorkspaceID, force bool) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	err = cli.ArchiveWorkspace(ctx, string(id), force)
	var rerr *protocol.RPCError
	if errors.As(err, &rerr) && rerr.Code == protocol.ErrCodeConflict {
		return fmt.Errorf("%w: %s", agent.ErrWorkUnsaved, rerr.Message)
	}
	return err
}

func (p *Plugin) RestoreWorkspace(ctx context.Context, id agent.WorkspaceID) (agent.Workspace, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Workspace{}, err
	}
	w, err := cli.RestoreWorkspace(ctx, string(id))
	if err != nil {
		return agent.Workspace{}, err
	}
	return fromProtoWorkspace(w), nil
}

func (p *Plugin) Refs(ctx context.Context, project agent.ProjectID) (agent.Refs, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Refs{}, err
	}
	r, err := cli.WorkspaceRefs(ctx, string(project))
	if err != nil {
		return agent.Refs{}, err
	}
	out := agent.Refs{Default: r.Default, Branches: r.Branches, Remotes: r.Remotes}
	for _, pr := range r.PRs {
		out.PRs = append(out.PRs, agent.PullRequest{Number: pr.Number, Title: pr.Title, Branch: pr.Branch, Fork: pr.Fork})
	}
	for _, wt := range r.Worktrees {
		out.Worktrees = append(out.Worktrees, fromProtoWorktree(wt))
	}
	return out, nil
}

func (p *Plugin) Config(ctx context.Context, id agent.WorkspaceID) (agent.WorkspaceConfig, error) {
	cli, err := p.current()
	if err != nil {
		return agent.WorkspaceConfig{}, err
	}
	c, err := cli.WorkspaceConfig(ctx, string(id))
	if err != nil {
		return agent.WorkspaceConfig{}, err
	}
	out := agent.WorkspaceConfig{Found: c.Found, Hash: c.Hash, Trusted: c.Trusted, Problem: c.Error, Setup: c.Setup,
		Teardown: c.Teardown,
		SetupRun: fromProtoRun(c.SetupTerminal)}
	for _, s := range c.Services {
		out.Services = append(out.Services, agent.WorkspaceService{Name: s.Name, Command: s.Command, Port: s.Port, Run: fromProtoRun(s.Terminal)})
	}
	return out, nil
}

func fromProtoRun(t *protocol.TerminalInfo) *agent.Run {
	if t == nil {
		return nil
	}
	return &agent.Run{Terminal: t.ID, Running: t.Running, ExitCode: t.ExitCode}
}

func (p *Plugin) TrustConfig(ctx context.Context, id agent.WorkspaceID, hash string) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.TrustWorkspaceConfig(ctx, string(id), hash)
}

func (p *Plugin) RunSetup(ctx context.Context, id agent.WorkspaceID) (string, error) {
	cli, err := p.current()
	if err != nil {
		return "", err
	}
	t, err := cli.RunSetup(ctx, string(id))
	return t.ID, untrusted(err)
}

func (p *Plugin) StartService(ctx context.Context, id agent.WorkspaceID, name string) (string, error) {
	cli, err := p.current()
	if err != nil {
		return "", err
	}
	t, err := cli.StartService(ctx, string(id), name)
	return t.ID, untrusted(err)
}

func (p *Plugin) StopService(ctx context.Context, id agent.WorkspaceID, name string) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.StopService(ctx, string(id), name)
}

// untrusted maps the daemon's refusal of an untrusted wada.json to
// agent.ErrUntrusted.
func untrusted(err error) error {
	var rerr *protocol.RPCError
	if errors.As(err, &rerr) && rerr.Code == protocol.ErrCodeConflict {
		return fmt.Errorf("%w: %s", agent.ErrUntrusted, rerr.Message)
	}
	return err
}

func (p *Plugin) BaseChanges(ctx context.Context, id agent.WorkspaceID) ([]agent.FileChange, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.WorkspaceChanges(ctx, string(id))
	if err != nil {
		return nil, err
	}
	return fromProtoChanges(list), nil
}

func (p *Plugin) DraftCommit(ctx context.Context, id agent.WorkspaceID) (string, error) {
	cli, err := p.current()
	if err != nil {
		return "", err
	}
	return cli.DraftCommit(ctx, string(id))
}

func (p *Plugin) Commit(ctx context.Context, id agent.WorkspaceID, message string) (string, error) {
	cli, err := p.current()
	if err != nil {
		return "", err
	}
	return cli.Commit(ctx, string(id), message)
}

func (p *Plugin) DraftPR(ctx context.Context, id agent.WorkspaceID) (string, string, error) {
	cli, err := p.current()
	if err != nil {
		return "", "", err
	}
	d, err := cli.DraftPR(ctx, string(id))
	return d.Title, d.Body, err
}

func (p *Plugin) CreatePR(ctx context.Context, id agent.WorkspaceID, title, body string, draft bool) (*agent.PRStatus, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	pr, err := cli.CreateWorkspacePR(ctx, protocol.WorkspacePRRequest{Workspace: string(id), Title: title, Body: body, Draft: draft})
	if err != nil {
		return nil, err
	}
	return fromProtoPRStatus(pr), nil
}
