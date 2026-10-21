package opencode

import "wada-desktop-go/internal/agent"

func normalizeOutput(parts []ToolOutput) []agent.ToolOutput {
	out := make([]agent.ToolOutput, len(parts))
	for i, p := range parts {
		out[i] = agent.ToolOutput{Type: p.Type, Text: p.Text}
	}
	return out
}
func (m storedMessage) normalized() agent.Message {
	text := m.Text
	if m.Payload.Text != "" {
		text = m.Payload.Text
	}
	out := agent.Message{ID: m.ID, Type: m.Type, Text: text, Cost: m.Cost, Tokens: agent.TokenSpend{Output: m.Tokens.Output, Reasoning: m.Tokens.Reasoning}, Model: agent.ModelRef{ID: m.Model.ID, ProviderID: m.Model.ProviderID, Variant: m.Model.Variant}}
	for _, p := range m.Content {
		out.Content = append(out.Content, agent.Part{Type: p.Type, Text: p.Text, ID: p.ID, Name: p.Name, Command: p.State.Input.Command, Output: normalizeOutput(p.State.Content), Exit: p.State.Metadata.Exit, Truncated: p.State.Metadata.Truncated})
	}
	return out
}

var _ agent.Client = (*Client)(nil)
