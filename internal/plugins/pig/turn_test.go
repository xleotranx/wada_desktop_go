package pig

import (
	"context"
	"encoding/json"
	"strings"
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

func TestResultFromTextFindsTheLastValidBlock(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["ready"]},"summary":{"type":"string"}},"required":["status","summary"],"additionalProperties":false}`)
	text := "Here is the plan.\n\n```json\n{\"status\":\"wrong\"}\n```\n\nFixed:\n```json\n{\"status\":\"ready\",\"summary\":\"planned\"}\n```"
	got, ok := resultFromText(text, schema)
	if !ok || !strings.Contains(string(got), "planned") {
		t.Fatalf("result %q ok=%v", got, ok)
	}
	if _, ok := resultFromText("no result here", schema); ok {
		t.Fatal("found a result in prose")
	}
	if _, ok := resultFromText("```json\n{\"status\":\"wrong\"}\n```", schema); ok {
		t.Fatal("accepted an invalid block")
	}
}
