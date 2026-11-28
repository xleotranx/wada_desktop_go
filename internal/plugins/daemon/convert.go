package daemon

import (
	"slices"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/terminal"
	"wada-desktop-go/internal/protocol"
)

func toProtoModelRef(m agent.ModelRef) protocol.ModelRef {
	return protocol.ModelRef{Provider: m.Provider, ID: m.ID, Variant: m.Variant}
}

func fromProtoModelRef(m protocol.ModelRef) agent.ModelRef {
	return agent.ModelRef{Provider: m.Provider, ID: m.ID, Variant: m.Variant}
}

func fromProtoModel(m protocol.Model) agent.Model {
	return agent.Model{
		Ref:      fromProtoModelRef(m.Ref),
		Name:     m.Name,
		Variants: slices.Clone(m.Variants),
		Free:     m.Free,
		Context:  m.Context,
	}
}

func fromProtoSession(s protocol.Session) agent.Session {
	return agent.Session{
		ID:        agent.SessionID(s.ID),
		Title:     s.Title,
		Directory: s.Directory,
		Provider:  s.Provider,
		Updated:   s.Updated,
		Model:     fromProtoModelRef(s.Model),
		Cost:      s.Cost,
	}
}

func fromProtoHarnessKind(k protocol.HarnessKind) agent.HarnessKind {
	switch k {
	case protocol.HarnessEmbedded:
		return agent.HarnessEmbedded
	case protocol.HarnessACP:
		return agent.HarnessACP
	case protocol.HarnessClaude:
		return agent.HarnessClaude
	case protocol.HarnessCodex:
		return agent.HarnessCodex
	}
	return agent.HarnessACP
}

func fromProtoHarness(h protocol.HarnessInfo) agent.Harness {
	return agent.Harness{
		ID:          h.ID,
		Name:        h.Name,
		Kind:        fromProtoHarnessKind(h.Kind),
		Binary:      h.Binary,
		Available:   h.Available,
		Description: h.Description,
	}
}

func fromProtoWorkerStatus(st protocol.WorkerStatus) agent.WorkerStatus {
	switch st {
	case protocol.WorkerIdle:
		return agent.WorkerIdle
	case protocol.WorkerRunning:
		return agent.WorkerRunning
	case protocol.WorkerWaitingApproval:
		return agent.WorkerWaitingApproval
	case protocol.WorkerCompleted:
		return agent.WorkerCompleted
	case protocol.WorkerFailed:
		return agent.WorkerFailed
	case protocol.WorkerStopped:
		return agent.WorkerStopped
	}
	return agent.WorkerIdle
}

func fromProtoWorker(w protocol.WorkerInfo) agent.Worker {
	return agent.Worker{
		ID:            agent.SessionID(w.ID),
		ParentSession: agent.SessionID(w.ParentSession),
		Provider:      w.Provider,
		Name:          w.Name,
		Directory:     w.Directory,
		Branch:        w.Branch,
		WorktreePath:  w.WorktreePath,
		Mode:          w.Mode,
		Status:        fromProtoWorkerStatus(w.Status),
		LastError:     w.LastError,
		Updated:       w.Updated,
	}
}

func fromProtoWorktree(wt protocol.WorktreeInfo) agent.Worktree {
	return agent.Worktree{
		RepoRoot:   wt.RepoRoot,
		Path:       wt.Path,
		Branch:     wt.Branch,
		HeadCommit: wt.HeadCommit,
		IsMain:     wt.IsMain,
		Dirty:      wt.Dirty,
		PR:         fromProtoPRStatus(wt.PR),
	}
}

func fromProtoPRStatus(pr *protocol.PRStatus) *agent.PRStatus {
	if pr == nil {
		return nil
	}
	return &agent.PRStatus{
		Number:         pr.Number,
		Title:          pr.Title,
		State:          pr.State,
		Draft:          pr.Draft,
		URL:            pr.URL,
		Checks:         pr.Checks,
		ReviewDecision: pr.ReviewDecision,
	}
}

func toProtoToolKind(k agent.ToolKind) protocol.ToolKind {
	switch k {
	case agent.ToolShell:
		return protocol.ToolShell
	case agent.ToolRead:
		return protocol.ToolRead
	case agent.ToolSearch:
		return protocol.ToolSearch
	case agent.ToolEdit:
		return protocol.ToolEdit
	case agent.ToolFetch:
		return protocol.ToolFetch
	case agent.ToolOther:
		return protocol.ToolOther
	}
	return protocol.ToolOther
}

func fromProtoToolKind(k protocol.ToolKind) agent.ToolKind {
	switch k {
	case protocol.ToolShell:
		return agent.ToolShell
	case protocol.ToolRead:
		return agent.ToolRead
	case protocol.ToolSearch:
		return agent.ToolSearch
	case protocol.ToolEdit:
		return agent.ToolEdit
	case protocol.ToolFetch:
		return agent.ToolFetch
	case protocol.ToolOther:
		return agent.ToolOther
	}
	return agent.ToolOther
}

