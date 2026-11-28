package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/protocol"
)

func runProjectCmd(ctx context.Context, c *client.Client, remote bool, args []string) error {
	const use = "usage: wada project <ls [--archived] | add [DIR] | rename <id> <name> | rm <id>>"
	if len(args) == 0 {
		return errors.New(use)
	}
	switch args[0] {
	case "ls":
		fs := flag.NewFlagSet("project ls", flag.ContinueOnError)
		archived := fs.Bool("archived", false, "Include archived projects")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		list, err := c.Projects(ctx, *archived)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, p := range list {
			state := ""
			if p.Archived != 0 {
				state = "archived"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Root, state)
		}
		return tw.Flush()
	case "add":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		root, err := workDir(remote, dir)
		if err != nil {
			return err
		}
		if root == "" {
			return errors.New("usage: wada project add DIR (a remote daemon needs the folder on its machine)")
		}
		added, err := c.AddProject(ctx, root)
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", added.Project.ID, added.Project.Name, added.Workspace.ID)
		return nil
	case "rename":
		if len(args) < 3 {
			return errors.New("usage: wada project rename <id> <name> (an empty name goes back to the folder's)")
		}
		return c.RenameProject(ctx, args[1], strings.Join(args[2:], " "))
	case "rm":
		if len(args) < 2 {
			return errors.New("usage: wada project rm <id>")
		}
		return c.ArchiveProject(ctx, args[1])
	}
	return errors.New(use)
}

func runWorkspaceCmd(ctx context.Context, c *client.Client, remote bool, args []string) error {
	const use = "usage: wada workspace <ls | create | rename <id> <title> | archive <id> [--force] | restore <id>>"
	if len(args) == 0 {
		return errors.New(use)
	}
	switch args[0] {
	case "ls":
		fs := flag.NewFlagSet("workspace ls", flag.ContinueOnError)
		project := fs.String("project", "", "Only this project's")
		archived := fs.Bool("archived", false, "Include archived workspaces")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		list, err := c.Workspaces(ctx, protocol.WorkspacesListRequest{Project: *project, Archived: *archived})
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, w := range list {
			state := string(w.Isolation)
			if w.Archived != 0 {
				state += ", archived"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", w.ID, w.Project, w.Branch, w.Directory, state)
		}
		return tw.Flush()
	case "create":
		req, err := parseCreate(ctx, c, remote, "workspace create", args[1:])
		if err != nil {
			return err
		}
		w, err := c.CreateWorkspace(ctx, req)
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", w.ID, w.Branch, w.Directory)
		return nil
	case "rename":
		if len(args) < 3 {
			return errors.New("usage: wada workspace rename <id> <title>")
		}
		return c.RenameWorkspace(ctx, args[1], strings.Join(args[2:], " "))
	case "archive":
		fs := flag.NewFlagSet("workspace archive", flag.ContinueOnError)
		force := fs.Bool("force", false, "Discard uncommitted changes and unpushed commits")
		pos, err := parseInterspersed(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return errors.New("usage: wada workspace archive <id> [--force]")
		}
		err = c.ArchiveWorkspace(ctx, pos[0], *force)
		var rerr *protocol.RPCError
		if errors.As(err, &rerr) && rerr.Code == protocol.ErrCodeConflict {
			return fmt.Errorf("%s; --force discards it", rerr.Message)
		}
		return err
	case "restore":
		if len(args) < 2 {
			return errors.New("usage: wada workspace restore <id>")
		}
		w, err := c.RestoreWorkspace(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", w.ID, w.Branch, w.Directory)
		return nil
	}
	return errors.New(use)
}

// worktreeFlags are the flags that say how a new worktree starts.
type worktreeFlags struct {
	base, checkout, slug, title, existing *string
	pr                                    *int
}

func addWorktreeFlags(fs *flag.FlagSet) worktreeFlags {
	return worktreeFlags{
		base:     fs.String("base", "", "Branch off this ref (default: origin's default branch)"),
		checkout: fs.String("checkout", "", "Check out this branch instead of branching off"),
		pr:       fs.Int("pr", 0, "Check out this pull request"),
		existing: fs.String("existing", "", "Use this worktree already on disk"),
		slug:     fs.String("slug", "", "Name the worktree and its new branch"),
		title:    fs.String("title", "", "The workspace's title"),
	}
}

// request turns the flags into a creation in project.
func (f worktreeFlags) request(project string) (protocol.WorkspaceCreateRequest, error) {
	req := protocol.WorkspaceCreateRequest{IdempotencyKey: "cli_" + uuid.NewString(), Project: project,
		Isolation: protocol.IsolationWorktree, Mode: protocol.StartBranchOff, From: *f.base, Slug: *f.slug, Title: *f.title}
	set := 0
	for _, on := range []bool{*f.base != "", *f.checkout != "", *f.pr != 0, *f.existing != ""} {
		if on {
			set++
		}
	}
	if set > 1 {
		return req, errors.New("use one of --base, --checkout, --pr and --existing")
	}
	switch {
	case *f.checkout != "":
		req.Mode, req.From = protocol.StartCheckoutBranch, *f.checkout
	case *f.pr != 0:
		req.Mode, req.From, req.PR = protocol.StartCheckoutPR, "", *f.pr
	case *f.existing != "":
		req.Isolation, req.Mode, req.From, req.Path = protocol.IsolationExisting, "", "", *f.existing
	}
	return req, nil
}

// parseCreate reads `workspace create` flags; the project is --project, or
// the one at --dir (default the current folder), added when it is new.
func parseCreate(ctx context.Context, c *client.Client, remote bool, name string, args []string) (protocol.WorkspaceCreateRequest, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	project := fs.String("project", "", "The project's ID")
	dir := fs.String("dir", "", "The project's folder, when --project isn't given (default: the current one)")
	wt := addWorktreeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return protocol.WorkspaceCreateRequest{}, err
	}
	id := *project
	if id == "" {
		var err error
		if id, err = projectAt(ctx, c, remote, *dir); err != nil {
			return protocol.WorkspaceCreateRequest{}, err
		}
	}
	if *wt.existing != "" && !remote {
		abs, err := workDir(false, *wt.existing)
		if err != nil {
			return protocol.WorkspaceCreateRequest{}, err
		}
		*wt.existing = abs
	}
	return wt.request(id)
}

// projectAt adds, or finds, the project at a folder.
func projectAt(ctx context.Context, c *client.Client, remote bool, dir string) (string, error) {
	root, err := workDir(remote, cmpOr(dir, "."))
	if err != nil {
		return "", err
	}
	if root == "" || root == "." && remote {
		return "", errors.New("a remote daemon needs --project or --dir with a folder on its machine")
	}
	added, err := c.AddProject(ctx, root)
	if err != nil {
		return "", err
	}
	return added.Project.ID, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// newWorktreeTimeout bounds making a worktree, which fetches first.
const newWorktreeTimeout = 2 * time.Minute
