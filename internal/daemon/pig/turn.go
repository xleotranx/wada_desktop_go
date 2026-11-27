package pig

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/protocol"
)

const (
	resultToolName  = "submit_result"
	projectToolName = "project"
)

type turnState struct {
	id      string
	project *protocol.ProjectScope
	tools   []string
	schema  json.RawMessage
	policy  *protocol.ToolPolicy
	result  json.RawMessage
}

type resultTool struct{ c *conversation }

func (*resultTool) Name() string                        { return resultToolName }
func (*resultTool) Label() string                       { return "Submit turn result" }
func (*resultTool) ExecutionMode() pa.ToolExecutionMode { return pa.ToolModeSequential }
func (t *resultTool) Schema() ai.ToolSchema {
	t.c.turnMu.Lock()
	defer t.c.turnMu.Unlock()
	params := map[string]any{"type": "object"}
	if t.c.turn != nil && len(t.c.turn.schema) > 0 {
		_ = json.Unmarshal(t.c.turn.schema, &params)
	}
	return ai.ToolSchema{Name: resultToolName, Description: "Submit the requested structured result and end this turn. Explain your work before calling this tool.", Parameters: params}
}
func (t *resultTool) Execute(ctx context.Context, _ string, params json.RawMessage, _ pa.ToolUpdateCallback) (pa.AgentToolResult, error) {
	if err := ctx.Err(); err != nil {
		return pa.AgentToolResult{}, err
	}
	t.c.turnMu.Lock()
	defer t.c.turnMu.Unlock()
	turn := t.c.turn
	if turn == nil || len(turn.schema) == 0 || len(turn.result) > 0 {
		return pa.AgentToolResult{}, errors.New("no structured result is pending")
	}
	validated, err := ai.ValidateToolArgumentsJSON(resultToolName, turn.schema, params)
	if err != nil {
		return pa.AgentToolResult{}, err
	}
	turn.result = slices.Clone(validated)
	return pa.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Result recorded."}}, Terminate: true}, nil
}

type projectTool struct {
	c *conversation
	e *engine
}

func (*projectTool) Name() string                        { return projectToolName }
func (*projectTool) Label() string                       { return "Work on the active project" }
func (*projectTool) ExecutionMode() pa.ToolExecutionMode { return pa.ToolModeSequential }
func (*projectTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: projectToolName, Description: "Work only on the active project.", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"inspect", "update", "read", "write", "check"}}, "input": map[string]any{"type": "object"}}, "required": []string{"action"}, "additionalProperties": false}}
}
func (t *projectTool) Execute(ctx context.Context, _ string, params json.RawMessage, _ pa.ToolUpdateCallback) (pa.AgentToolResult, error) {
	if err := ctx.Err(); err != nil {
		return pa.AgentToolResult{}, err
	}
	t.c.turnMu.Lock()
	var scope *protocol.ProjectScope
	if t.c.turn != nil && t.c.turn.project != nil {
		cp := *t.c.turn.project
		scope = &cp
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

func resultFromText(text string, schema json.RawMessage) (json.RawMessage, bool) {
	var candidates []string
	for i := 0; i < len(text); {
		open := strings.Index(text[i:], "```")
		if open < 0 {
			break
		}
		open += i
		rest := text[open+3:]
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		info := strings.TrimSpace(rest[:nl])
		if info != "" && !strings.EqualFold(info, "json") {
			i = open + 3
			continue
		}
		body := open + 3 + nl + 1
		end := strings.Index(text[body:], "```")
		if end < 0 {
			break
		}
		candidates = append(candidates, strings.TrimSpace(text[body:body+end]))
		i = body + end + 3
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		validated, err := ai.ValidateToolArgumentsJSON(resultToolName, schema, json.RawMessage(candidates[i]))
		if err == nil {
			return slices.Clone(validated), true
		}
	}
	return nil, false
}

func withoutTurnTools(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == resultToolName || n == projectToolName })
}
