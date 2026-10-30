package chat

import (
	"testing"

	"wada-desktop-go/internal/contract/chat"

	"github.com/xleotranx/wada-kit/kernel"
)

func TestNewChatsKeepTheirDraftIdentitiesWhenCreationFinishesOutOfOrder(t *testing.T) {
	h, p, backend, _ := start(t)
	backend.Numbered = true
	var outgoing []chat.Draft
	kernel.Transform(h, chat.Outgoing, 0, func(d chat.Draft) (chat.Draft, func(), error) {
		outgoing = append(outgoing, d)
		return d, nil, nil
	})
	var started []chat.Conversation
	kernel.On(h, chat.Started, func(c chat.Conversation) { started = append(started, c) })
	p.NewChat("/first")
	first := p.conv.DraftID
	p.draft = "first"
	p.send()
	p.NewChat("/second")
	second := p.conv.DraftID
	p.draft = "second"
	p.send()
	if first == 0 || first == second || len(outgoing) != 2 || outgoing[0].DraftID != first || outgoing[1].DraftID != second {
		t.Fatalf("draft identities %d, %d; outgoing %+v", first, second, outgoing)
	}
	h.RunWork(1) // The second Create returns before the first one.
	h.RunPosts()
	if p.conv.DraftID != second || p.conv.Session == "" {
		t.Fatalf("the current chat lost its identity: %+v", p.conv)
	}
	h.Flush()
	if len(started) != 2 || started[0].DraftID != second || started[0].Directory != "/second" ||
		started[1].DraftID != first || started[1].Directory != "/first" {
		t.Fatalf("started %+v", started)
	}
}
