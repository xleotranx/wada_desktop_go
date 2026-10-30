package pig

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/contract/agent"
)

const resultToolName = "submit_result"

type turnState struct {
	id     string
	schema json.RawMessage
	policy *agent.ToolPolicy
	result json.RawMessage
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
func withoutResult(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == resultToolName })
}
