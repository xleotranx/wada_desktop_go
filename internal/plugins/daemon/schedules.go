package daemon

import (
	"context"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/protocol"
)

func (p *Plugin) Schedules(ctx context.Context) ([]agent.Schedule, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	list, err := cli.Schedules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Schedule, len(list))
	for i, sc := range list {
		out[i] = fromProtoSchedule(sc)
	}
	return out, nil
}

func (p *Plugin) CreateSchedule(ctx context.Context, spec agent.ScheduleSpec) (agent.Schedule, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Schedule{}, err
	}
	sc, err := cli.CreateSchedule(ctx, toProtoScheduleSpec(spec))
	return fromProtoSchedule(sc), err
}

func (p *Plugin) UpdateSchedule(ctx context.Context, id string, spec agent.ScheduleSpec) (agent.Schedule, error) {
	cli, err := p.current()
	if err != nil {
		return agent.Schedule{}, err
	}
	sc, err := cli.UpdateSchedule(ctx, id, toProtoScheduleSpec(spec))
	return fromProtoSchedule(sc), err
}

func (p *Plugin) DeleteSchedule(ctx context.Context, id string) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.DeleteSchedule(ctx, id)
}

func (p *Plugin) RunSchedule(ctx context.Context, id string) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.RunSchedule(ctx, id)
}

func toProtoScheduleSpec(s agent.ScheduleSpec) protocol.ScheduleSpec {
	return protocol.ScheduleSpec{Name: s.Name, Prompt: s.Prompt, Cron: s.Cron, Every: s.Every, Session: string(s.Session),
		Directory: s.Directory, Workspace: s.Workspace, Provider: s.Provider, Profile: s.Profile, Mode: s.Mode, Paused: s.Paused}
}

func fromProtoSchedule(sc protocol.Schedule) agent.Schedule {
	s := sc.ScheduleSpec
	var st agent.ScheduleStatus
	switch sc.LastStatus {
	case protocol.ScheduleRan:
		st = agent.ScheduleRan
	case protocol.ScheduleSkipped:
		st = agent.ScheduleSkipped
	case protocol.ScheduleFailed:
		st = agent.ScheduleFailed
	}
	return agent.Schedule{
		ScheduleSpec: agent.ScheduleSpec{Name: s.Name, Prompt: s.Prompt, Cron: s.Cron, Every: s.Every,
			Session: agent.SessionID(s.Session), Directory: s.Directory, Workspace: s.Workspace, Provider: s.Provider,
			Profile: s.Profile, Mode: s.Mode, Paused: s.Paused},
		ID: sc.ID, Created: sc.Created, NextRun: sc.NextRun, LastRun: sc.LastRun, LastStatus: st,
		LastError: sc.LastError, LastSession: agent.SessionID(sc.LastSession), Removed: sc.Removed,
	}
}
