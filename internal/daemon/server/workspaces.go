package server

import (
	"context"
	"encoding/json"
	"errors"

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
		p, w, err := s.workspaces.AddProject(ctx, req.Root)
		if err != nil {
			return nil, workspaceErr(err)
		}
		return protocol.ProjectAddResponse{Project: p, Workspace: w}, nil
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
	}
	return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "unknown method " + method}
}

func workspaceErr(err error) *protocol.RPCError {
	var pending *workspaces.PendingError
	switch {
	case errors.As(err, &pending):
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
