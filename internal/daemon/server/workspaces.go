package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"wada-desktop-go/internal/daemon/workspaces"
	"wada-desktop-go/internal/protocol"
)

// dispatchWorkspaces serves the workspace.* methods.
func (s *Server) dispatchWorkspaces(ctx context.Context, method string, payload json.RawMessage) (any, *protocol.RPCError) {
	decode := func(v any) *protocol.RPCError {
		if len(payload) == 0 {
			return nil
		}
		if err := json.Unmarshal(payload, v); err != nil {
			return invalidErr(err)
		}
		return nil
	}
	switch method {
	case protocol.MethodProjectsList:
		var req protocol.ProjectsListRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		return s.workspaces.Projects(req.Archived), nil
	case protocol.MethodProjectAdd:
		var req protocol.ProjectAddRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		if !filepath.IsAbs(req.Root) {
			return nil, invalidErr(errors.New("enter an absolute project folder path"))
		}
		info, err := os.Stat(filepath.Clean(req.Root))
		if err != nil {
			return nil, invalidErr(err)
		}
		if !info.IsDir() {
			return nil, invalidErr(fmt.Errorf("%q is not a folder", req.Root))
		}
		p, w, err := s.workspaces.AddProject(ctx, req.Root)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return protocol.ProjectAddResponse{Project: p, Workspace: w}, nil
	case protocol.MethodProjectFolders:
		var req protocol.ProjectFoldersRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		folders, err := projectFolders(ctx, req.Prefix)
		if err != nil {
			return nil, invalidErr(err)
		}
		return folders, nil
	case protocol.MethodProjectRename:
		var req protocol.ProjectRenameRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		if err := s.workspaces.RenameProject(ctx, req.Project, req.Name); err != nil {
			return nil, workspaceErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodProjectArchive:
		var req protocol.ProjectArchiveRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		if err := s.workspaces.ArchiveProject(ctx, req.Project); err != nil {
			return nil, workspaceErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodWorkspacesList:
		var req protocol.WorkspacesListRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		return s.workspaces.Workspaces(req.Project, req.Archived), nil
	case protocol.MethodWorkspaceRename:
		var req protocol.WorkspaceRenameRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		if err := s.workspaces.RenameWorkspace(ctx, req.Workspace, req.Title); err != nil {
			return nil, workspaceErr(err)
		}
		return map[string]bool{"ok": true}, nil
	case protocol.MethodWorkspaceCreate:
		var req protocol.WorkspaceCreateRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		return s.withCreationReceipt(ctx, req.IdempotencyKey, "workspace", hashStruct(req),
			func(id string) any { w, _ := s.workspaces.Workspace(id); return w },
			func() (string, any, error) {
				w, err := s.workspaces.Create(ctx, req)
				if err != nil {
					return "", nil, workspaceErr(err)
				}
				return w.ID, w, nil
			})
	case protocol.MethodWorkspaceArchive:
		var req protocol.WorkspaceArchiveRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		w, _ := s.workspaces.Workspace(req.Workspace)
		if err := s.workspaces.Archive(ctx, req.Workspace, req.Force); err != nil {
			return nil, workspaceErr(err)
		}
		s.stopWorkIn(ctx, w)
		return map[string]bool{"ok": true}, nil
	case protocol.MethodWorkspaceRestore:
		var req protocol.WorkspaceIDRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		w, err := s.workspaces.Restore(ctx, req.Workspace)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return w, nil
	case protocol.MethodWorkspaceRefs:
		var req protocol.WorkspaceRefsRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		refs, err := s.workspaces.Refs(ctx, req.Project)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return refs, nil
	case protocol.MethodWorkspaceChanges:
		var req protocol.WorkspaceIDRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		changes, err := s.workspaces.Changes(ctx, req.Workspace)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return changes, nil
	case protocol.MethodWorkspaceConfig:
		var req protocol.WorkspaceIDRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		return s.workspaceConfig(ctx, req.Workspace)
	case protocol.MethodWorkspaceTrust:
		var req protocol.WorkspaceTrustRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		if err := s.workspaces.Trust(ctx, req.Workspace, req.Hash); err != nil {
			return nil, workspaceErr(err)
		}
		return struct{}{}, nil
	case protocol.MethodWorkspaceSetup:
		var req protocol.WorkspaceIDRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		return s.runSetup(ctx, req.Workspace)
	case protocol.MethodServiceStart:
		var req protocol.WorkspaceServiceRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		return s.startService(ctx, req.Workspace, req.Name)
	case protocol.MethodServiceStop:
		var req protocol.WorkspaceServiceRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		return s.stopService(ctx, req.Workspace, req.Name)
	case protocol.MethodCommitDraft:
		var req protocol.WorkspaceIDRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		msg, err := s.workspaces.DraftCommit(ctx, req.Workspace, s.cfg.Backend.Generate)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return protocol.CommitDraft{Message: msg}, nil
	case protocol.MethodCommit:
		var req protocol.WorkspaceCommitRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		sha, err := s.workspaces.Commit(ctx, req.Workspace, req.Message)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return protocol.CommitResult{SHA: sha}, nil
	case protocol.MethodPRDraft:
		var req protocol.WorkspaceIDRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		title, body, err := s.workspaces.DraftPR(ctx, req.Workspace, s.cfg.Backend.Generate)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return protocol.PRDraft{Title: title, Body: body}, nil
	case protocol.MethodPRCreate:
		var req protocol.WorkspacePRRequest
		if rerr := decode(&req); rerr != nil {
			return nil, rerr
		}
		pr, err := s.workspaces.CreatePR(ctx, req.Workspace, req.Title, req.Body, req.Draft)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return pr, nil
	}
	return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "unknown method " + method}
}

func workspaceErr(err error) *protocol.RPCError {
	var pending *workspaces.PendingError
	var teardown *workspaces.TeardownError
	switch {
	case errors.As(err, &pending), errors.As(err, &teardown):
		return &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: err.Error()}
	case errors.Is(err, workspaces.ErrUntrusted):
		return &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: err.Error()}
	case errors.Is(err, workspaces.ErrNotFound):
		return &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: err.Error()}
	}
	return &protocol.RPCError{Code: protocol.ErrCodeInvalid, Message: err.Error()}
}

