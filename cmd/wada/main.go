// Command wada is the CLI client for wadad.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/protocol"
)

func main() {
	var (
		dataDir    = flag.String("data-dir", "", "Wada data directory (default WADA_DATA_DIR or OS config dir)")
		endpoint   = flag.String("endpoint", "", "Remote wadad endpoint (host:port or ws://.../ws)")
		credential = flag.String("credential", "", "Daemon credential or password")
	)
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	ctx := context.Background()
	c, err := connect(ctx, *dataDir, *endpoint, *credential)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wada: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	switch args[0] {
	case "daemon":
		err = runDaemonCmd(ctx, c, args[1:])
	case "ls":
		err = runList(ctx, c, args[1:])
	case "run":
		err = runPrompt(ctx, c, args[1:])
	case "logs":
		err = runLogs(ctx, c, args[1:])
	case "abort":
		if len(args) < 2 {
			err = fmt.Errorf("usage: wada abort <session-id>")
		} else {
			err = c.Abort(ctx, args[1])
		}
	case "delete":
		if len(args) < 2 {
			err = fmt.Errorf("usage: wada delete <session-id>")
		} else {
			err = c.Delete(ctx, args[1])
		}
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wada: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: wada [flags] <command>

Commands:
  daemon status          Show connected wadad status
  daemon stop            Stop the running wadad daemon
  ls [--dir DIR]         List sessions
  run [--dir DIR] [--mode ask|full] <prompt>
                         Create a session, stream output, and handle approvals
  logs <session-id>      Print stored conversation messages
  abort <session-id>     Abort a running turn
  delete <session-id>    Delete a session`)
}

func connect(ctx context.Context, dataDir, endpoint, credential string) (*client.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	opts := client.Options{
		Endpoint:   endpoint,
		Credential: credential,
		Password:   credential,
		ClientType: "cli",
		AppVersion: "0.1.0-dev",
	}
	if endpoint == "" {
		return client.DialLocal(ctx, dataDir, opts)
	}
	return client.Dial(ctx, opts)
}

func runDaemonCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wada daemon <status|stop>")
	}
	switch args[0] {
	case "status":
		st, err := c.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("serverId: %s\nversion:  %s\npid:      %d\nepoch:    %d\norigin:   %s\n",
			st.ServerID, st.Version, st.PID, st.Epoch, st.Origin)
		return nil
	case "stop":
		return c.Shutdown(ctx)
	default:
		return fmt.Errorf("unknown daemon command %q", args[0])
	}
}

func runList(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	dir := fs.String("dir", "", "Filter by directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	page, err := c.Sessions(ctx, protocol.SessionQuery{Directory: *dir})
	if err != nil {
		return err
	}
	for _, s := range page.Sessions {
		fmt.Printf("%s\t%s\t%s\n", s.ID, s.Directory, s.Title)
	}
	return nil
}

func runLogs(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: wada logs <session-id>")
	}
	msgs, err := c.Messages(ctx, args[0])
	if err != nil {
		return err
	}
	for _, m := range msgs {
		switch m.Role {
		case protocol.RoleUser:
			fmt.Printf("USER: %s\n", m.Text)
		case protocol.RoleAssistant:
			for _, p := range m.Parts {
				switch p.Kind {
				case protocol.PartText:
					fmt.Printf("ASSISTANT: %s\n", p.Text)
				case protocol.PartTool:
					if p.Tool != nil {
						fmt.Printf("TOOL [%s]: %s (%s)\n", p.Tool.Status, p.Tool.Name, p.Tool.Command)
					}
				case protocol.PartReasoning, protocol.PartOther:
				}
			}
		case protocol.RoleOther:
		}
	}
	return nil
}

func runPrompt(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Workspace directory")
	mode := fs.String("mode", "ask", "Access mode: ask or full")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("usage: wada run [--dir DIR] [--mode ask|full] <prompt>")
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	sess, err := c.Create(ctx, protocol.CreateSessionRequest{
		IdempotencyKey: "cli_" + uuid.NewString(),
		Directory:      absDir,
		Mode:           *mode,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "session: %s (%s)\n", sess.ID, sess.Directory)

	subID, ch, err := c.Subscribe(ctx, sess.ID)
	if err != nil {
		return err
	}
	defer func() { _ = c.Unsubscribe(context.Background(), subID) }()

	reqID := "cli_" + uuid.NewString()
	if _, err := c.Send(ctx, protocol.SendRequest{
		ID:        reqID,
		MessageID: reqID,
		Session:   sess.ID,
		Mode:      *mode,
		Text:      prompt,
	}); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	for ev := range ch {
		switch ev.Kind {
		case protocol.EventTextDelta:
			if ev.Text != nil {
				fmt.Print(ev.Text.Delta)
			}
		case protocol.EventTextEnded:
			fmt.Println()
		case protocol.EventToolCalled:
			if ev.Tool != nil {
				fmt.Fprintf(os.Stderr, "[tool] %s %s\n", ev.Tool.Name, ev.Tool.Command)
			}
		case protocol.EventApprovalRequested:
			if ev.Approval != nil {
				fmt.Fprintf(os.Stderr, "Approve %s %v? [y=once / a=always / n=reject]: ", ev.Approval.Action, ev.Approval.Resources)
				line, _ := reader.ReadString('\n')
				dec := protocol.DecisionReject
				switch strings.ToLower(strings.TrimSpace(line)) {
				case "y", "yes":
					dec = protocol.DecisionOnce
				case "a", "always":
					dec = protocol.DecisionAlways
				}
				if err := c.Reply(ctx, protocol.ReplyRequest{
					Session:  ev.Approval.Session,
					Approval: ev.Approval.ID,
					Decision: dec,
				}); err != nil {
					return err
				}
			}
		case protocol.EventTurnSucceeded:
			return nil
		case protocol.EventTurnFailed:
			return fmt.Errorf("turn failed: %s", ev.Error)
		case protocol.EventTurnInterrupted:
			return fmt.Errorf("turn interrupted")
		case protocol.EventTextStarted, protocol.EventToolStarted, protocol.EventToolFinished,
			protocol.EventInteractionRequested, protocol.EventTurnStarted, protocol.EventModelChanged,
			protocol.EventSessionRenamed, protocol.EventSessionDeleted, protocol.EventModelsChanged:
		}
	}
	return nil
}
