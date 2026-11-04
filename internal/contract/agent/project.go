package agent

import (
	"context"
	"encoding/json"

	"github.com/xleotranx/wada-kit/kernel"
)

// ProjectScope limits a turn to the project tool contributed for Kind. It is
// serializable so queued messages and workflow restarts retain their scope.
type ProjectScope struct {
	Kind      string
	Directory string
}

// ProjectTools are worker-safe project operations. Execute must not access UI
// state; Directory is the host-selected project, never a model-supplied path.
var ProjectTools = kernel.NewPoint[ProjectTool]("agent.project-tools")

type ProjectTool struct {
	Kind         string
	Instructions string // optional base assistant behavior for this tool family
	Execute      func(context.Context, string, string, json.RawMessage) (string, error)
}
