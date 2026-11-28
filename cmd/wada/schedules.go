package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/protocol"
)

const scheduleUsage = `usage:
  wada schedule ls
  wada schedule add (--every 30m | --cron "0 9 * * 1-5") [--name N]
                    (--session ID | [--dir DIR | --workspace ID] [--provider ID | --profile NAME])
                    [--mode ask|full] <prompt>
  wada schedule pause|resume|run|rm <id>`

func runScheduleCmd(ctx context.Context, c *client.Client, remote bool, args []string) error {
	if len(args) == 0 {
		return errors.New(scheduleUsage)
	}
	switch args[0] {
	case "ls":
		list, err := c.Schedules(ctx)
		if err != nil {
			return err
		}
		for _, sc := range list {
			when := sc.Every
			if sc.Cron != "" {
				when = sc.Cron
			}
			target := "new chat"
			if sc.Session != "" {
				target = "heartbeat " + sc.Session
			}
			next := "paused"
			if sc.NextRun != 0 {
				next = "next " + time.UnixMilli(sc.NextRun).Format("Jan 2 15:04")
			}
			last := "-"
			if sc.LastRun != 0 {
				last = string(sc.LastStatus) + " " + time.UnixMilli(sc.LastRun).Format("Jan 2 15:04")
				if sc.LastError != "" {
					last += ": " + sc.LastError
				}
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", sc.ID, sc.Name, when, target, next, last)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("schedule add", flag.ContinueOnError)
		var spec protocol.ScheduleSpec
		fs.StringVar(&spec.Name, "name", "", "Name (default: the prompt's first line)")
		fs.StringVar(&spec.Every, "every", "", "Interval, such as 30m or 2h")
		fs.StringVar(&spec.Cron, "cron", "", "Cron expression: minute hour day month weekday, or @hourly/@daily/@weekly")
		fs.StringVar(&spec.Session, "session", "", "Heartbeat: send to this conversation")
		dir := fs.String("dir", "", "Folder of each run's new conversation (default: the daemon's inbox)")
		fs.StringVar(&spec.Workspace, "workspace", "", "Workspace of each run's new conversation")
		fs.StringVar(&spec.Provider, "provider", "", "Agent harness of each run")
		fs.StringVar(&spec.Profile, "profile", "", "Agent profile of each run")
		fs.StringVar(&spec.Mode, "mode", "", "ask or full")
		pos, err := parseInterspersed(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New(scheduleUsage)
		}
		spec.Prompt = strings.Join(pos, " ")
		if *dir != "" {
			if spec.Directory, err = workDir(remote, *dir); err != nil {
				return err
			}
		}
		sc, err := c.CreateSchedule(ctx, spec)
		if err != nil {
			return err
		}
		fmt.Println(sc.ID)
		return nil
	case "pause", "resume":
		if len(args) != 2 {
			return errors.New(scheduleUsage)
		}
		sc, err := findSchedule(ctx, c, args[1])
		if err != nil {
			return err
		}
		sc.Paused = args[0] == "pause"
		_, err = c.UpdateSchedule(ctx, sc.ID, sc.ScheduleSpec)
		return err
	case "run":
		if len(args) != 2 {
			return errors.New(scheduleUsage)
		}
		return c.RunSchedule(ctx, args[1])
	case "rm":
		if len(args) != 2 {
			return errors.New(scheduleUsage)
		}
		return c.DeleteSchedule(ctx, args[1])
	}
	return errors.New(scheduleUsage)
}

func findSchedule(ctx context.Context, c *client.Client, id string) (protocol.Schedule, error) {
	list, err := c.Schedules(ctx)
	if err != nil {
		return protocol.Schedule{}, err
	}
	for _, sc := range list {
		if sc.ID == id {
			return sc, nil
		}
	}
	return protocol.Schedule{}, fmt.Errorf("no schedule %s", id)
}