func toProtoToolPolicy(p *agent.ToolPolicy) *protocol.ToolPolicy {
	if p == nil {
		return nil
	}
	out := &protocol.ToolPolicy{
		Allow: make([]protocol.ToolKind, len(p.Allow)),
		Deny:  make([]protocol.ToolKind, len(p.Deny)),
	}
	for i, k := range p.Allow {
		out.Allow[i] = toProtoToolKind(k)
	}
	for i, k := range p.Deny {
		out.Deny[i] = toProtoToolKind(k)
	}
	return out
}

func toProtoFile(f agent.File) protocol.File {
	return protocol.File{
		Name: f.Name,
		MIME: f.MIME,
		Data: f.Data,
	}
}

func fromProtoFile(f protocol.File) agent.File {
	return agent.File{
		Name: f.Name,
		MIME: f.MIME,
		Data: f.Data,
	}
}

func toProtoDecision(d agent.Decision) protocol.Decision {
	switch d {
	case agent.Once:
		return protocol.DecisionOnce
	case agent.Always:
		return protocol.DecisionAlways
	case agent.Reject:
		return protocol.DecisionReject
	}
	return protocol.DecisionReject
}

func fromProtoDisposition(d protocol.SendDisposition) agent.SendDisposition {
	switch d {
	case protocol.SendAccepted:
		return agent.SendAccepted
	case protocol.SendQueued:
		return agent.SendQueued
	case protocol.SendHandled:
		return agent.SendHandled
	}
	return agent.SendAccepted
}

func fromProtoRole(r protocol.Role) agent.Role {
	switch r {
	case protocol.RoleUser:
		return agent.RoleUser
	case protocol.RoleAssistant:
		return agent.RoleAssistant
	case protocol.RoleOther:
		return agent.RoleOther
	}
	return agent.RoleOther
}

func fromProtoPartKind(k protocol.PartKind) agent.PartKind {
	switch k {
	case protocol.PartText:
		return agent.PartText
	case protocol.PartReasoning:
		return agent.PartReasoning
	case protocol.PartTool:
		return agent.PartTool
	case protocol.PartOther:
		return agent.PartOther
	}
	return agent.PartOther
}

func fromProtoTool(t *protocol.Tool) *agent.Tool {
	if t == nil {
		return nil
	}
	return &agent.Tool{
		ID:        t.ID,
		Name:      t.Name,
		Kind:      fromProtoToolKind(t.Kind),
		Command:   t.Command,
		Output:    slices.Clone(t.Output),
		Status:    t.Status,
		Exit:      t.Exit,
		Truncated: t.Truncated,
	}
}

func fromProtoMessage(m protocol.Message) agent.Message {
	files := make([]agent.File, len(m.Files))
	for i, f := range m.Files {
		files[i] = fromProtoFile(f)
	}
	parts := make([]agent.Part, len(m.Parts))
	for i, p := range m.Parts {
		parts[i] = agent.Part{
			Kind: fromProtoPartKind(p.Kind),
			Text: p.Text,
			Tool: fromProtoTool(p.Tool),
		}
	}
	return agent.Message{
		ID:    agent.MessageID(m.ID),
		Role:  fromProtoRole(m.Role),
		Text:  m.Text,
		Files: files,
		Parts: parts,
		Model: fromProtoModelRef(m.Model),
		Cost:  m.Cost,
		Tokens: agent.Tokens{
			Input:      m.Tokens.Input,
			Output:     m.Tokens.Output,
			Reasoning:  m.Tokens.Reasoning,
			CacheRead:  m.Tokens.CacheRead,
			CacheWrite: m.Tokens.CacheWrite,
		},
	}
}

func fromProtoFileStatus(s protocol.FileStatus) agent.FileStatus {
	switch s {
	case protocol.FileAdded:
		return agent.FileAdded
	case protocol.FileDeleted:
		return agent.FileDeleted
	case protocol.FileModified:
		return agent.FileModified
	}
	return agent.FileModified
}

func fromProtoChanges(in []protocol.FileChange) []agent.FileChange {
	out := make([]agent.FileChange, len(in))
	for i, c := range in {
		out[i] = agent.FileChange{
			Path:    c.Path,
			Status:  fromProtoFileStatus(c.Status),
			Added:   c.Added,
			Removed: c.Removed,
			Patch:   c.Patch,
		}
	}
	return out
}

func fromProtoInteractionKind(k protocol.InteractionKind) agent.InteractionKind {
	switch k {
	case protocol.InteractionConfirm:
		return agent.Confirm
	case protocol.InteractionSelect:
		return agent.Select
	case protocol.InteractionInput:
		return agent.Input
	case protocol.InteractionEditor:
		return agent.Editor
	}
	return agent.Confirm
}

