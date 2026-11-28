package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/preview"
	"wada-desktop-go/internal/protocol"
)

func TestPreviewApprovalRequiresTheLocalDesktopAndRunningService(t *testing.T) {
	data, root := t.TempDir(), t.TempDir()
	terms := &fakeTerminals{}
	broker := &preview.Broker{}
	startTestServer(t, data, func(cfg *Config) {
		terms.Runtime = cfg.Backend.(*pig.Runtime)
		cfg.Backend = terms
		cfg.Preview = broker
	})
	dial := func(kind string) *client.Client {
		t.Helper()
		c, err := client.DialLocal(t.Context(), data, client.Options{ClientType: kind})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	cli, desktop := dial("cli"), dial("desktop")
	if err := os.WriteFile(filepath.Join(root, "wada.json"), []byte(`{"services":{"web":"serve"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	project, err := cli.AddProject(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	id := project.Workspace.ID
	config, err := cli.WorkspaceConfig(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.TrustWorkspaceConfig(t.Context(), id, config.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.StartService(t.Context(), id, "web"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out := make(chan string, 1)
	queue := func() *protocol.PreviewRequest {
		t.Helper()
		go func() {
			text, err := broker.Request(ctx, protocol.PreviewRequest{Workspace: id,
				Service: "web", Origin: "http://127.0.0.1:41000", Action: "read"})
			if err != nil {
				text = err.Error()
			}
			out <- text
		}()
		for ctx.Err() == nil {
			req, err := desktop.PendingPreview(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if req != nil {
				return req
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("desktop did not receive the queued request")
		return nil
	}
	req := queue()
	if _, err := cli.PendingPreview(ctx, id); !isCode(err, protocol.ErrCodeUnauthorized) {
		t.Fatalf("CLI pending preview = %v", err)
	}
	if _, err := desktop.ClaimPreview(ctx, req.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := desktop.ReplyPreview(ctx, protocol.PreviewReply{ID: req.ID, Text: "visible text"}); err != nil {
		t.Fatal(err)
	}
	if got := <-out; got != "visible text" {
		t.Fatalf("first reply: %q", got)
	}
	req = queue()
	if err := cli.StopService(ctx, id, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.ClaimPreview(ctx, req.ID, true); !isCode(err, protocol.ErrCodeConflict) {
		t.Fatalf("approval after stopping the service = %v", err)
	}
	if got := <-out; got != "the workspace preview is no longer running or trusted" {
		t.Fatalf("second reply: %q", got)
	}
}
