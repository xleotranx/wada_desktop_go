package server

import (
	"context"
	"encoding/json"
	"errors"

	"wada-desktop-go/internal/daemon/schedules"
	"wada-desktop-go/internal/protocol"
)

// scheduleRunner is the daemon as the schedules' runs see it.
type scheduleRunner struct{ s *Server }

func (r scheduleRunner) Create(ctx context.Context, req protocol.CreateSessionRequest) (protocol.Session, error) {
	if req.Workspace != "" {
		dir, rerr := r.s.workspaceDir(req.Workspace, req.Directory)
		if rerr != nil {
			return protocol.Session{}, rerr
		}
		req.Directory, req.Workspace = dir, ""
	}
	return r.s.cfg.Backend.Create(ctx, req)
}

func (r scheduleRunner) Send(ctx context.Context, req protocol.SendRequest) (protocol.SendDisposition, error) {
	return r.s.cfg.Backend.Send(ctx, req)
}

func (r scheduleRunner) Running(session string) bool { return r.s.turnRunning(session) }

// turnRunning reports that a conversation's turn has started and not ended.
func (s *Server) turnRunning(session string) bool {
	s.turnsMu.Lock()
	defer s.turnsMu.Unlock()
	return s.turns[session]
}

// trackTurn follows which conversations have a turn going.
func (s *Server) trackTurn(ev protocol.Event) {
	if ev.Session == "" {
		return
	}
	s.turnsMu.Lock()
	defer s.turnsMu.Unlock()
	switch ev.Kind {
	case protocol.EventTurnStarted:
		s.turns[ev.Session] = true
	case protocol.EventTurnSucceeded, protocol.EventTurnFailed, protocol.EventTurnInterrupted, protocol.EventSessionDeleted:
		delete(s.turns, ev.Session)
	case protocol.EventTextStarted, protocol.EventTextDelta, protocol.EventTextEnded, protocol.EventToolStarted,
		protocol.EventToolCalled, protocol.EventToolFinished, protocol.EventApprovalRequested,
		protocol.EventInteractionRequested, protocol.EventModelChanged, protocol.EventSessionRenamed,
		protocol.EventModelsChanged, protocol.EventProviderAuth, protocol.EventWorkerUpdated,
		protocol.EventTerminalUpdated, protocol.EventWorkspaceUpdated, protocol.EventScheduleUpdated:
	}
}

func scheduleErr(err error) *protocol.RPCError {
	if errors.Is(err, schedules.ErrNotFound) {
		return &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: err.Error()}
	}
	return internalErr(err)
}

// handleSchedules answers the schedules.* methods; ok is false for others.
func (s *Server) handleSchedules(ctx context.Context, method string, payload json.RawMessage) (any, *protocol.RPCError, bool) {
	switch method {
	case protocol.MethodSchedulesList:
		return s.schedules.List(), nil, true
	case protocol.MethodScheduleCreate:
		var spec protocol.ScheduleSpec
		if err := json.Unmarshal(payload, &spec); err != nil {
			return nil, invalidErr(err), true
		}
		if _, err := schedules.Validate(spec); err != nil {
			return nil, invalidErr(err), true
		}
		sc, err := s.schedules.Create(ctx, spec)
		if err != nil {
			return nil, internalErr(err), true
		}
		return sc, nil, true
	case protocol.MethodScheduleUpdate:
		var req protocol.ScheduleUpdateRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err), true
		}
		if _, err := schedules.Validate(req.Spec); err != nil {
			return nil, invalidErr(err), true
		}
		sc, err := s.schedules.Update(ctx, req.ID, req.Spec)
		if err != nil {
			return nil, scheduleErr(err), true
		}
		return sc, nil, true
	case protocol.MethodScheduleDelete, protocol.MethodScheduleRun:
		var req protocol.ScheduleIDRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err), true
		}
		var err error
		if method == protocol.MethodScheduleDelete {
			err = s.schedules.Delete(ctx, req.ID)
		} else {
			err = s.schedules.RunNow(req.ID)
		}
		if err != nil {
			return nil, scheduleErr(err), true
		}
		return map[string]bool{"ok": true}, nil, true
	}
	return nil, nil, false
}
