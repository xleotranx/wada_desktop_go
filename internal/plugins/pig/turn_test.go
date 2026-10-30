package pig

import (
	"context"
	"encoding/json"
	"testing"

	pa "github.com/MichaelKinsy/PiG/agent"
	"wada-desktop-go/internal/contract/agent"
)

func TestStructuredResultValidatesAndTerminates(t *testing.T) {
	c := &conversation{}
	c.turn = &turnState{schema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["ready"]}},"required":["status"],"additionalProperties":false}`)}
	tool := &resultTool{c: c}
	if _, err := tool.Execute(t.Context(), "bad", json.RawMessage(`{"status":"wrong"}`), nil); err == nil {
		t.Fatal("accepted invalid result")
	}
	got, err := tool.Execute(t.Context(), "ok", json.RawMessage(`{"status":"ready"}`), nil)
	if err != nil || !got.Terminate || string(c.turn.result) != `{"status":"ready"}` {
		t.Fatalf("result=%+v err=%v state=%+v", got, err, c.turn)
	}
	if _, err := tool.Execute(t.Context(), "again", json.RawMessage(`{"status":"ready"}`), nil); err == nil {
		t.Fatal("accepted duplicate result")
	}
	c.turn = nil
	if _, err := tool.Execute(t.Context(), "ordinary", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("result accepted outside structured turn")
	}
}

func TestTurnPolicyCannotBeBypassedByFullAccess(t *testing.T) {
	c := &conversation{rec: record{Mode: modeFull}, allowed: map[string]bool{}}
	c.turn = &turnState{policy: &agent.ToolPolicy{Deny: []agent.ToolKind{agent.ToolEdit}}}
	e := &engine{ctx: context.Background()}
	if got := e.toolHook(c)(t.Context(), "edit", "write", json.RawMessage(`{}`)); !got.Block {
		t.Fatal("full access bypassed turn restriction")
	}
	c.turn = &turnState{result: json.RawMessage(`{}`)}
	if got := e.toolHook(c)(t.Context(), "late", "read", json.RawMessage(`{}`)); !got.Block {
		t.Fatal("tool ran after result submission")
	}
	var _ pa.AgentTool = &resultTool{c: c}
}
