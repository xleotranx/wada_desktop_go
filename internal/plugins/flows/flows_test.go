package flows

import (
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"testing"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/plugintest"
)

func TestContributesConversationWorkflowAndCommand(t *testing.T) {
	ctl := &plugintest.Chat{}
	h := kerneltest.New(t, ctl.New, New)
	definitions := kernel.Items(h, wf.Definitions)
	choices := kernel.Items(h, chat.Workflows)
	reviewed := kernel.Items(h, chat.ReviewedWork)
	if len(definitions) != 1 || len(choices) != 1 || len(reviewed) != 1 ||
		definitions[0].Value.MaxReviews != 3 || reviewed[0].ID != "flows.plan-work-review" ||
		reviewed[0].Value.Title == "" {
		t.Fatalf("definitions=%v choices=%v reviewed=%v", definitions, choices, reviewed)
	}
	var selected string
	kernel.On(h, chat.WorkflowSelected, func(id string) { selected = id })
	for _, c := range kernel.Items(h, kernel.Commands) {
		if c.ID == "flows.plan-work-review" {
			c.Value.Run()
		}
	}
	if selected != "flows.plan-work-review" {
		t.Fatalf("selection %q", selected)
	}
}
