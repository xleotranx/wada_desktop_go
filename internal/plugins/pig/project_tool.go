package pig

import (
	"context"
	"encoding/json"
	"errors"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
)

const projectToolName = "project"

type projectTool struct {
	c *conversation
	e *engine
}

func (*projectTool) Name() string                        { return projectToolName }
func (*projectTool) Label() string                       { return "Work on the active project" }
func (*projectTool) ExecutionMode() pa.ToolExecutionMode { return pa.ToolModeSequential }
func (*projectTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: projectToolName, Description: "Work only on the active project. Start with inspect to see its content and available components. update edits structured content; read/write access project source files; check validates. The host selects the project directory. App internals and other projects are inaccessible.", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"inspect", "update", "read", "write", "check"}}, "input": map[string]any{"type": "object"}}, "required": []string{"action"}, "additionalProperties": false}}
}
func (t *projectTool) Execute(ctx context.Context, _ string, params json.RawMessage, _ pa.ToolUpdateCallback) (pa.AgentToolResult, error) {
	if err := ctx.Err(); err != nil {
		return pa.AgentToolResult{}, err
	}
	t.c.turnMu.Lock()
	var scope *agent.ProjectScope
	if t.c.turn != nil && t.c.turn.project != nil {
		copy := *t.c.turn.project
		scope = &copy
	}
	t.c.turnMu.Unlock()
	if scope == nil || t.e.projectTool == nil {
		return pa.AgentToolResult{}, errors.New("no project is active for this turn")
	}
	var call struct {
		Action string          `json:"action"`
		Input  json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return pa.AgentToolResult{}, err
	}
	tool, err := t.e.projectTool(ctx, scope.Kind)
	if err != nil {
		return pa.AgentToolResult{}, err
	}
	output, err := tool.Execute(ctx, scope.Directory, call.Action, call.Input)
	if err != nil {
		return pa.AgentToolResult{}, err
	}
	return pa.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output}}}, nil
}

// Resolve contributions on the UI thread; execute their stateless operations
// on the calling worker. SDK types never cross the backend seam.
func projectResolver(c *kernel.Context, run *kernel.Scope) func(context.Context, string) (agent.ProjectTool, error) {
	return func(ctx context.Context, kind string) (agent.ProjectTool, error) {
		result := make(chan agent.ProjectTool, 1)
		run.Post(func() {
			var found agent.ProjectTool
			for _, entry := range kernel.Items(c, agent.ProjectTools) {
				if entry.Value.Kind == kind {
					found = entry.Value
					break
				}
			}
			result <- found
		})
		select {
		case <-ctx.Done():
			return agent.ProjectTool{}, ctx.Err()
		case tool := <-result:
			if tool.Execute == nil {
				return agent.ProjectTool{}, errors.New("the project tools are unavailable")
			}
			return tool, nil
		}
	}
}
