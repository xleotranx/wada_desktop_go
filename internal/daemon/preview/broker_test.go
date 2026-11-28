package preview

import (
	"context"
	"testing"
	"time"

	"wada-desktop-go/internal/protocol"
)

func TestPreviewStaysInItsWorkspaceAndHasOneOwner(t *testing.T) {
	var b Broker
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		text, _ := b.Request(ctx, protocol.PreviewRequest{Workspace: "one", Action: "read"})
		done <- text
	}()
	var req *protocol.PreviewRequest
	for req == nil && ctx.Err() == nil {
		req = b.Pending("one")
		time.Sleep(time.Millisecond)
	}
	if req == nil || b.Pending("two") != nil {
		t.Fatal("the pending preview was absent or leaked to the wrong workspace")
	}
	if b.Claim(req.ID, "desktop-a") == nil || b.Claim(req.ID, "desktop-b") != nil {
		t.Fatal("the request was not claimed exactly once")
	}
	reply := protocol.PreviewReply{ID: req.ID, Text: "preview text"}
	if b.Reply(reply, "desktop-b") || !b.Reply(reply, "desktop-a") {
		t.Fatal("reply was accepted from the wrong owner")
	}
	select {
	case got := <-done:
		if got != reply.Text {
			t.Fatalf("reply = %q", got)
		}
	case <-ctx.Done():
		t.Fatal("the request did not finish")
	}
}
