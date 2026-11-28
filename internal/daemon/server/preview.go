package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"wada-desktop-go/internal/daemon/workspaces"
	"wada-desktop-go/internal/protocol"
)

func (s *Server) previewRPC(ctx context.Context, c *clientConn, method string, data json.RawMessage) (any, *protocol.RPCError) {
	if !c.previewDesktop || s.cfg.Preview == nil {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeUnauthorized, Message: "preview requests require the local desktop"}
	}
	switch method {
	case protocol.MethodPreviewPending:
		var req protocol.WorkspaceIDRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, invalidErr(err)
		}
		return s.cfg.Preview.Pending(req.Workspace), nil
	case protocol.MethodPreviewClaim:
		var in struct {
			ID    string `json:"id"`
			Allow bool   `json:"allow"`
		}
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, invalidErr(err)
		}
		req := s.cfg.Preview.Claim(in.ID, c.id)
		if req == nil {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "browser request expired or was handled"}
		}
		if !in.Allow {
			s.cfg.Preview.Reply(protocol.PreviewReply{ID: in.ID, Error: "the user declined the browser action"}, c.id)
			return nil, nil
		}
		if !s.previewAvailable(ctx, req) {
			s.cfg.Preview.Reply(protocol.PreviewReply{ID: in.ID, Error: "the workspace preview is no longer running or trusted"}, c.id)
			return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: "the workspace preview is no longer running or trusted"}
		}
		return req, nil
	case protocol.MethodPreviewReply:
		var reply protocol.PreviewReply
		if err := json.Unmarshal(data, &reply); err != nil {
			return nil, invalidErr(err)
		}
		if len(reply.Text) > 32768 || len(reply.Error) > 4096 || strings.TrimSpace(reply.ID) == "" {
			return nil, invalidErr(errors.New("invalid browser reply"))
		}
		if !s.cfg.Preview.Reply(reply, c.id) {
			return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "browser request expired or was handled"}
		}
		return nil, nil
	default:
		return nil, invalidErr(errors.New("unknown browser request"))
	}
}

func (s *Server) previewAvailable(ctx context.Context, req *protocol.PreviewRequest) bool {
	loaded, err := s.workspaces.LoadConfig(ctx, req.Workspace)
	if err != nil || loaded.Runnable() != nil {
		return false
	}
	_, port, ok := loaded.Service(req.Service)
	if !ok || req.Origin != fmt.Sprintf("http://127.0.0.1:%d", port) {
		return false
	}
	t := s.roleTerminals(ctx, loaded.Workspace.Directory)[workspaces.ServiceRole(req.Service)]
	return t != nil && t.Running
}