// stopWorkIn ends what still runs in an archived workspace: its conversations'
// turns and its terminals. Each is tried once; one that fails is left.
func (s *Server) stopWorkIn(ctx context.Context, w protocol.WorkspaceInfo) {
	if w.Directory == "" {
		return
	}
	if page, err := s.cfg.Backend.Sessions(ctx, protocol.SessionQuery{Directory: w.Directory}); err == nil {
		for _, sess := range page.Sessions {
			_ = s.cfg.Backend.Abort(ctx, sess.ID)
		}
	}
	if tb, ok := s.cfg.Backend.(TerminalBackend); ok {
		if list, err := tb.ListTerminals(ctx); err == nil {
			for _, t := range list {
				if t.Running && t.Directory == w.Directory {
					_ = tb.KillTerminal(ctx, t.ID)
				}
			}
		}
	}
}

// workspaceDir resolves a request's workspace to its directory. A request may
// name a directory too only when it is that workspace's.
func (s *Server) workspaceDir(id, dir string) (string, *protocol.RPCError) {
	w, ok := s.workspaces.Workspace(id)
	if !ok || w.Archived != 0 {
		return "", &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "no active workspace " + id}
	}
	if dir != "" && dir != w.Directory {
		return "", &protocol.RPCError{Code: protocol.ErrCodeInvalid, Message: "the directory is not the workspace's"}
	}
	return w.Directory, nil
}

// placeSession names the workspace a conversation belongs to, adding a project
// for its directory when none was ever there. A failed add leaves it unplaced.
func (s *Server) placeSession(ctx context.Context, sess protocol.Session) protocol.Session {
	if sess.Directory == "" {
		return sess
	}
	if w, err := s.workspaces.Adopt(ctx, sess.Directory); err == nil {
		sess.Workspace = w.ID
	}
	return sess
}

// placeTerminal names the active workspace a terminal runs in, if any.
func (s *Server) placeTerminal(t protocol.TerminalInfo) protocol.TerminalInfo {
	if w, ok := s.workspaces.Active(t.Directory); ok {
		t.Workspace = w.ID
	}
	return t
}

// broadcastUpdate sends a workspace_updated event to every subscription that
// takes all sessions' events.
func (s *Server) broadcastUpdate(ev protocol.Event) {
	ev.Epoch = s.epoch
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.broadcastEvent("", payload)
}

// ---- wada.json: setup and services

// terminals is the terminal backend, or the error a client gets without one.
func (s *Server) terminals() (TerminalBackend, *protocol.RPCError) {
	tb, ok := s.cfg.Backend.(TerminalBackend)
	if !ok {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "terminals are not supported"}
	}
	return tb, nil
}

// roleTerminals is the last terminal of each role started in a directory.
func (s *Server) roleTerminals(ctx context.Context, dir string) map[string]*protocol.TerminalInfo {
	out := map[string]*protocol.TerminalInfo{}
	tb, rerr := s.terminals()
	if rerr != nil {
		return out
	}
	list, err := tb.ListTerminals(ctx)
	if err != nil {
		return out
	}
	for _, t := range list {
		if t.Role == "" || t.Directory != dir {
			continue
		}
		if cur, ok := out[t.Role]; !ok || t.CreatedAt >= cur.CreatedAt {
			placed := s.placeTerminal(t)
			out[t.Role] = &placed
		}
	}
	return out
}

