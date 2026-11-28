package agent

import (
	"context"

	"github.com/xleotranx/wada-kit/kernel"
)

var (
	// SchedulesKey is an optional backend capability keeping schedules and
	// heartbeats.
	SchedulesKey = kernel.NewKey[Schedules]("agent.schedules")
	// ScheduleUpdated is emitted on the UI thread when a schedule is added,
	// changed, runs, or is removed (Removed set).
	ScheduleUpdated = kernel.NewTopic[Schedule]("agent.schedule-updated")
)

// ScheduleSpec is what a schedule does and when: Prompt on Cron (five
// fields, or @hourly, @daily, @weekly, @monthly) or Every (a duration such as
// "30m"), in the backend's local time. With Session it is a heartbeat, sent
// to that conversation and skipped while its turn runs; without, each run
// starts a new conversation in Workspace or Directory, on Provider or as
// Profile, in Mode.
type ScheduleSpec struct {
	Name      string
	Prompt    string
	Cron      string
	Every     string
	Session   SessionID
	Directory string
	Workspace string
	Provider  string
	Profile   string
	Mode      string
	Paused    bool
}

// ScheduleStatus is how a schedule's last run went.
type ScheduleStatus uint8

const (
	ScheduleNotRun  ScheduleStatus = iota
	ScheduleRan                    // the prompt was sent
	ScheduleSkipped                // the conversation was busy
	ScheduleFailed                 // see LastError; runs are not retried
)

// Schedule is a kept schedule and how its runs went.
type Schedule struct {
	ScheduleSpec
	ID          string
	Created     int64 // Unix ms
	NextRun     int64 // Unix ms; 0 while paused
	LastRun     int64
	LastStatus  ScheduleStatus
	LastError   string
	LastSession SessionID
	Removed     bool // on ScheduleUpdated, for a deleted one
}

// Heartbeat reports that the schedule nudges an existing conversation.
func (s Schedule) Heartbeat() bool { return s.Session != "" }

type Schedules interface {
	Schedules(ctx context.Context) ([]Schedule, error)
	CreateSchedule(ctx context.Context, spec ScheduleSpec) (Schedule, error)
	UpdateSchedule(ctx context.Context, id string, spec ScheduleSpec) (Schedule, error)
	DeleteSchedule(ctx context.Context, id string) error
	// RunSchedule runs it now, without changing its next time.
	RunSchedule(ctx context.Context, id string) error
}
