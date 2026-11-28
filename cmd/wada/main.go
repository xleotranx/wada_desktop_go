// Command wada is the CLI client for wadad.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
		hostName   = flag.String("host", "", "Connect to a saved remote host profile by name")
		offer      = flag.String("offer", "", "Connect via pairing offer token or #offer= URL")
		endpoint   = flag.String("endpoint", "", "Remote wadad endpoint (host:port or ws://.../ws)")
		credential = flag.String("credential", "", "Daemon credential or password")
		relayToken = flag.String("relay-token", os.Getenv("WADA_RELAY_TOKEN"), "Optional Bearer token for wada-relay")
	)
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	ctx := context.Background()
	if args[0] == "host" {
		if err := runHostCmd(*dataDir, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "wada: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// A remote daemon resolves folders on its own machine, so they are passed
	// as typed rather than against this one's working directory.
	remote := *hostName != "" || *offer != "" || *endpoint != ""
	c, err := connect(ctx, *dataDir, *hostName, *offer, *endpoint, *credential, *relayToken)
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
	case "pr":
		err = runPRCmd(ctx, c, remote, args[1:])
	case "terminal":
		err = runTerminalCmd(ctx, c, remote, args[1:])
	case "project":
		err = runProjectCmd(ctx, c, remote, args[1:])
	case "workspace":
		err = runWorkspaceCmd(ctx, c, remote, args[1:])
	case "ls":
		err = runList(ctx, c, args[1:])
	case "run":
		err = runPrompt(ctx, c, remote, args[1:])
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
  daemon pair            Print a pairing offer & #offer= link for remote/mobile clients
  daemon set-password    Configure or clear the remote daemon password
  host <ls|add|remove>   Manage saved remote wadad host profiles
  providers              List embedded and external coding-agent harnesses
  workers [--parent ID]  List managed external harness workers
  pr <status|create>     Inspect or create GitHub pull requests via gh
  terminal <ls|create|attach|capture|kill>
                         Manage detached interactive PTY terminals
  project <ls|add|rename|rm>
                         List, add, rename or remove projects
  workspace <ls|create|rename|archive|restore|config|trust|setup|start|stop|commit|pr>
                         Manage workspaces; create takes --project or --dir, and
                         --base REF, --checkout BRANCH, --pr N or --existing PATH;
                         config, trust, setup, start and stop run its wada.json;
                         commit and pr write their message and text with the model
  ls [--dir DIR | --workspace ID]
                         List sessions
  run [--dir DIR | --workspace ID | --new-worktree [--base REF ...]]
      [--provider ID] [--mode ask|full] <prompt>
                         Create a session, stream output, and handle approvals
  logs <session-id>      Print stored conversation messages
  abort <session-id>     Abort a running turn
  delete <session-id>    Delete a session`)
}

func connect(ctx context.Context, dataDir, hostName, offer, endpoint, credential, relayToken string) (*client.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	opts := client.Options{
		Endpoint:       endpoint,
		Credential:     credential,
		Password:       credential,
		RelayAuthToken: relayToken,
		ClientType:     "cli",
		AppVersion:     "0.1.0-dev",
	}
	switch {
	case offer != "":
		return client.DialOffer(ctx, offer, opts)
	case hostName != "":
		return client.DialHost(ctx, dataDir, hostName, opts)
	case endpoint != "":
		return client.Dial(ctx, opts)
	default:
		return client.DialLocal(ctx, dataDir, opts)
	}
}

// workDir is the folder to send to the daemon: dir resolved against this
// machine's working directory for a local daemon, or as typed for a remote
// one, where empty leaves the choice to the daemon.
func workDir(remote bool, dir string) (string, error) {
	if remote {
		return dir, nil
	}
	return filepath.Abs(dir)
}

// parseInterspersed parses fs from args, allowing flags after the positional
// arguments, and returns the positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func runHostCmd(dataDir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wada host <ls|add|remove>")
	}
	switch args[0] {
	case "ls":
		list, err := client.LoadHosts(dataDir)
		if err != nil {
			return err
		}
		for _, h := range list {
			target := h.RelayURL
			if target == "" {
				target = h.Endpoint
			}
			fmt.Printf("%s\t%s\t%s\n", h.Name, target, h.ServerID)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("host add", flag.ContinueOnError)
		endpoint := fs.String("endpoint", "", "Direct WebSocket endpoint (host[:port] or ws://...)")
		cred := fs.String("credential", "", "Credential or remote token")
		relayToken := fs.String("relay-token", "", "Bearer token the host's wada-relay requires")
		pos, err := parseInterspersed(fs, args[1:])
		if err != nil {
			return err
		}
		const hostAddUsage = "usage: wada host add <name> (<offer-url> | <host[:port]> | --endpoint <url>) [--credential <tok>] [--relay-token <tok>]"
		if len(pos) < 1 || len(pos) > 2 || (len(pos) == 2) == (*endpoint != "") {
			return errors.New(hostAddUsage)
		}
		rec := client.HostRecord{
			Name:       pos[0],
			Endpoint:   *endpoint,
			Credential: *cred,
			RelayToken: *relayToken,
		}
		if len(pos) == 2 {
			rec.Offer, rec.Endpoint = client.ParseHostTarget(pos[1])
		}
		return client.SaveHost(dataDir, rec)
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: wada host remove <name>")
		}
		return client.RemoveHost(dataDir, args[1])
	default:
		return fmt.Errorf("unknown host command %q", args[0])
	}
}

func runDaemonCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wada daemon <status|stop|pair|set-password>")
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
	case "pair":
		fs := flag.NewFlagSet("daemon pair", flag.ContinueOnError)
		appURL := fs.String("app-url", "", "Web app base URL for #offer= link")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		resp, err := c.PairingOffer(ctx, *appURL)
		if err != nil {
			return err
		}
		fmt.Printf("url:              %s\noffer:            %s\nserverId:         %s\nserverPubKey:     %s\nrelayUrl:         %s\ndirectUrl:        %s\nrequiresPassword: %v\n",
			resp.URL, resp.Offer, resp.ServerID, resp.ServerPubKey, resp.RelayURL, resp.DirectURL, resp.RequiresPassword)
		return nil
	case "set-password":
		fs := flag.NewFlagSet("daemon set-password", flag.ContinueOnError)
		pw := fs.String("password", "", "Password to set (or use --clear)")
		clearPW := fs.Bool("clear", false, "Clear daemon password and allow pairing token")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		target := *pw
		if *clearPW {
			target = ""
		} else if target == "" {
			if fs.NArg() > 0 {
				target = fs.Arg(0)
			} else if term.IsTerminal(int(os.Stdin.Fd())) {
				read := func(prompt string) (string, error) {
					fmt.Fprint(os.Stderr, prompt)
					raw, err := term.ReadPassword(int(os.Stdin.Fd()))
					fmt.Fprintln(os.Stderr)
					return string(raw), err
				}
				first, err := read("New daemon password: ")
				if err != nil {
					return err
				}
				// Clearing takes --clear: an empty entry is a slip, not a request.
				if first == "" {
					return errors.New("no password entered; nothing changed (use --clear to remove the password)")
				}
				again, err := read("Repeat the password: ")
				if err != nil {
					return err
				}
				if again != first {
					return errors.New("the passwords did not match; nothing changed")
				}
				target = first
			} else {
				return fmt.Errorf("usage: wada daemon set-password (--password PW | --clear)")
			}
		}
		if target == "" && !*clearPW {
			return errors.New("empty password; use --clear to remove the password")
		}
		if err := c.SetPassword(ctx, target); err != nil {
			return err
		}
		if target == "" {
			fmt.Println("daemon password cleared")
		} else {
			fmt.Println("daemon password updated")
		}
		return nil
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

func runPRCmd(ctx context.Context, c *client.Client, remote bool, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wada pr <status|create>")
	}
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("pr status", flag.ContinueOnError)
		dir := fs.String("dir", "", "Worktree or repository directory (default: the current one, for a local daemon)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		absDir, err := workDir(remote, *dir)
		if err != nil {
			return err
		}
		pr, err := c.WorktreePR(ctx, absDir)
		if err != nil {
			return err
		}
		if pr == nil {
			fmt.Println("no pull request (or origin host not authenticated with gh)")
			return nil
		}
		fmt.Printf("#%d\t%s\tchecks=%s\t%s\t%s\n", pr.Number, pr.State, pr.Checks, pr.Title, pr.URL)
		return nil
	case "create":
		fs := flag.NewFlagSet("pr create", flag.ContinueOnError)
		dir := fs.String("dir", "", "Worktree or repository directory (default: the current one, for a local daemon)")
		title := fs.String("title", "", "Pull request title")
		body := fs.String("body", "", "Pull request body")
		base := fs.String("base", "", "Base branch")
		draft := fs.Bool("draft", false, "Open as draft pull request")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*title) == "" {
			return fmt.Errorf("usage: wada pr create [--dir DIR] --title TITLE [--body BODY] [--base BRANCH] [--draft]")
		}
		absDir, err := workDir(remote, *dir)
		if err != nil {
			return err
		}
		pr, err := c.CreateWorktreePR(ctx, protocol.WorktreeCreatePRRequest{
			Directory: absDir,
			Title:     *title,
			Body:      *body,
			Base:      *base,
			Draft:     *draft,
		})
		if err != nil {
			return err
		}
		if pr != nil {
			fmt.Printf("#%d\t%s\n", pr.Number, pr.URL)
		}
		return nil
	default:
		return fmt.Errorf("unknown pr command %q", args[0])
	}
}

func runTerminalCmd(ctx context.Context, c *client.Client, remote bool, args []string) error {
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
		dir := fs.String("dir", "", "Working directory (default: the current one, for a local daemon)")
		sh := fs.String("shell", "", "Shell executable")
		name := fs.String("name", "", "Terminal name")
		cols := fs.Int("cols", 80, "Columns")
		rows := fs.Int("rows", 24, "Rows")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		absDir, err := workDir(remote, *dir)
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
		stopResize := watchResize(inFD, stream)
		defer stopResize()

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

		// Standard input is read on its own goroutine, so the command ends
		// when the terminal exits or the connection drops, not at the next
		// key press. A read still blocked then is abandoned with the process.
		inErr := make(chan error, 1)
		go func() {
			buf := make([]byte, 1024)
			for {
				n, rerr := os.Stdin.Read(buf)
				if n > 0 {
					// Detach on Ctrl+] (0x1d).
					if idx := bytes.IndexByte(buf[:n], 0x1d); idx >= 0 {
						if idx > 0 {
							_ = stream.WriteInput(buf[:idx])
						}
						inErr <- nil
						return
					}
					if werr := stream.WriteInput(buf[:n]); werr != nil {
						inErr <- werr
						return
					}
				}
				if rerr != nil {
					if rerr == io.EOF {
						// Input ended: keep showing output until the terminal does.
						<-done
						rerr = nil
					}
					inErr <- rerr
					return
				}
			}
		}()
		select {
		case <-done:
			return nil
		case err := <-inErr:
			return err
		}
	default:
		return fmt.Errorf("unknown terminal command %q", args[0])
	}
}

func runList(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	dir := fs.String("dir", "", "Filter by directory")
	workspace := fs.String("workspace", "", "Filter by workspace ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	page, err := c.Sessions(ctx, protocol.SessionQuery{Directory: *dir, Workspace: *workspace})
	if err != nil {
		return err
	}
	for _, s := range page.Sessions {
		fmt.Printf("%s\t%s\t%s\t%s\n", s.ID, s.Workspace, s.Directory, s.Title)
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

func runPrompt(ctx context.Context, c *client.Client, remote bool, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("dir", "", "Workspace directory (default: the current one, or a remote daemon's inbox)")
	workspace := fs.String("workspace", "", "Run in this workspace")
	newWorktree := fs.Bool("new-worktree", false, "Run in a new worktree workspace of the project at --dir")
	wt := addWorktreeFlags(fs)
	provider := fs.String("provider", "", "Agent provider/harness ID (default embedded PiG)")
	mode := fs.String("mode", "ask", "Access mode: ask or full")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("usage: wada run [--dir DIR | --workspace ID | --new-worktree ...] [--provider ID] [--mode ask|full] <prompt>")
	}
	create := protocol.CreateSessionRequest{IdempotencyKey: "cli_" + uuid.NewString(), Workspace: *workspace,
		Provider: *provider, Mode: *mode}
	switch {
	case *newWorktree:
		if *workspace != "" {
			return errors.New("use --workspace or --new-worktree, not both")
		}
		project, err := projectAt(ctx, c, remote, *dir)
		if err != nil {
			return err
		}
		req, err := wt.request(project)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, newWorktreeTimeout)
		w, err := c.CreateWorkspace(wctx, req)
		cancel()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "workspace: %s (%s on %s)\n", w.ID, w.Directory, w.Branch)
		create.Workspace = w.ID
	case *workspace == "":
		absDir, err := workDir(remote, *dir)
		if err != nil {
			return err
		}
		create.Directory = absDir
	}
	sess, err := c.Create(ctx, create)
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
		case protocol.EventInteractionRequested:
			if ev.Interaction != nil && !ev.Interaction.Settled {
				if err := c.AnswerInteraction(ctx, askInteraction(reader, *ev.Interaction)); err != nil {
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
			protocol.EventTurnStarted, protocol.EventModelChanged,
			protocol.EventSessionRenamed, protocol.EventSessionDeleted, protocol.EventModelsChanged,
			protocol.EventProviderAuth, protocol.EventWorkerUpdated, protocol.EventTerminalUpdated, protocol.EventWorkspaceUpdated:
		}
	}
	// The event stream ends only when the connection does; the turn may still
	// be running on the daemon.
	return fmt.Errorf("connection to wadad lost before the turn ended; session %s may still be running", sess.ID)
}

// askInteraction asks the user the agent's question on the terminal. An
// unreadable reply, or an invalid choice, cancels it.
func askInteraction(reader *bufio.Reader, in protocol.Interaction) protocol.InteractionAnswer {
	ans := protocol.InteractionAnswer{Session: in.Session, ID: in.ID}
	readLine := func(prompt string) (string, bool) {
		fmt.Fprint(os.Stderr, prompt)
		line, err := reader.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err == nil || line != ""
	}
	fmt.Fprintln(os.Stderr, in.Title)
	if in.Message != "" {
		fmt.Fprintln(os.Stderr, in.Message)
	}
	switch in.Kind {
	case protocol.InteractionConfirm:
		line, ok := readLine("[y/n]: ")
		if !ok {
			ans.Cancelled = true
			return ans
		}
		v := strings.ToLower(strings.TrimSpace(line))
		ans.Confirmed = v == "y" || v == "yes"
	case protocol.InteractionSelect:
		for i, o := range in.Options {
			fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, o)
		}
		line, ok := readLine("Choose a number (empty cancels): ")
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if !ok || err != nil || n < 1 || n > len(in.Options) {
			ans.Cancelled = true
			return ans
		}
		ans.Value = in.Options[n-1]
	case protocol.InteractionInput, protocol.InteractionEditor:
		prompt := "> "
		if in.Prefill != "" {
			prompt = fmt.Sprintf("[%s] > ", in.Prefill)
		} else if in.Placeholder != "" {
			prompt = fmt.Sprintf("(%s) > ", in.Placeholder)
		}
		line, ok := readLine(prompt)
		if !ok {
			ans.Cancelled = true
			return ans
		}
		if line == "" {
			line = in.Prefill
		}
		ans.Value = line
	default:
		ans.Cancelled = true
	}
	return ans
}
