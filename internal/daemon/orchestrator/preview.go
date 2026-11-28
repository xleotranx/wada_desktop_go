package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/workspaces"
	"wada-desktop-go/internal/protocol"
)

func (o *Orchestrator) previewTool(getCtx func() pig.ChiefContext) pig.Tool {
	return pig.Tool{
		Name: "workspace_preview", Label: "Use local workspace preview",
		Description: "Ask the local Wada desktop to open, read visible text from, or click a uniquely labeled button in a running, trusted wada.json service of this workspace. Every action needs approval in the desktop. Use value for a relative path (open) or the button's exact text (click); read needs no value.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"service": map[string]any{"type": "string"},
				"action":  map[string]any{"type": "string", "enum": []string{"open", "read", "click"}},
				"value":   map[string]any{"type": "string"},
			},
			"required": []string{"service", "action"},
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			cc := getCtx()
			var input struct {
				Service string `json:"service"`
				Action  string `json:"action"`
				Value   string `json:"value"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			if err := validatePreview(input.Action, input.Value); err != nil {
				return nil, err
			}
			w, err := o.workspaceHere(cc.Dir)
			if err != nil {
				return nil, err
			}
			config, err := o.workspaces.LoadConfig(ctx, w.ID)
			if err != nil {
				return nil, err
			}
			if err := config.Runnable(); err != nil {
				return nil, err
			}
			_, port, known := config.Service(input.Service)
			if !known || port < 1 || port > 65535 || o.terminals == nil {
				return nil, errors.New("choose a running service from the workspace's wada.json")
			}
			terms, err := o.terminals.List(ctx)
			if err != nil {
				return nil, err
			}
			for _, t := range terms {
				if t.Directory == w.Directory && t.Role == workspaces.ServiceRole(input.Service) && t.Running {
					return o.preview.Request(ctx, protocol.PreviewRequest{
						Workspace: w.ID, Session: cc.SessionID, Service: input.Service, Action: input.Action,
						Origin: fmt.Sprintf("http://127.0.0.1:%d", port), Value: input.Value,
					})
				}
			}
			return nil, errors.New("start the workspace service in Wada before opening its preview")
		},
	}
}

func validatePreview(action, value string) error {
	switch action {
	case "open":
		u, err := url.Parse(value)
		if err == nil && len(value) <= 512 && !u.IsAbs() && u.Host == "" &&
			!strings.Contains(value, "\\") && !strings.HasPrefix(u.Path, "//") &&
			(value == "" || strings.HasPrefix(value, "/")) {
			return nil
		}
	case "read":
		if value == "" {
			return nil
		}
	case "click":
		if len(value) <= 160 && strings.TrimSpace(value) != "" {
			return nil
		}
	}
	return errors.New("invalid preview action or value")
}