func fromProtoInteraction(in *protocol.Interaction) *agent.Interaction {
	if in == nil {
		return nil
	}
	questions := make([]agent.Question, len(in.Questions))
	for i, q := range in.Questions {
		opts := make([]agent.QuestionOption, len(q.Options))
		for j, o := range q.Options {
			opts[j] = agent.QuestionOption{
				Value:       o.Value,
				Label:       o.Label,
				Description: o.Description,
			}
		}
		questions[i] = agent.Question{
			Key:         q.Key,
			Title:       q.Title,
			Description: q.Description,
			Options:     opts,
			Multiple:    q.Multiple,
			Custom:      q.Custom,
		}
	}
	return &agent.Interaction{
		Session:     agent.SessionID(in.Session),
		Questions:   questions,
		Settled:     in.Settled,
		ID:          agent.InteractionID(in.ID),
		Kind:        fromProtoInteractionKind(in.Kind),
		Title:       in.Title,
		Message:     in.Message,
		Options:     slices.Clone(in.Options),
		Placeholder: in.Placeholder,
		Prefill:     in.Prefill,
	}
}

func fromProtoApproval(a *protocol.Approval) *agent.Approval {
	if a == nil {
		return nil
	}
	return &agent.Approval{
		Session:   agent.SessionID(a.Session),
		ID:        agent.ApprovalID(a.ID),
		Kind:      fromProtoToolKind(a.Kind),
		Action:    a.Action,
		Resources: slices.Clone(a.Resources),
		Save:      slices.Clone(a.Save),
	}
}

func fromProtoEvent(ev protocol.Event) agent.Event {
	out := agent.Event{
		Session: agent.SessionID(ev.Session),
		Model:   fromProtoModelRef(ev.Model),
		Title:   ev.Title,
		Error:   ev.Error,
		Bytes:   128 + len(ev.Error) + len(ev.Title),
	}
	if ev.Text != nil {
		out.Text = &agent.TextEvent{
			Message: agent.MessageID(ev.Text.Message),
			Ordinal: ev.Text.Ordinal,
			Delta:   ev.Text.Delta,
			Text:    ev.Text.Text,
		}
		out.Bytes += len(ev.Text.Delta) + len(ev.Text.Text)
	}
	if ev.Tool != nil {
		out.Tool = fromProtoTool(ev.Tool)
		out.Bytes += len(ev.Tool.Command)
		for _, line := range ev.Tool.Output {
			out.Bytes += len(line)
		}
	}
	if ev.Approval != nil {
		out.Approval = fromProtoApproval(ev.Approval)
	}
	if ev.Interaction != nil {
		out.Interaction = fromProtoInteraction(ev.Interaction)
	}

	switch ev.Kind {
	case protocol.EventTextStarted:
		out.Kind = agent.TextStarted
	case protocol.EventTextDelta:
		out.Kind = agent.TextDelta
	case protocol.EventTextEnded:
		out.Kind = agent.TextEnded
	case protocol.EventToolStarted:
		out.Kind = agent.ToolStarted
	case protocol.EventToolCalled:
		out.Kind = agent.ToolCalled
	case protocol.EventToolFinished:
		out.Kind = agent.ToolFinished
	case protocol.EventApprovalRequested:
		out.Kind = agent.ApprovalRequested
	case protocol.EventInteractionRequested:
		out.Kind = agent.InteractionRequested
	case protocol.EventTurnStarted:
		out.Kind = agent.TurnStarted
	case protocol.EventTurnSucceeded:
		out.Kind = agent.TurnSucceeded
	case protocol.EventTurnFailed:
		out.Kind = agent.TurnFailed
	case protocol.EventTurnInterrupted:
		out.Kind = agent.TurnInterrupted
	case protocol.EventModelChanged:
		out.Kind = agent.ModelChanged
	case protocol.EventSessionRenamed:
		out.Kind = agent.SessionRenamed
	case protocol.EventSessionDeleted, protocol.EventModelsChanged, protocol.EventProviderAuth,
		protocol.EventWorkerUpdated, protocol.EventTerminalUpdated:
	}
	return out
}

func fromProtoTerminal(t protocol.TerminalInfo) terminal.Info {
	var created time.Time
	if t.CreatedAt > 0 {
		created = time.UnixMilli(t.CreatedAt)
	}
	return terminal.Info{
		ID:        t.ID,
		Name:      t.Name,
		Directory: t.Directory,
		Shell:     t.Shell,
		Cols:      t.Cols,
		Rows:      t.Rows,
		PID:       t.PID,
		Running:   t.Running,
		ExitCode:  t.ExitCode,
		CreatedAt: created,
	}
}
