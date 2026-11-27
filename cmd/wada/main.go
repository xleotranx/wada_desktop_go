// Command wada is the CLI client for wadad.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"
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
	case "providers":
		err = runProviders(ctx, c)
	case "workers":
		err = runWorkers(ctx, c, args[1:])
	case "terminal":
		err = runTerminalCmd(ctx, c, args[1:])
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
  providers              List embedded and external coding-agent harnesses
  workers [--parent ID]  List managed external harness workers
  terminal <ls|create|attach|capture|kill>
                         Manage detached interactive PTY terminals
  ls [--dir DIR]         List sessions
  run [--dir DIR] [--provider ID] [--mode ask|full] <prompt>
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

func runProviders(ctx context.Context, c *client.Client) error {
	list, err := c.Harnesses(ctx)
	if err != nil {
		return err
	}
	for _, h := range list {
		avail := "unavailable"
		if h.Available {
			avail = "available"
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", h.ID, h.Kind, avail, h.Name)
	}
	return nil
}

func runWorkers(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("workers", flag.ContinueOnError)
	parent := fs.String("parent", "", "Filter by parent session ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list, err := c.Workers(ctx, *parent)
	if err != nil {
		return err
	}
	for _, w := range list {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", w.ID, w.Provider, w.Status, w.Directory, w.Name)
	}
	return nil
}

func runTerminalCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wada terminal <ls|create|attach|capture|kill>")
	}
	switch args[0] {
	case "ls":
		list, err := c.Terminals(ctx)
		if err != nil {
			return err
		}
		for _, t := range list {
			fmt.Printf("%s\t%s\t%dx%d\trunning=%v\t%s\n", t.ID, t.Name, t.Cols, t.Rows, t.Running, t.Directory)
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("terminal create", flag.ContinueOnError)
		dir := fs.String("dir", ".", "Working directory")
		sh := fs.String("shell", "", "Shell executable")
		name := fs.String("name", "", "Terminal name")
		cols := fs.Int("cols", 80, "Columns")
		rows := fs.Int("rows", 24, "Rows")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		absDir, err := filepath.Abs(*dir)
		if err != nil {
			return err
		}
		info, err := c.CreateTerminal(ctx, protocol.TerminalCreateRequest{
			Name:      *name,
			Directory: absDir,
			Shell:     *sh,
			Cols:      *cols,
			Rows:      *rows,
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", info.ID, info.Name, info.Directory)
		return nil
	case "capture":
		fs := flag.NewFlagSet("terminal capture", flag.ContinueOnError)
		lines := fs.Int("lines", 200, "Maximum lines")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() < 1 {
			return fmt.Errorf("usage: wada terminal capture [--lines N] <id>")
		}
		resp, err := c.CaptureTerminal(ctx, fs.Arg(0), *lines)
		if err != nil {
			return err
		}
		fmt.Println(resp.Text)
		return nil
	case "kill":
		if len(args) < 2 {
			return fmt.Errorf("usage: wada terminal kill <id>")
		}
		return c.KillTerminal(ctx, args[1])
	case "attach":
		if len(args) < 2 {
			return fmt.Errorf("usage: wada terminal attach <id>")
		}
		cols, rows := 80, 24
		inFD := int(os.Stdin.Fd())
		if term.IsTerminal(inFD) {
			if w, h, err := term.GetSize(inFD); err == nil {
				cols, rows = w, h
			}
		}
		stream, err := c.AttachTerminal(ctx, protocol.TerminalAttachRequest{ID: args[1], Cols: cols, Rows: rows})
		if err != nil {
			return err
		}
		defer stream.Close()

		if term.IsTerminal(inFD) {
			oldState, err := term.MakeRaw(inFD)
			if err == nil {
				defer func() { _ = term.Restore(inFD, oldState) }()
			}
		}
		if snap := stream.Snapshot(); len(snap) > 0 {
			_, _ = os.Stdout.Write(snap)
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			for chunk := range stream.Output() {
				_, _ = os.Stdout.Write(chunk)
			}
		}()

		buf := make([]byte, 1024)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				// Detach on Ctrl+] (0x1d).
				if idx := strings.IndexByte(string(buf[:n]), 0x1d); idx >= 0 {
					if idx > 0 {
						_ = stream.WriteInput(buf[:idx])
					}
					return nil
				}
				if werr := stream.WriteInput(buf[:n]); werr != nil {
					return werr
				}
			}
			if rerr != nil {
				if rerr == io.EOF {
					<-done
					return nil
				}
				return rerr
			}
		}
	default:
		return fmt.Errorf("unknown terminal command %q", args[0])
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
	provider := fs.String("provider", "", "Agent provider/harness ID (default embedded PiG)")
	mode := fs.String("mode", "ask", "Access mode: ask or full")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("usage: wada run [--dir DIR] [--provider ID] [--mode ask|full] <prompt>")
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	sess, err := c.Create(ctx, protocol.CreateSessionRequest{
		IdempotencyKey: "cli_" + uuid.NewString(),
		Directory:      absDir,
		Provider:       *provider,
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
			protocol.EventSessionRenamed, protocol.EventSessionDeleted, protocol.EventModelsChanged,
			protocol.EventProviderAuth, protocol.EventWorkerUpdated, protocol.EventTerminalUpdated:
		}
	}
	return nil
}