func (s *Server) workspaceConfig(ctx context.Context, id string) (any, *protocol.RPCError) {
	l, err := s.workspaces.LoadConfig(ctx, id)
	if err != nil {
		return nil, workspaceErr(err)
	}
	out := protocol.WorkspaceConfig{Found: l.Found, Hash: l.Hash, Trusted: l.Trusted, Setup: l.Config.Setup,
		Teardown: l.Config.Teardown}
	if l.Err != nil {
		out.Error = l.Err.Error()
	}
	if !l.Found || l.Err != nil {
		return out, nil
	}
	running := s.roleTerminals(ctx, l.Workspace.Directory)
	out.SetupTerminal = running[workspaces.SetupRole]
	for i, def := range l.Config.Services {
		out.Services = append(out.Services, protocol.ServiceStatus{Name: def.Name, Command: def.Command,
			Port: l.PortBase + i, Terminal: running[workspaces.ServiceRole(def.Name)]})
	}
	return out, nil
}

// runSetup starts the workspace's setup in a terminal of its own. Setup is
// not retried: a failed start is reported, and running it again is the
// user's call.
func (s *Server) runSetup(ctx context.Context, id string) (any, *protocol.RPCError) {
	tb, rerr := s.terminals()
	if rerr != nil {
		return nil, rerr
	}
	l, err := s.workspaces.LoadConfig(ctx, id)
	if err == nil {
		err = l.Runnable()
	}
	if err != nil {
		return nil, workspaceErr(err)
	}
	if len(l.Config.Setup) == 0 {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "wada.json has no setup"}
	}
	if t := s.roleTerminals(ctx, l.Workspace.Directory)[workspaces.SetupRole]; t != nil && t.Running {
		return *t, nil
	}
	return s.startRun(ctx, tb, protocol.TerminalCreateRequest{Name: "Setup", Directory: l.Workspace.Directory,
		Command: l.SetupCommand(), Env: l.Env(""), Role: workspaces.SetupRole})
}

// startRun starts a terminal that runs a command.
func (s *Server) startRun(ctx context.Context, tb TerminalBackend, req protocol.TerminalCreateRequest) (any, *protocol.RPCError) {
	info, err := tb.CreateTerminal(ctx, req)
	if err != nil {
		return nil, internalErr(err)
	}
	return s.placeTerminal(info), nil
}

// startService starts a service in a terminal of its own, or returns the one
// it runs in.
func (s *Server) startService(ctx context.Context, id, name string) (any, *protocol.RPCError) {
	tb, rerr := s.terminals()
	if rerr != nil {
		return nil, rerr
	}
	l, err := s.workspaces.LoadConfig(ctx, id)
	if err == nil {
		err = l.Runnable()
	}
	if err != nil {
		return nil, workspaceErr(err)
	}
	def, _, ok := l.Service(name)
	if !ok {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "wada.json has no service " + name}
	}
	if t := s.roleTerminals(ctx, l.Workspace.Directory)[workspaces.ServiceRole(name)]; t != nil && t.Running {
		return *t, nil
	}
	return s.startRun(ctx, tb, protocol.TerminalCreateRequest{Name: def.Name, Directory: l.Workspace.Directory,
		Command: def.Command, Env: l.Env(def.Name), Role: workspaces.ServiceRole(def.Name)})
}

// stopService ends a running service's terminal.
func (s *Server) stopService(ctx context.Context, id, name string) (any, *protocol.RPCError) {
	tb, rerr := s.terminals()
	if rerr != nil {
		return nil, rerr
	}
	w, ok := s.workspaces.Workspace(id)
	if !ok || w.Archived != 0 {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "no active workspace " + id}
	}
	t := s.roleTerminals(ctx, w.Directory)[workspaces.ServiceRole(name)]
	if t == nil || !t.Running {
		return struct{}{}, nil
	}
	if err := tb.KillTerminal(ctx, t.ID); err != nil {
		return nil, internalErr(err)
	}
	return struct{}{}, nil
}

// nameFromSend names the workspace a message was sent in when it waits to be
// named, in the background: the send has already been answered.
func (s *Server) nameFromSend(session, text string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
		defer cancel()
		page, err := s.cfg.Backend.Sessions(ctx, protocol.SessionQuery{})
		if err != nil {
			return
		}
		for _, sess := range page.Sessions {
			if sess.ID == session {
				_ = s.workspaces.NameFromMessage(ctx, sess.Directory, text, s.cfg.Backend.Generate)
				return
			}
		}
	}()
}
