package pig

import (
	"context"
	"encoding/json"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Tool is a tool the runtime offers a top-level conversation's model, declared
// without PiG's types so other packages need not import PiG. Execute receives
// the call's JSON arguments, and its result reaches the model as indented JSON.
type Tool struct {
	Name        string
	Label       string
	Description string
	// Parameters is the arguments' JSON Schema.
	Parameters map[string]any
	Execute    func(ctx context.Context, params json.RawMessage) (any, error)
}

// agentTool adapts a Tool to PiG. Calls may run in parallel.
type agentTool struct{ t Tool }

func (a agentTool) Name() string                      { return a.t.Name }
func (a agentTool) Label() string                     { return a.t.Label }
func (agentTool) ExecutionMode() pa.ToolExecutionMode { return pa.ToolModeParallel }
func (a agentTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: a.t.Name, Description: a.t.Description, Parameters: a.t.Parameters}
}

func (a agentTool) Execute(ctx context.Context, _ string, params json.RawMessage, _ pa.ToolUpdateCallback) (pa.AgentToolResult, error) {
	res, err := a.t.Execute(ctx, params)
	if err != nil {
		return pa.AgentToolResult{}, err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return pa.AgentToolResult{}, err
	}
	return pa.AgentToolResult{
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: string(data)}},
	}, nil
}
